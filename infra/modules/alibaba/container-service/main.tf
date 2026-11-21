# Compute capability: Serverless App Engine (SAE) applications, one per spec service.
#
# Identity: each service gets a RAM role whose policy is derived from its `access` block.
# The alicloud provider (v2.2) has no argument to attach a RAM role to an SAE application,
# so binding the role to the application (SAE "identity authentication", RRSA) is a
# documented post-provision step. Secrets are not injected natively: the application
# fetches the secrets listed in secret_environment with its role (kms:GetSecretValue).

locals {
  tags     = merge(var.tags, { "hermes-component" = "compute" })
  services = { for s in var.services : s.name => s }

  # SAE CPU (millicores) / memory (MB) pairs that are both valid provider values and
  # listed SAE instance types, smallest first. The smallest pair that satisfies the
  # request is used, so 0.25 vCPU / 512 MB rounds up to 0.5 vCPU / 1 GB.
  sizes = [
    [500, 1024], [500, 2048],
    [1000, 1024], [1000, 2048], [1000, 4096],
    [2000, 2048], [2000, 4096], [2000, 8192], [2000, 16384],
    [4000, 4096], [4000, 8192], [4000, 16384], [4000, 32768],
    [8000, 8192], [8000, 16384], [8000, 32768], [8000, 65536],
    [16000, 16384], [16000, 32768], [16000, 65536],
    [32000, 65536], [32000, 131072],
  ]
  size = {
    for k, s in local.services : k => [for p in local.sizes : p if p[0] >= s.cpu * 1000 && p[1] >= s.memory_mb][0]
  }

  # SAE keeps at least one instance: min_instances = 0 is raised to 1 (no scale to zero).
  replicas = { for k, s in local.services : k => max(1, s.min_instances) }

  public = { for k, s in local.services : k => s if s.public }
  https  = var.certificate_id != ""

  ports = distinct([for s in var.services : s.port])
}

module "names" {
  source = "../naming"
  names = merge(
    {
      # SAE namespace short ID: [a-z0-9], at most 20 characters.
      namespace = { parts = [replace(var.project, "-", ""), var.environment], max_length = 20, delimiter = "" }
      sg        = { parts = [var.prefix, "services"], max_length = 128 }
    },
    { for k, _ in local.services : "app/${k}" => { parts = [var.prefix, k], max_length = 36 } },
    { for k, _ in local.services : "role/${k}" => { parts = [var.prefix, k], max_length = 64 } },
    { for k, _ in local.services : "policy/${k}" => { parts = [var.prefix, k, "access"], max_length = 128 } },
  )
}

resource "alicloud_sae_namespace" "this" {
  namespace_short_id        = module.names.names["namespace"]
  namespace_name            = var.prefix
  namespace_description     = "Hermes services for ${var.prefix}"
  enable_micro_registration = false
}

resource "alicloud_security_group" "services" {
  security_group_name = module.names.names["sg"]
  vpc_id              = var.vpc_id
  description         = "Hermes SAE services for ${var.prefix}"
  tags                = local.tags
}

# Service ports are reachable from inside the VPC only.
resource "alicloud_security_group_rule" "vpc" {
  for_each = toset([for p in local.ports : tostring(p)])

  security_group_id = alicloud_security_group.services.id
  type              = "ingress"
  ip_protocol       = "tcp"
  port_range        = "${each.key}/${each.key}"
  cidr_ip           = var.vpc_cidr
  policy            = "accept"
  priority          = 1
  description       = "Service port from the VPC"
}

# Public services: the SAE-managed CLB forwards and health-checks from 100.64.0.0/10,
# Alibaba Cloud's reserved load-balancer range (not the internet).
resource "alicloud_security_group_rule" "load_balancer" {
  for_each = toset(distinct([for s in values(local.public) : tostring(s.port)]))

  security_group_id = alicloud_security_group.services.id
  type              = "ingress"
  ip_protocol       = "tcp"
  port_range        = "${each.key}/${each.key}"
  cidr_ip           = "100.64.0.0/10"
  policy            = "accept"
  priority          = 1
  description       = "Service port from the Alibaba Cloud load balancer range"
}

# ---------------------------------------------------------------- identity (RAM)

locals {
  account = var.account_id

  oss_bucket_arn = { for b, n in var.bucket_names : b => "acs:oss:*:${local.account}:${n}" }
  topic_arn      = { for t, n in var.topic_names : t => "acs:mns:${var.region}:${local.account}:/topics/${n}/messages" }
  queue_arn      = { for q, n in var.queue_names : q => "acs:mns:${var.region}:${local.account}:/queues/${n}/messages" }
  secret_arn     = { for s, n in var.secret_names : s => "acs:kms:${var.region}:${local.account}:secret/${n}" }

  statements = {
    for k, s in local.services : k => concat(
      [for b, mode in s.access.buckets : {
        Effect   = "Allow"
        Action   = ["oss:ListObjects"]
        Resource = [local.oss_bucket_arn[b]]
      }],
      [for b, mode in s.access.buckets : {
        Effect   = "Allow"
        Action   = mode == "read-write" ? ["oss:GetObject", "oss:PutObject", "oss:DeleteObject"] : ["oss:GetObject"]
        Resource = ["${local.oss_bucket_arn[b]}/*"]
      }],
      # SSE-KMS objects need the bucket key: decrypt to read, generate data keys to write.
      var.bucket_kms_key_id != null && length(s.access.buckets) > 0 ? [{
        Effect   = "Allow"
        Action   = contains(values(s.access.buckets), "read-write") ? ["kms:Decrypt", "kms:GenerateDataKey"] : ["kms:Decrypt"]
        Resource = ["acs:kms:${var.region}:${local.account}:key/${var.bucket_kms_key_id}"]
      }] : [],
      length(s.access.publish) > 0 ? [{
        Effect   = "Allow"
        Action   = ["mns:PublishMessage"]
        Resource = [for t in s.access.publish : local.topic_arn[t]]
      }] : [],
      length(s.access.consume) > 0 ? [{
        Effect   = "Allow"
        Action   = ["mns:ReceiveMessage", "mns:DeleteMessage", "mns:ChangeMessageVisibility"]
        Resource = [for q in s.access.consume : local.queue_arn[q]]
      }] : [],
      length(s.secret_env) > 0 ? [{
        Effect   = "Allow"
        Action   = ["kms:GetSecretValue"]
        Resource = [for n in distinct(values(s.secret_env)) : local.secret_arn[n]]
      }] : [],
    )
  }

  # Keys come from the spec only (known at plan time on every engine); the documents may
  # contain apply-time values such as bucket names.
  needs_policy = {
    for k, s in local.services : k => k
    if length(s.access.buckets) + length(s.access.publish) + length(s.access.consume) + length(s.secret_env) > 0
  }

  policy_documents = {
    for k in keys(local.needs_policy) : k => jsonencode({ Version = "1", Statement = local.statements[k] })
  }

  # Explicit trusted principals only, never the account root (trusting
  # acs:ram::<account>:root would let any principal holding sts:AssumeRole become any
  # service). Default: the SAE service principal, which assumes the role when it is bound
  # to the application ("RAM role for application", a post-provision step). With
  # var.trusted_principals the listed RAM role/user ARNs replace the service principal.
  # An SAE RRSA (OIDC) binding adds its own federated trust statement out-of-band.
  trust_override  = length(var.trusted_principals) > 0
  trust_principal = { (local.trust_override ? "RAM" : "Service") = local.trust_override ? var.trusted_principals : ["sae.aliyuncs.com"] }

  trust_policy = jsonencode({
    Version = "1"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = local.trust_principal
    }]
  })
}

resource "alicloud_ram_role" "service" {
  for_each = local.services

  role_name                   = module.names.names["role/${each.key}"]
  description                 = "Hermes workload identity for service ${each.key} (${var.prefix})"
  assume_role_policy_document = local.trust_policy
  max_session_duration        = 3600
  tags                        = local.tags
}

resource "alicloud_ram_policy" "service" {
  for_each = local.needs_policy

  policy_name     = module.names.names["policy/${each.key}"]
  description     = "Least-privilege access for service ${each.key} (${var.prefix})"
  policy_document = local.policy_documents[each.key]
  rotate_strategy = "DeleteOldestNonDefaultVersionWhenLimitExceeded"
  tags            = local.tags
}

resource "alicloud_ram_role_policy_attachment" "service" {
  for_each = local.needs_policy

  role_name   = alicloud_ram_role.service[each.key].role_name
  policy_name = alicloud_ram_policy.service[each.key].policy_name
  policy_type = "Custom"
}

# ---------------------------------------------------------------- applications

resource "alicloud_sae_application" "this" {
  for_each = local.services

  app_name          = module.names.names["app/${each.key}"]
  app_description   = "Hermes service ${each.key}"
  namespace_id      = alicloud_sae_namespace.this.id
  package_type      = "Image"
  image_url         = each.value.image
  acr_instance_id   = var.acr_instance_id == "" ? null : var.acr_instance_id
  replicas          = local.replicas[each.key]
  cpu               = local.size[each.key][0]
  memory            = local.size[each.key][1]
  vpc_id            = var.vpc_id
  vswitch_id        = join(",", var.vswitch_ids) # instances spread across the zones' vSwitches
  security_group_id = alicloud_security_group.services.id
  command           = length(each.value.command) > 0 ? each.value.command[0] : null
  command_args_v2   = length(each.value.command) > 1 ? slice(each.value.command, 1, length(each.value.command)) : null

  envs = jsonencode([for name in sort(keys(var.service_environment[each.key])) : {
    name  = name
    value = var.service_environment[each.key][name]
  }])

  sls_configs = var.log_project == "" ? null : jsonencode([{
    projectName  = var.log_project
    logstoreName = var.logstore
    logType      = "stdout"
    logDir       = ""
    logtailName  = ""
  }])

  liveness_v2 {
    initial_delay_seconds = 15
    period_seconds        = 30
    timeout_seconds       = 5
    failure_threshold     = 3
    http_get {
      path   = each.value.health_check_path
      port   = each.value.port
      scheme = "HTTP"
    }
  }

  readiness_v2 {
    initial_delay_seconds = 5
    period_seconds        = 10
    timeout_seconds       = 5
    failure_threshold     = 3
    http_get {
      path   = each.value.health_check_path
      port   = each.value.port
      scheme = "HTTP"
    }
  }

  tags = local.tags
}

resource "alicloud_sae_application_scaling_rule" "cpu" {
  for_each = { for k, s in local.services : k => s if s.max_instances > local.replicas[k] }

  app_id              = alicloud_sae_application.this[each.key].id
  scaling_rule_name   = "hermes-cpu"
  scaling_rule_type   = "metric"
  scaling_rule_enable = true

  scaling_rule_metric {
    min_replicas = local.replicas[each.key]
    max_replicas = each.value.max_instances
    metrics {
      metric_type                       = "CPU"
      metric_target_average_utilization = 70
    }
  }
}

resource "alicloud_sae_load_balancer_internet" "this" {
  for_each = local.public

  app_id = alicloud_sae_application.this[each.key].id

  internet {
    protocol      = local.https ? "HTTPS" : "HTTP"
    https_cert_id = local.https ? var.certificate_id : null
    port          = local.https ? 443 : 80
    target_port   = each.value.port
  }

  lifecycle {
    precondition {
      condition     = !(var.protected && !local.https)
      error_message = "Public service ${each.key} in a protected environment needs HTTPS: set extensions.alibaba.certificate_id."
    }
  }
}
