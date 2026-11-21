# GitHub Actions -> AWS federation for Hermes CI (OIDC, no long-lived keys).
#
# Creates:
#   - the IAM OIDC provider for token.actions.githubusercontent.com (optional; one per account);
#   - role <project>-ci-plan: read-only + state read + lock-file write; trusted ONLY for the exact
#     subjects repo:<repo>:pull_request and repo:<repo>:ref:refs/heads/<branch>;
#   - role <project>-ci-apply-<env> per environment: deploy permissions + state read/write for
#     that environment only; trusted ONLY for repo:<repo>:environment:<env>, so every assumption
#     passes the GitHub environment's protection rules (required reviewers = human approval).
# No wildcard subjects, 1-hour sessions, CI roles under the /hermes-ci/ path.
#
# The deploy permission set is explicit but inherently broad: an identity that can create VPCs,
# databases, IAM roles and KMS keys can do a lot of damage. Guardrails: resource names are scoped
# to <project>-*, attachable managed policies and PassRole targets are restricted, data-plane reads
# and writes are denied, the CI roles cannot modify themselves, and an optional permissions
# boundary is enforced on every role the pipeline creates. See docs/security/iam-strategy.md.
#
# Status: implemented, mock-tested (tests/ci_identity.tftest.hcl); NOT tested against real AWS.

data "aws_caller_identity" "current" {}

data "aws_partition" "current" {}

locals {
  issuer       = "token.actions.githubusercontent.com"
  partition    = data.aws_partition.current.partition
  account      = data.aws_caller_identity.current.account_id
  path         = "/hermes-ci/"
  oidc_arn     = var.create_oidc_provider ? aws_iam_openid_connect_provider.github[0].arn : var.oidc_provider_arn
  role_prefix  = "${var.project}-ci"
  repo_subject = "repo:${var.github_repository}"

  plan_subjects = concat(
    var.plan_on_pull_request ? ["${local.repo_subject}:pull_request"] : [],
    [for b in var.plan_branches : "${local.repo_subject}:ref:refs/heads/${b}"],
  )
  apply_subjects = { for e in var.environments : e => "${local.repo_subject}:environment:${e}" }

  # State layout: <prefix>/<project>/<environment>/aws/terraform.tfstate (+ .tflock).
  state_bucket_arn  = "arn:${local.partition}:s3:::${var.state_bucket}"
  state_project_arn = "${local.state_bucket_arn}/${var.state_prefix}/${var.project}"
  lock_table_arn    = var.state_lock_table == "" ? "" : "arn:${local.partition}:dynamodb:*:${local.account}:table/${var.state_lock_table}"

  # ARN patterns for the resources the Hermes AWS composition names <project>-<environment>-*.
  arn = {
    roles        = "arn:${local.partition}:iam::${local.account}:role/${var.project}-*"
    ci_roles     = "arn:${local.partition}:iam::${local.account}:role${local.path}*"
    buckets      = "arn:${local.partition}:s3:::${var.project}-*"
    topics       = "arn:${local.partition}:sns:*:${local.account}:${var.project}-*"
    queues       = "arn:${local.partition}:sqs:*:${local.account}:${var.project}-*"
    secrets      = "arn:${local.partition}:secretsmanager:*:${local.account}:secret:${var.project}-*"
    rds_secrets  = "arn:${local.partition}:secretsmanager:*:${local.account}:secret:rds!*"
    log_groups   = "arn:${local.partition}:logs:*:${local.account}:log-group:/hermes/${var.project}/*"
    kms_keys     = "arn:${local.partition}:kms:*:${local.account}:key/*"
    kms_aliases  = "arn:${local.partition}:kms:*:${local.account}:alias/hermes/${var.project}-*"
    ssm_public   = "arn:${local.partition}:ssm:*::parameter/aws/service/*"
    service_role = "arn:${local.partition}:iam::*:role/aws-service-role/*"
  }

  passable_services = ["ecs-tasks.amazonaws.com", "monitoring.rds.amazonaws.com", "vpc-flow-logs.amazonaws.com"]
  service_linked    = ["ecs.amazonaws.com", "elasticloadbalancing.amazonaws.com", "rds.amazonaws.com", "ecs.application-autoscaling.amazonaws.com"]
  attachable_policies = [
    "arn:${local.partition}:iam::aws:policy/service-role/AmazonRDSEnhancedMonitoringRole",
    "arn:${local.partition}:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy",
  ]

  kms_state = var.state_kms_key_arn == "" ? [] : [{
    Sid      = "StateKey"
    Effect   = "Allow"
    Action   = ["kms:Decrypt", "kms:Encrypt", "kms:GenerateDataKey", "kms:DescribeKey"]
    Resource = var.state_kms_key_arn
  }]
  lock_table = var.state_lock_table == "" ? [] : [{
    Sid      = "LegacyLockTable"
    Effect   = "Allow"
    Action   = ["dynamodb:DescribeTable", "dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:DeleteItem"]
    Resource = local.lock_table_arn
  }]

  trust = {
    for name, subjects in merge({ plan = local.plan_subjects }, { for e, s in local.apply_subjects : "apply-${e}" => [s] }) :
    name => jsonencode({
      Version = "2012-10-17"
      Statement = [{
        Sid       = "GitHubActionsOIDC"
        Effect    = "Allow"
        Principal = { Federated = local.oidc_arn }
        Action    = "sts:AssumeRoleWithWebIdentity"
        Condition = {
          StringEquals = {
            "${local.issuer}:aud" = "sts.amazonaws.com"
            "${local.issuer}:sub" = subjects
          }
        }
      }]
    })
  }

  # Data reads/writes no CI role needs (tofu never reads object contents, secret values, messages
  # or log events). Explicit denies also cap any extra managed policy attached later.
  data_plane_deny = [
    {
      Sid         = "DenyObjectDataOutsideState"
      Effect      = "Deny"
      Action      = ["s3:GetObject", "s3:GetObjectVersion", "s3:PutObject", "s3:DeleteObject", "s3:DeleteObjectVersion"]
      NotResource = ["${local.state_project_arn}/*"]
    },
    {
      Sid    = "DenySecretAndMessageData"
      Effect = "Deny"
      Action = [
        "secretsmanager:GetSecretValue", "secretsmanager:PutSecretValue", "secretsmanager:BatchGetSecretValue",
        "sqs:ReceiveMessage", "sqs:SendMessage", "sqs:DeleteMessage", "sqs:PurgeQueue", "sns:Publish",
        "kinesis:GetRecords", "kinesis:PutRecord", "kinesis:PutRecords",
        "logs:GetLogEvents", "logs:FilterLogEvents", "logs:StartQuery", "logs:StartLiveTail",
        "rds:DownloadDBLogFilePortion", "rds:DownloadCompleteDBLogFile", "ec2:GetPasswordData", "ec2:GetConsoleOutput",
      ]
      Resource = "*"
    },
    {
      Sid         = "DenyParameterValues"
      Effect      = "Deny"
      Action      = ["ssm:GetParameter", "ssm:GetParameters", "ssm:GetParametersByPath"]
      NotResource = [local.arn.ssm_public]
    },
    {
      Sid         = "DenyTableItems"
      Effect      = "Deny"
      Action      = ["dynamodb:GetItem", "dynamodb:BatchGetItem", "dynamodb:Query", "dynamodb:Scan", "dynamodb:PutItem", "dynamodb:DeleteItem", "dynamodb:UpdateItem", "dynamodb:BatchWriteItem"]
      NotResource = var.state_lock_table == "" ? ["arn:${local.partition}:dynamodb:*:${local.account}:table/hermes-none"] : [local.lock_table_arn]
    },
  ]

  identity_deny = [
    {
      Sid      = "DenyCiSelfModification"
      Effect   = "Deny"
      Action   = ["iam:*"]
      Resource = [local.arn.ci_roles, "arn:${local.partition}:iam::${local.account}:oidc-provider/${local.issuer}"]
    },
    {
      Sid      = "DenyLongLivedCredentialsAndAccountChanges"
      Effect   = "Deny"
      Action   = ["iam:*User*", "iam:*AccessKey*", "iam:*LoginProfile*", "iam:*OpenIDConnectProvider*", "iam:*SAMLProvider*", "iam:*AccountPasswordPolicy", "organizations:*", "account:*"]
      Resource = "*"
    },
  ]

  plan_policy = jsonencode({
    Version = "2012-10-17"
    Statement = concat([
      {
        Sid      = "StateList"
        Effect   = "Allow"
        Action   = ["s3:ListBucket", "s3:GetBucketLocation", "s3:GetBucketVersioning"]
        Resource = local.state_bucket_arn
      },
      {
        Sid      = "StateRead"
        Effect   = "Allow"
        Action   = ["s3:GetObject"]
        Resource = "${local.state_project_arn}/*"
      },
      {
        # Plans take the S3 native lock: write/delete only the lock objects, never state.
        Sid      = "StateLockFile"
        Effect   = "Allow"
        Action   = ["s3:PutObject", "s3:DeleteObject"]
        Resource = "${local.state_project_arn}/*.tflock"
      },
    ], local.kms_state, local.lock_table, local.data_plane_deny, local.identity_deny)
  })

  boundary_condition = var.workload_permissions_boundary_arn == "" ? {} : {
    Condition = { StringEquals = { "iam:PermissionsBoundary" = var.workload_permissions_boundary_arn } }
  }

  apply_policy = {
    for e in var.environments : e => jsonencode({
      Version = "2012-10-17"
      Statement = concat([
        {
          Sid      = "StateList"
          Effect   = "Allow"
          Action   = ["s3:ListBucket", "s3:GetBucketLocation", "s3:GetBucketVersioning"]
          Resource = local.state_bucket_arn
        },
        {
          Sid      = "StateReadWriteThisEnvironment"
          Effect   = "Allow"
          Action   = ["s3:GetObject", "s3:PutObject"]
          Resource = "${local.state_project_arn}/${e}/*"
        },
        {
          Sid      = "StateLockFile"
          Effect   = "Allow"
          Action   = ["s3:DeleteObject"]
          Resource = "${local.state_project_arn}/${e}/*.tflock"
        },
        {
          # Control-plane services the composition manages. Inherently broad (Resource "*"):
          # these services do not support name-scoped creation in a practical way.
          Sid    = "ControlPlane"
          Effect = "Allow"
          Action = [
            "ec2:Describe*", "ec2:*Vpc*", "ec2:*Subnet*", "ec2:*RouteTable*", "ec2:CreateRoute", "ec2:DeleteRoute", "ec2:ReplaceRoute",
            "ec2:*InternetGateway*", "ec2:*NatGateway*", "ec2:*Address*", "ec2:*SecurityGroup*", "ec2:CreateTags", "ec2:DeleteTags", "ec2:*FlowLogs",
            "ecs:*", "elasticloadbalancing:*", "application-autoscaling:*", "cloudwatch:*", "rds:*",
            "logs:DescribeLogGroups", "kms:ListAliases", "secretsmanager:ListSecrets", "sts:GetCallerIdentity",
          ]
          Resource = "*"
        },
        {
          Sid      = "ProjectLogGroups"
          Effect   = "Allow"
          Action   = ["logs:*"]
          Resource = [local.arn.log_groups, "${local.arn.log_groups}:*"]
        },
        {
          Sid      = "ProjectBucketsMessagingSecrets"
          Effect   = "Allow"
          Action   = ["s3:*", "sns:*", "sqs:*", "secretsmanager:CreateSecret", "secretsmanager:DeleteSecret", "secretsmanager:DescribeSecret", "secretsmanager:UpdateSecret", "secretsmanager:RestoreSecret", "secretsmanager:TagResource", "secretsmanager:UntagResource", "secretsmanager:GetResourcePolicy", "secretsmanager:PutResourcePolicy", "secretsmanager:DeleteResourcePolicy"]
          Resource = [local.arn.buckets, local.arn.topics, local.arn.queues, local.arn.secrets]
        },
        {
          # RDS creates the master user secret (manage_master_user_password) with the caller's rights.
          Sid      = "RdsManagedMasterSecret"
          Effect   = "Allow"
          Action   = ["secretsmanager:CreateSecret", "secretsmanager:TagResource", "secretsmanager:DescribeSecret", "secretsmanager:RotateSecret"]
          Resource = local.arn.rds_secrets
        },
        {
          Sid       = "CreateTaggedKeys"
          Effect    = "Allow"
          Action    = ["kms:CreateKey"]
          Resource  = "*"
          Condition = { StringEquals = { "aws:RequestTag/hermes-project" = var.project } }
        },
        {
          Sid       = "ManageProjectKeys"
          Effect    = "Allow"
          Action    = ["kms:DescribeKey", "kms:GetKeyPolicy", "kms:PutKeyPolicy", "kms:GetKeyRotationStatus", "kms:EnableKeyRotation", "kms:ScheduleKeyDeletion", "kms:CancelKeyDeletion", "kms:TagResource", "kms:UntagResource", "kms:ListResourceTags", "kms:CreateGrant", "kms:Encrypt", "kms:GenerateDataKey"]
          Resource  = local.arn.kms_keys
          Condition = { StringEquals = { "aws:ResourceTag/hermes-project" = var.project } }
        },
        {
          Sid      = "ManageProjectKeyAliases"
          Effect   = "Allow"
          Action   = ["kms:CreateAlias", "kms:DeleteAlias", "kms:UpdateAlias"]
          Resource = [local.arn.kms_aliases, local.arn.kms_keys]
        },
        {
          Sid      = "ReadProjectRoles"
          Effect   = "Allow"
          Action   = ["iam:GetRole", "iam:GetRolePolicy", "iam:ListRolePolicies", "iam:ListAttachedRolePolicies", "iam:ListInstanceProfilesForRole", "iam:ListRoleTags"]
          Resource = local.arn.roles
        },
        merge({
          Sid      = "WriteProjectRoles"
          Effect   = "Allow"
          Action   = ["iam:CreateRole", "iam:PutRolePolicy", "iam:PutRolePermissionsBoundary"]
          Resource = local.arn.roles
        }, local.boundary_condition),
        {
          Sid      = "MaintainProjectRoles"
          Effect   = "Allow"
          Action   = ["iam:DeleteRole", "iam:UpdateRole", "iam:UpdateAssumeRolePolicy", "iam:DeleteRolePolicy", "iam:TagRole", "iam:UntagRole"]
          Resource = local.arn.roles
        },
        merge({
          Sid      = "AttachOnlyServicePolicies"
          Effect   = "Allow"
          Action   = ["iam:AttachRolePolicy", "iam:DetachRolePolicy"]
          Resource = local.arn.roles
          Condition = merge(
            { ArnEquals = { "iam:PolicyARN" = local.attachable_policies } },
            var.workload_permissions_boundary_arn == "" ? {} : { StringEquals = { "iam:PermissionsBoundary" = var.workload_permissions_boundary_arn } },
          )
        }),
        {
          Sid       = "PassProjectRolesToServices"
          Effect    = "Allow"
          Action    = ["iam:PassRole"]
          Resource  = local.arn.roles
          Condition = { StringEquals = { "iam:PassedToService" = local.passable_services } }
        },
        {
          Sid       = "ServiceLinkedRoles"
          Effect    = "Allow"
          Action    = ["iam:CreateServiceLinkedRole"]
          Resource  = local.arn.service_role
          Condition = { StringEquals = { "iam:AWSServiceName" = local.service_linked } }
        },
        {
          Sid      = "KeepPermissionsBoundary"
          Effect   = "Deny"
          Action   = ["iam:DeleteRolePermissionsBoundary"]
          Resource = local.arn.roles
        },
      ], local.kms_state, local.lock_table, local.data_plane_deny, local.identity_deny)
    })
  }
}

resource "aws_iam_openid_connect_provider" "github" {
  count = var.create_oidc_provider ? 1 : 0

  url            = "https://${local.issuer}"
  client_id_list = ["sts.amazonaws.com"]
  tags           = merge(var.tags, { "hermes-component" = "identity", "hermes-managed-by" = "hermes" })

  lifecycle {
    precondition {
      condition     = var.oidc_provider_arn == ""
      error_message = "Set either create_oidc_provider = true or oidc_provider_arn, not both."
    }
  }
}

resource "aws_iam_role" "plan" {
  name                 = "${local.role_prefix}-plan"
  path                 = local.path
  description          = "Hermes CI plan role for ${var.github_repository} (read-only, state lock only)."
  assume_role_policy   = local.trust["plan"]
  max_session_duration = 3600
  tags                 = merge(var.tags, { "hermes-component" = "identity", "hermes-managed-by" = "hermes", "hermes-ci-role" = "plan" })

  lifecycle {
    precondition {
      condition     = var.create_oidc_provider || can(regex("^arn:aws[a-z-]*:iam::[0-9]{12}:oidc-provider/token\\.actions\\.githubusercontent\\.com$", var.oidc_provider_arn))
      error_message = "oidc_provider_arn must be the ARN of the token.actions.githubusercontent.com provider when create_oidc_provider = false."
    }
  }
}

resource "aws_iam_role_policy" "plan" {
  name   = "hermes-plan"
  role   = aws_iam_role.plan.id
  policy = local.plan_policy
}

resource "aws_iam_role_policy_attachment" "plan" {
  for_each = toset(var.plan_policy_arns)

  role       = aws_iam_role.plan.name
  policy_arn = each.value
}

resource "aws_iam_role" "apply" {
  for_each = toset(var.environments)

  name                 = "${local.role_prefix}-apply-${each.key}"
  path                 = local.path
  description          = "Hermes CI apply role for ${var.github_repository}, GitHub environment ${each.key}."
  assume_role_policy   = local.trust["apply-${each.key}"]
  max_session_duration = 3600
  tags                 = merge(var.tags, { "hermes-component" = "identity", "hermes-managed-by" = "hermes", "hermes-ci-role" = "apply", "hermes-environment" = each.key })
}

resource "aws_iam_role_policy" "apply" {
  for_each = toset(var.environments)

  name   = "hermes-deploy"
  role   = aws_iam_role.apply[each.key].id
  policy = local.apply_policy[each.key]
}

resource "aws_iam_role_policy_attachment" "apply" {
  for_each = { for pair in setproduct(var.environments, var.apply_policy_arns) : "${pair[0]}|${pair[1]}" => { env = pair[0], arn = pair[1] } }

  role       = aws_iam_role.apply[each.value.env].name
  policy_arn = each.value.arn
}
