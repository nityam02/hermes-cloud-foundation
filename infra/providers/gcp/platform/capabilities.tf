# -----------------------------------------------------------------------------
# Capability modules. Every module waits for the APIs it needs; modules using the
# customer-managed key also wait for the key grants (module.kms) so a resource is
# never created before its service agent may use the key.
# -----------------------------------------------------------------------------

module "network" {
  source = "../../../modules/gcp/network"

  names = {
    network               = module.naming.names["network"]
    subnet                = module.naming.names["subnet"]
    router                = module.naming.names["router"]
    nat                   = module.naming.names["nat"]
    psa                   = module.naming.names["psa"]
    firewall_deny_ingress = module.naming.names["firewall_deny_ingress"]
  }
  region                  = var.region
  cidr                    = var.network.cidr
  nat                     = var.network.nat
  private_services_access = local.database
  flow_logs               = local.obs

  depends_on = [google_project_service.this]
}

# --- customer-managed encryption keys ----------------------------------------

data "google_storage_project_service_account" "gcs" {
  count = local.cmk && length(local.buckets) > 0 ? 1 : 0
}

resource "google_project_service_identity" "sql" {
  provider = google-beta
  count    = local.cmk && local.database ? 1 : 0

  project = local.project_id
  service = "sqladmin.googleapis.com"

  depends_on = [google_project_service.this]
}

resource "google_project_service_identity" "secretmanager" {
  provider = google-beta
  count    = local.cmk && length(local.secrets) > 0 ? 1 : 0

  project = local.project_id
  service = "secretmanager.googleapis.com"

  depends_on = [google_project_service.this]
}

locals {
  # Static keys (known at plan time) -> service agent members.
  kms_encrypters = merge(
    local.cmk && length(local.buckets) > 0 ? { storage = "serviceAccount:${data.google_storage_project_service_account.gcs[0].email_address}" } : {},
    local.cmk && length(local.topics) > 0 ? { pubsub = "serviceAccount:service-${local.project_number}@gcp-sa-pubsub.iam.gserviceaccount.com" } : {},
    local.cmk && local.database ? { sql = "serviceAccount:${google_project_service_identity.sql[0].email}" } : {},
    local.cmk && length(local.secrets) > 0 ? { secretmanager = "serviceAccount:${google_project_service_identity.secretmanager[0].email}" } : {},
  )
  kms_key = local.cmk ? module.kms[0].key_id : ""
}

module "kms" {
  source = "../../../modules/gcp/kms"
  count  = local.cmk ? 1 : 0

  key_ring_name              = module.naming.names["key_ring"]
  key_name                   = module.naming.names["crypto_key"]
  location                   = var.region
  labels                     = module.naming.component_labels["secrets"]
  destroy_scheduled_duration = var.protected ? "7776000s" : "2592000s"
  encrypters                 = local.kms_encrypters

  depends_on = [google_project_service.this]
}

# --- data services -----------------------------------------------------------

module "storage" {
  source = "../../../modules/gcp/object-storage"

  buckets = {
    for k, b in local.buckets : k => {
      name           = module.naming.names["bucket/${k}"]
      versioning     = b.versioning
      retention_days = b.retention_days
    }
  }
  location                          = var.region
  enable_cmek                       = local.cmk
  kms_key                           = local.kms_key
  noncurrent_version_retention_days = var.protected ? 90 : 30
  labels                            = module.naming.component_labels["storage"]

  depends_on = [google_project_service.this, module.kms]
}

module "messaging" {
  source = "../../../modules/gcp/messaging"

  topics = {
    for k, t in local.topics : k => {
      name            = module.naming.names["topic/${k}"]
      retention_hours = t.retention_hours
    }
  }
  subscriptions = {
    for k, s in local.subscriptions : k => {
      name                          = module.naming.names["sub/${k}"]
      topic                         = s.topic
      ack_deadline_seconds          = s.ack_deadline_seconds
      max_delivery_attempts         = s.max_delivery_attempts
      retention_hours               = s.retention_hours
      dead_letter_topic_name        = s.max_delivery_attempts > 0 ? module.naming.names["dlq/${k}"] : ""
      dead_letter_subscription_name = s.max_delivery_attempts > 0 ? module.naming.names["dlq/${k}"] : ""
    }
  }
  region         = var.region
  project_number = local.project_number
  enable_cmek    = local.cmk
  kms_key        = local.kms_key
  labels         = module.naming.component_labels["messaging"]

  depends_on = [google_project_service.this, module.kms]
}

module "secrets" {
  source = "../../../modules/gcp/secrets"

  secrets             = { for k in local.secrets : k => module.naming.names["secret/${k}"] }
  region              = var.region
  enable_cmek         = local.cmk
  kms_key             = local.kms_key
  version_destroy_ttl = var.protected ? "2592000s" : "604800s"
  labels              = module.naming.component_labels["secrets"]

  depends_on = [google_project_service.this, module.kms]
}

module "postgres" {
  source = "../../../modules/gcp/postgres"
  count  = local.database ? 1 : 0

  name                   = module.naming.names["database"]
  region                 = var.region
  database_version       = var.database.version
  tier                   = local.database_tiers[var.database.size]
  storage_gb             = var.database.storage_gb
  max_storage_gb         = var.database.max_storage_gb
  high_availability      = var.database.high_availability
  backup_retention_days  = var.database.backup_retention_days
  point_in_time_recovery = var.database.point_in_time_recovery
  deletion_protection    = var.database.deletion_protection || var.protected
  private_network        = module.network.network_id
  enable_cmek            = local.cmk
  kms_key                = local.kms_key
  iam_users              = { for k in keys(local.db_services) : k => google_service_account.service[k].email }
  labels                 = module.naming.component_labels["database"]

  # module.network includes the private services access peering.
  depends_on = [google_project_service.this, module.network, module.kms]
}

# --- observability -----------------------------------------------------------

locals {
  otlp_endpoint   = local.tracing ? "http://localhost:4317" : ""
  sql_database_id = local.database ? "${local.project_id}:${module.postgres[0].instance_name}" : ""

  log_filter = join(" OR ", concat(
    ["(resource.type=\"gce_subnetwork\" AND resource.labels.subnetwork_name=\"${module.naming.names["subnet"]}\")"],
    var.network.nat ? ["(resource.type=\"nat_gateway\" AND resource.labels.gateway_name=\"${module.naming.names["nat"]}\")"] : [],
    [for k in keys(local.services) : "(resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"${module.naming.names["run/${k}"]}\")"],
    local.database ? ["(resource.type=\"cloudsql_database\" AND resource.labels.database_id=\"${local.sql_database_id}\")"] : [],
  ))
}

module "observability" {
  source = "../../../modules/gcp/observability"

  project_id   = local.project_id
  region       = var.region
  logs_enabled = local.obs
  names = {
    log_bucket    = module.naming.names["log_bucket"]
    log_sink      = module.naming.names["log_sink"]
    log_exclusion = module.naming.names["log_exclusion"]
  }
  log_retention_days = var.observability.log_retention_days
  log_filter         = local.log_filter
  alert_email        = local.obs ? var.observability.alert_email : ""
  display_prefix     = local.prefix
  cloud_run_services = { for k in keys(local.services) : k => module.naming.names["run/${k}"] }
  sql_enabled        = local.database
  sql_database_id    = local.sql_database_id
  labels             = module.naming.component_labels["observability"]

  depends_on = [google_project_service.this]
}
