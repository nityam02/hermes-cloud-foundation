# Hermes provider composition for Alibaba Cloud (contract v1alpha1).
#
# The calling root module configures the provider, e.g.
#   provider "alicloud" { region = var.region }
# so that var.region (used in bindings and RAM resource ARNs) matches the provider region.
#
# Extension keys (extensions.alibaba.*, or the same keys at the top level of
# var.extensions): certificate_id, kms_instance_id, otlp_endpoint, otlp_protocol,
# zone_ids, db_instance_type, db_credentials_secret, oss_redundancy_type, mns_sse,
# acr_instance_id, workload_role_trusted_principals. See docs/providers/alibaba.md.

data "alicloud_account" "current" {}

data "alicloud_zones" "vswitch" {
  available_resource_creation = "VSwitch"
}

locals {
  ext = try(var.extensions.alibaba, var.extensions)

  certificate_id        = try(tostring(local.ext.certificate_id), "")
  kms_instance_id       = try(tostring(local.ext.kms_instance_id), "")
  otlp_endpoint         = try(tostring(local.ext.otlp_endpoint), "")
  otlp_protocol         = try(tostring(local.ext.otlp_protocol), "grpc")
  zone_ids_override     = try(tolist(local.ext.zone_ids), [])
  db_instance_type      = try(tostring(local.ext.db_instance_type), "")
  db_credentials_secret = try(tostring(local.ext.db_credentials_secret), "${local.prefix}-db-credentials")
  oss_redundancy_type   = try(tostring(local.ext.oss_redundancy_type), "")
  mns_sse               = try(tobool(local.ext.mns_sse), false)
  acr_instance_id       = try(tostring(local.ext.acr_instance_id), "")
  # RAM role/user ARNs that replace the SAE service principal in the workload role trust
  # policies (never the account root; validated by the compute module).
  workload_role_trusted_principals = try(tolist(local.ext.workload_role_trusted_principals), [])

  prefix = "${var.project}-${var.environment}"
  tags = merge(var.labels, {
    "hermes-project"     = var.project
    "hermes-environment" = var.environment
    "hermes-managed-by"  = "hermes"
  })

  available_zones = length(local.zone_ids_override) > 0 ? local.zone_ids_override : data.alicloud_zones.vswitch.ids
  zone_ids        = slice(local.available_zones, 0, min(length(local.available_zones), var.network.zones))

  services     = var.compute.type == "containerized" ? var.compute.services : []
  buckets      = var.storage.enabled ? var.storage.buckets : []
  topics       = var.messaging.enabled ? var.messaging.topics : []
  secret_names = var.secrets.enabled ? var.secrets.names : []

  # A key is needed for customer-managed encryption, and for Secrets Manager (KMS 3.0
  # secrets must be encrypted by a key in the same KMS instance).
  cmk        = var.security.customer_managed_keys
  need_key   = local.cmk || length(local.secret_names) > 0
  cmk_key_id = local.cmk ? module.kms[0].key_id : null

  subscription_names = flatten([for t in local.topics : [for s in t.subscriptions : s.name]])
  otlp_endpoint_used = var.observability.enabled && var.observability.tracing ? local.otlp_endpoint : ""
}

# Spec-level guardrails evaluated at plan time (cross-capability checks that the contract
# variables cannot express).
resource "terraform_data" "guardrails" {
  input = local.prefix

  lifecycle {
    precondition {
      condition     = length(local.zone_ids) == var.network.zones
      error_message = "The region offers ${length(local.available_zones)} vSwitch zones but network.zones is ${var.network.zones} (set extensions.alibaba.zone_ids or lower network.zones)."
    }

    precondition {
      condition     = !(var.protected && anytrue([for s in local.services : s.public]) && local.certificate_id == "")
      error_message = "Public services in a protected environment need HTTPS: set extensions.alibaba.certificate_id (an SSL Certificates Service certificate ID)."
    }

    precondition {
      condition     = length(local.secret_names) == 0 || local.kms_instance_id != ""
      error_message = "KMS 3.0 creates secrets inside a KMS instance: set extensions.alibaba.kms_instance_id to use secrets on Alibaba Cloud."
    }

    precondition {
      condition = alltrue(flatten([for s in local.services : concat(
        [for b in keys(s.access.buckets) : contains([for x in local.buckets : x.name], b)],
        [for t in s.access.publish : contains([for x in local.topics : x.name], t)],
        [for q in s.access.consume : contains(local.subscription_names, q)],
        [for n in values(s.secret_env) : contains(local.secret_names, n)],
        [!s.access.database || var.database.enabled],
      )]))
      error_message = "A service access/secret_env entry references a bucket, topic, subscription, secret or database that is not enabled in the spec."
    }

    precondition {
      condition     = !var.database.enabled || var.database.high_availability || var.database.size != "xlarge" || local.db_instance_type != ""
      error_message = "No verified RDS for PostgreSQL Basic Edition class for size xlarge: enable database.high_availability or set extensions.alibaba.db_instance_type."
    }
  }
}

module "network" {
  source = "../../../modules/alibaba/network"

  prefix    = local.prefix
  cidr      = var.network.cidr
  zone_ids  = local.zone_ids
  nat       = var.network.nat
  protected = var.protected
  tags      = local.tags
}

module "kms" {
  source = "../../../modules/alibaba/kms"
  count  = local.need_key ? 1 : 0

  prefix          = local.prefix
  kms_instance_id = local.kms_instance_id
  protected       = var.protected
  tags            = local.tags
}

module "postgres" {
  source = "../../../modules/alibaba/postgres"
  count  = var.database.enabled ? 1 : 0

  prefix                 = local.prefix
  engine_version         = var.database.version
  size                   = var.database.size
  instance_type          = local.db_instance_type
  storage_gb             = var.database.storage_gb
  max_storage_gb         = var.database.max_storage_gb
  high_availability      = var.database.high_availability
  backup_retention_days  = var.database.backup_retention_days
  point_in_time_recovery = var.database.point_in_time_recovery
  deletion_protection    = var.database.deletion_protection || var.protected
  protected              = var.protected
  kms_key_id             = local.cmk_key_id
  vpc_id                 = module.network.vpc_id
  vpc_cidr               = module.network.cidr
  vswitch_ids            = module.network.vswitch_ids
  zone_ids               = module.network.zone_ids
  tags                   = local.tags

  depends_on = [terraform_data.guardrails]
}

module "storage" {
  source = "../../../modules/alibaba/object-storage"

  prefix          = local.prefix
  buckets         = local.buckets
  kms_key_id      = local.cmk_key_id
  protected       = var.protected
  redundancy_type = local.oss_redundancy_type
  tags            = local.tags
}

module "messaging" {
  source = "../../../modules/alibaba/messaging"

  prefix      = local.prefix
  topics      = local.topics
  sse_enabled = local.mns_sse
  kms_key_id  = local.cmk_key_id
  tags        = local.tags
}

module "secrets" {
  source = "../../../modules/alibaba/secrets"
  count  = length(local.secret_names) > 0 ? 1 : 0

  prefix            = local.prefix
  names             = local.secret_names
  kms_instance_id   = local.kms_instance_id
  encryption_key_id = module.kms[0].key_id
  protected         = var.protected
  tags              = local.tags

  depends_on = [terraform_data.guardrails]
}

module "observability" {
  source = "../../../modules/alibaba/observability"
  count  = var.observability.enabled ? 1 : 0

  prefix               = local.prefix
  log_retention_days   = var.observability.log_retention_days
  alert_email          = var.observability.alert_email
  database_enabled     = var.database.enabled
  database_instance_id = var.database.enabled ? module.postgres[0].instance_id : ""
  tags                 = local.tags
}

# The binding contract (data-contracts §2): computed once, injected into SAE and output.
locals {
  service_environment = {
    for s in local.services : s.name => merge(
      s.env,
      {
        HERMES_PROVIDER    = "alibaba"
        HERMES_REGION      = var.region
        HERMES_PROJECT     = var.project
        HERMES_ENVIRONMENT = var.environment
        HERMES_SERVICE     = s.name
      },
      { for b in keys(s.access.buckets) : "HERMES_BUCKET_${upper(replace(b, "-", "_"))}" => try(module.storage.buckets[b].name, "") },
      { for t in s.access.publish : "HERMES_TOPIC_${upper(replace(t, "-", "_"))}" => lookup(module.messaging.topic_names, t, "") },
      { for q in s.access.consume : "HERMES_SUBSCRIPTION_${upper(replace(q, "-", "_"))}" => lookup(module.messaging.queue_names, q, "") },
      s.access.database && var.database.enabled ? {
        HERMES_DB_HOST = module.postgres[0].endpoint
        HERMES_DB_PORT = "5432"
        HERMES_DB_NAME = module.postgres[0].database_name
        HERMES_DB_USER = "app"
        HERMES_DB_AUTH = "password"
      } : {},
      var.observability.enabled ? {
        OTEL_SERVICE_NAME           = s.name
        OTEL_RESOURCE_ATTRIBUTES    = "service.namespace=${var.project},deployment.environment=${var.environment},cloud.provider=alibaba_cloud,cloud.region=${var.region}"
        OTEL_EXPORTER_OTLP_ENDPOINT = local.otlp_endpoint_used
        OTEL_EXPORTER_OTLP_PROTOCOL = local.otlp_protocol
      } : {},
    )
  }

  secret_names_native = try(module.secrets[0].secrets, {})

  secret_environment = {
    for s in local.services : s.name => {
      for env_name, secret in s.secret_env : env_name => lookup(local.secret_names_native, secret, "")
    }
  }
}

module "compute" {
  source = "../../../modules/alibaba/container-service"
  count  = var.compute.type == "containerized" ? 1 : 0

  project             = var.project
  environment         = var.environment
  prefix              = local.prefix
  region              = var.region
  account_id          = data.alicloud_account.current.id
  services            = local.services
  service_environment = local.service_environment
  vpc_id              = module.network.vpc_id
  vpc_cidr            = module.network.cidr
  vswitch_ids         = module.network.vswitch_ids
  bucket_names        = { for k, b in module.storage.buckets : k => b.name }
  bucket_kms_key_id   = local.cmk_key_id
  topic_names         = module.messaging.topic_names
  queue_names         = module.messaging.queue_names
  secret_names        = local.secret_names_native
  certificate_id      = local.certificate_id
  acr_instance_id     = local.acr_instance_id
  trusted_principals  = local.workload_role_trusted_principals
  log_project         = var.observability.enabled ? module.observability[0].log_project : ""
  logstore            = var.observability.enabled ? module.observability[0].logstore : ""
  protected           = var.protected
  tags                = local.tags

  depends_on = [terraform_data.guardrails]
}
