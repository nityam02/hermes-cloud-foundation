# GitHub Actions -> Alibaba Cloud federation for Hermes CI (RAM OIDC role SSO, no AccessKeys).
#
# Creates a RAM OIDC provider for https://token.actions.githubusercontent.com (audience
# sts.aliyuncs.com), a plan role and one apply role per environment. Trust policies pin
# oidc:iss, oidc:aud and the exact oidc:sub:
#   plan:          repo:<repo>:pull_request, repo:<repo>:ref:refs/heads/<branch>
#   apply (<env>): repo:<repo>:environment:<env>  (GitHub environment protection = human approval)
# Roles are named hermes-ci-<project>-*, so the deploy permissions (scoped to <project>-*) never
# cover the CI roles themselves, and an explicit deny blocks any change to them.
#
# The apply permission set is BROAD by necessity (VPC, NAT, security groups, SAE, SLB, RDS, OSS,
# MNS, KMS secrets containers, SLS, CloudMonitor, RAM roles for services). Data-plane reads and
# writes (object contents, secret values, messages, log contents) are explicitly denied.
#
# Status: implemented, mock-tested (tests/ci_identity.tftest.hcl); NOT tested against real
# Alibaba Cloud. The RAM action names below were taken from Alibaba's public RAM documentation
# and must be verified against your account before use.

data "alicloud_account" "current" {}

locals {
  issuer   = "https://token.actions.githubusercontent.com"
  audience = "sts.aliyuncs.com"
  account  = data.alicloud_account.current.id
  oidc_arn = var.create_oidc_provider ? alicloud_ims_oidc_provider.github[0].arn : var.oidc_provider_arn
  subject  = "repo:${var.github_repository}"
  ci_name  = "hermes-ci-${var.project}"

  plan_subjects = concat(
    var.plan_on_pull_request ? ["${local.subject}:pull_request"] : [],
    [for b in var.plan_branches : "${local.subject}:ref:refs/heads/${b}"],
  )

  trust = {
    for name, subjects in merge({ plan = local.plan_subjects }, { for e in var.environments : "apply-${e}" => ["${local.subject}:environment:${e}"] }) :
    name => jsonencode({
      Version = "1"
      Statement = [{
        Effect    = "Allow"
        Action    = "sts:AssumeRole"
        Principal = { Federated = [local.oidc_arn] }
        Condition = {
          StringEquals = {
            "oidc:iss" = local.issuer
            "oidc:aud" = local.audience
            "oidc:sub" = subjects
          }
        }
      }]
    })
  }

  state_objects = "acs:oss:*:${local.account}:${var.state_bucket}/${var.state_prefix}/${var.project}"
  lock_table    = var.lock_tablestore_instance == "" ? [] : ["acs:ots:*:${local.account}:instance/${var.lock_tablestore_instance}/table/${var.lock_table}"]
  lock_statement = var.lock_tablestore_instance == "" ? [] : [{
    Effect   = "Allow"
    Action   = ["ots:GetRow", "ots:PutRow", "ots:UpdateRow", "ots:DeleteRow", "ots:DescribeTable"]
    Resource = local.lock_table
  }]

  # Metadata reads used by refresh. Deliberately no object, secret-value, message or log reads.
  read_actions = [
    "vpc:Describe*", "ecs:DescribeSecurityGroup*", "ecs:DescribeZones", "ecs:DescribeRegions",
    "sae:Describe*", "sae:List*", "slb:Describe*", "rds:Describe*",
    "oss:GetBucket*", "oss:ListBuckets",
    "mns:GetQueueAttributes", "mns:GetTopicAttributes", "mns:GetSubscriptionAttributes", "mns:ListQueue", "mns:ListTopic", "mns:ListSubscriptionByTopic",
    "kms:DescribeKey", "kms:DescribeSecret", "kms:ListKeys", "kms:ListSecrets", "kms:ListAliases", "kms:ListResourceTags", "kms:GetSecretPolicy",
    "log:GetProject", "log:ListProject", "log:GetLogStore", "log:ListLogStores", "log:GetIndex",
    "cms:Describe*", "ram:GetRole", "ram:GetPolicy", "ram:GetPolicyVersion", "ram:ListPoliciesForRole", "ram:ListRoles", "ram:ListPolicies",
    "ims:GetOIDCProvider", "tag:ListTagResources",
  ]

  data_plane_deny = {
    Effect = "Deny"
    Action = [
      "oss:GetObject", "oss:GetObjectVersion", "oss:DeleteObject", "oss:DeleteObjectVersion",
      "kms:GetSecretValue", "kms:PutSecretValue", "kms:Decrypt",
      "mns:ReceiveMessage", "mns:SendMessage", "mns:BatchReceiveMessage", "mns:BatchSendMessage", "mns:DeleteMessage", "mns:PublishMessage",
      "log:GetLogStoreLogs", "log:GetLogs", "log:GetHistograms", "log:PostLogStoreLogs", "log:PullLogs",
    ]
    Resource = ["acs:oss:*:${local.account}:${var.project}-*/*", "acs:kms:*:${local.account}:*", "acs:mns:*:${local.account}:*", "acs:log:*:${local.account}:*"]
  }

  identity_deny = {
    Effect   = "Deny"
    Action   = ["ram:*", "ims:*"]
    Resource = ["acs:ram::${local.account}:role/${local.ci_name}-*", "acs:ram::${local.account}:oidc-provider/*", "acs:ram::${local.account}:user/*"]
  }

  plan_policy = jsonencode({
    Version = "1"
    Statement = concat([
      { Effect = "Allow", Action = local.read_actions, Resource = ["*"] },
      { Effect = "Allow", Action = ["oss:ListObjects", "oss:GetBucketInfo"], Resource = ["acs:oss:*:${local.account}:${var.state_bucket}"] },
      { Effect = "Allow", Action = ["oss:GetObject"], Resource = ["${local.state_objects}/*"] },
    ], local.lock_statement, [local.data_plane_deny, local.identity_deny])
  })

  apply_policy = {
    for e in var.environments : e => jsonencode({
      Version = "1"
      Statement = concat([
        { Effect = "Allow", Action = local.read_actions, Resource = ["*"] },
        { Effect = "Allow", Action = ["oss:ListObjects", "oss:GetBucketInfo"], Resource = ["acs:oss:*:${local.account}:${var.state_bucket}"] },
        { Effect = "Allow", Action = ["oss:GetObject", "oss:PutObject"], Resource = ["${local.state_objects}/${e}/*"] },
        {
          # Control-plane services the composition manages. Inherently broad (Resource "*").
          Effect = "Allow"
          Action = [
            "vpc:*", "ecs:CreateSecurityGroup", "ecs:DeleteSecurityGroup", "ecs:ModifySecurityGroupAttribute", "ecs:ModifySecurityGroupPolicy",
            "ecs:AuthorizeSecurityGroup", "ecs:AuthorizeSecurityGroupEgress", "ecs:RevokeSecurityGroup", "ecs:RevokeSecurityGroupEgress",
            "ecs:ModifySecurityGroupRule", "ecs:ModifySecurityGroupEgressRule", "ecs:TagResources", "ecs:UntagResources",
            "sae:*", "slb:*", "rds:*", "cms:*", "kms:CreateKey", "kms:CreateSecret", "kms:DeleteSecret", "kms:RestoreSecret",
            "kms:UpdateSecret", "kms:SetSecretPolicy", "kms:TagResource", "kms:UntagResource", "kms:TagResources", "kms:UntagResources",
            "kms:EnableKey", "kms:DisableKey", "kms:ScheduleKeyDeletion", "kms:CancelKeyDeletion", "kms:UpdateRotationPolicy", "kms:CreateAlias", "kms:DeleteAlias",
            "tag:TagResources", "tag:UntagResources",
          ]
          Resource = ["*"]
        },
        {
          Effect   = "Allow"
          Action   = ["oss:*"]
          Resource = ["acs:oss:*:${local.account}:${var.project}-*"]
        },
        {
          Effect   = "Allow"
          Action   = ["mns:*"]
          Resource = ["acs:mns:*:${local.account}:/topics/${var.project}-*", "acs:mns:*:${local.account}:/queues/${var.project}-*"]
        },
        {
          Effect   = "Allow"
          Action   = ["log:*"]
          Resource = ["acs:log:*:${local.account}:project/${var.project}-*"]
        },
        {
          Effect   = "Allow"
          Action   = ["ram:CreateRole", "ram:DeleteRole", "ram:UpdateRole", "ram:CreatePolicy", "ram:DeletePolicy", "ram:CreatePolicyVersion", "ram:DeletePolicyVersion", "ram:SetDefaultPolicyVersion", "ram:AttachPolicyToRole", "ram:DetachPolicyFromRole", "ram:TagResources", "ram:UntagResources"]
          Resource = ["acs:ram::${local.account}:role/${var.project}-*", "acs:ram::${local.account}:policy/${var.project}-*"]
        },
        {
          Effect   = "Allow"
          Action   = ["ram:PassRole"]
          Resource = ["acs:ram::${local.account}:role/${var.project}-*"]
        },
      ], local.lock_statement, [local.data_plane_deny, local.identity_deny])
    })
  }

  role_tags = merge(var.tags, { "hermes-component" = "identity", "hermes-managed-by" = "hermes", "hermes-project" = var.project })
}

resource "alicloud_ims_oidc_provider" "github" {
  count = var.create_oidc_provider ? 1 : 0

  oidc_provider_name = var.oidc_provider_name
  issuer_url         = local.issuer
  client_ids         = [local.audience]
  fingerprints       = var.oidc_fingerprints
  description        = "GitHub Actions OIDC for Hermes CI (${var.github_repository})"

  lifecycle {
    precondition {
      condition     = length(var.oidc_fingerprints) > 0 && var.oidc_provider_arn == ""
      error_message = "Creating the OIDC provider needs oidc_fingerprints (verified by you) and no oidc_provider_arn."
    }
  }
}

resource "alicloud_ram_role" "plan" {
  role_name                   = "${local.ci_name}-plan"
  description                 = "Hermes CI plan role for ${var.github_repository} (read-only, state lock only)."
  assume_role_policy_document = local.trust["plan"]
  max_session_duration        = 3600
  tags                        = merge(local.role_tags, { "hermes-ci-role" = "plan" })

  lifecycle {
    precondition {
      condition     = var.create_oidc_provider || can(regex("^acs:ram::[0-9]+:oidc-provider/.+$", var.oidc_provider_arn))
      error_message = "oidc_provider_arn must be a RAM OIDC provider ARN when create_oidc_provider = false."
    }
  }
}

resource "alicloud_ram_policy" "plan" {
  policy_name     = "${local.ci_name}-plan"
  description     = "Hermes CI plan permissions"
  policy_document = local.plan_policy
  tags            = local.role_tags
}

resource "alicloud_ram_role_policy_attachment" "plan" {
  role_name   = alicloud_ram_role.plan.role_name
  policy_name = alicloud_ram_policy.plan.policy_name
  policy_type = "Custom"
}

resource "alicloud_ram_role" "apply" {
  for_each = toset(var.environments)

  role_name                   = "${local.ci_name}-apply-${each.key}"
  description                 = "Hermes CI apply role for ${var.github_repository}, GitHub environment ${each.key}."
  assume_role_policy_document = local.trust["apply-${each.key}"]
  max_session_duration        = 3600
  tags                        = merge(local.role_tags, { "hermes-ci-role" = "apply", "hermes-environment" = each.key })
}

resource "alicloud_ram_policy" "apply" {
  for_each = toset(var.environments)

  policy_name     = "${local.ci_name}-apply-${each.key}"
  description     = "Hermes CI deploy permissions for ${each.key}"
  policy_document = local.apply_policy[each.key]
  tags            = merge(local.role_tags, { "hermes-environment" = each.key })
}

resource "alicloud_ram_role_policy_attachment" "apply" {
  for_each = toset(var.environments)

  role_name   = alicloud_ram_role.apply[each.key].role_name
  policy_name = alicloud_ram_policy.apply[each.key].policy_name
  policy_type = "Custom"
}
