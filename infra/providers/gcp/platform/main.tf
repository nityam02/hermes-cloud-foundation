# -----------------------------------------------------------------------------
# Hermes GCP composition (contract v1alpha1).
#
# The provider block (project, region, credentials) is rendered by hermes; this
# module only declares required providers. Extensions read (extensions.gcp.*):
#   vpc_egress           "private-ranges-only" (default) | "all-traffic"
#   otel_collector_image collector sidecar image (default pinned contrib image)
# See docs/providers/gcp.md.
# -----------------------------------------------------------------------------

locals {
  prefix        = "${var.project}-${var.environment}"
  containerized = var.compute.type == "containerized"

  services = { for s in(local.containerized ? var.compute.services : []) : s.name => s }
  buckets  = var.storage.enabled ? { for b in var.storage.buckets : b.name => b } : {}
  topics   = var.messaging.enabled ? { for t in var.messaging.topics : t.name => t } : {}
  secrets  = var.secrets.enabled ? toset(var.secrets.names) : toset([])

  subscription_list = flatten([
    for t in values(local.topics) : [
      for s in t.subscriptions : merge(s, { topic = t.name, retention_hours = t.retention_hours })
    ]
  ])
  # Grouped so duplicate names surface as a spec_guard error, not a map-key crash.
  subscriptions = { for name, group in { for s in local.subscription_list : s.name => s... } : name => group[0] }

  database    = var.database.enabled
  db_services = { for k, s in local.services : k => s if s.access.database }
  obs         = var.observability.enabled
  tracing     = local.obs && var.observability.tracing
  metrics     = local.obs && var.observability.metrics
  cmk         = var.security.customer_managed_keys

  # Cloud SQL Enterprise custom tiers (vCPU-memory MiB).
  database_tiers = {
    small  = "db-custom-1-3840"
    medium = "db-custom-2-7680"
    large  = "db-custom-4-15360"
    xlarge = "db-custom-8-30720"
  }

  known_extensions     = ["vpc_egress", "otel_collector_image"]
  extension_keys       = try(keys(var.extensions), [])
  ext_vpc_egress       = try(var.extensions.vpc_egress, "private-ranges-only")
  ext_otel_image       = try(var.extensions.otel_collector_image, "otel/opentelemetry-collector-contrib:0.130.0")
  cloud_run_vpc_egress = local.ext_vpc_egress == "all-traffic" ? "ALL_TRAFFIC" : "PRIVATE_RANGES_ONLY"

  # The provider's configured project (rendered by hermes) and its number.
  project_id     = data.google_client_config.this.project
  project_number = data.google_project.this.number

  apis = toset(concat(
    ["compute.googleapis.com", "iam.googleapis.com"],
    local.containerized ? ["run.googleapis.com"] : [],
    local.database ? ["sqladmin.googleapis.com", "servicenetworking.googleapis.com"] : [],
    length(local.buckets) > 0 ? ["storage.googleapis.com"] : [],
    length(local.topics) > 0 ? ["pubsub.googleapis.com"] : [],
    length(local.secrets) > 0 ? ["secretmanager.googleapis.com"] : [],
    local.cmk ? ["cloudkms.googleapis.com"] : [],
    local.obs ? ["logging.googleapis.com", "monitoring.googleapis.com"] : [],
    local.tracing ? ["cloudtrace.googleapis.com"] : [],
  ))
}

data "google_client_config" "this" {}

data "google_project" "this" {
  project_id = data.google_client_config.this.project
}

# Suffix for names that must be globally unique (buckets) or cannot be reused after
# deletion (KMS key rings, log buckets).
resource "random_id" "suffix" {
  byte_length = 4
}

# Cloud SQL blocks instance-name reuse for up to a week; a new suffix whenever a
# change forces a replacement lets the replacement be created.
resource "random_id" "database" {
  count = local.database ? 1 : 0

  byte_length = 2
  keepers = {
    customer_managed_keys = tostring(local.cmk)
    region                = var.region
  }
}

module "naming" {
  source = "../../../modules/gcp/naming"

  project     = var.project
  environment = var.environment
  labels      = var.labels

  resources = merge(
    {
      network               = { name = "vpc", max_length = 63 }
      subnet                = { name = "subnet", max_length = 63 }
      router                = { name = "router", max_length = 63 }
      nat                   = { name = "nat", max_length = 63 }
      psa                   = { name = "psa", max_length = 63 }
      firewall_deny_ingress = { name = "deny-all-ingress", max_length = 63 }
      key_ring              = { name = "kms", max_length = 63, suffix = random_id.suffix.hex }
      crypto_key            = { name = "cmek", max_length = 63 }
      log_bucket            = { name = "logs", max_length = 100, suffix = random_id.suffix.hex }
      log_sink              = { name = "logs", max_length = 100 }
      log_exclusion         = { name = "logs", max_length = 100 }
    },
    local.database ? { database = { name = "pg", max_length = 63, suffix = random_id.database[0].hex } } : {},
    { for k in keys(local.services) : "run/${k}" => { name = k, max_length = 49 } },
    { for k in keys(local.services) : "sa/${k}" => { name = k, max_length = 30 } },
    { for k in keys(local.buckets) : "bucket/${k}" => { name = k, max_length = 63, suffix = random_id.suffix.hex } },
    { for k in keys(local.topics) : "topic/${k}" => { name = k, max_length = 255 } },
    { for k in keys(local.subscriptions) : "sub/${k}" => { name = k, max_length = 255 } },
    { for k, s in local.subscriptions : "dlq/${k}" => { name = "${k}-dlq", max_length = 255 } if s.max_delivery_attempts > 0 },
    { for k in local.secrets : "secret/${k}" => { name = k, max_length = 255 } },
  )
}

resource "google_project_service" "this" {
  for_each = local.apis

  service            = each.value
  disable_on_destroy = false
}

# -----------------------------------------------------------------------------
# Spec cross-checks the variable types cannot express. hermes validates the same
# rules earlier; these keep direct OpenTofu/Terraform use safe.
# -----------------------------------------------------------------------------
resource "terraform_data" "spec_guard" {
  lifecycle {
    precondition {
      condition     = alltrue([for k in local.extension_keys : contains(local.known_extensions, k)])
      error_message = "Unknown extensions.gcp key. Supported keys: vpc_egress, otel_collector_image."
    }

    precondition {
      condition     = contains(["private-ranges-only", "all-traffic"], local.ext_vpc_egress)
      error_message = "extensions.gcp.vpc_egress must be private-ranges-only or all-traffic."
    }

    precondition {
      condition     = can(regex("^[a-z0-9][^\\s]*$", local.ext_otel_image))
      error_message = "extensions.gcp.otel_collector_image must be a non-empty container image reference without spaces."
    }

    precondition {
      condition     = var.compute.type != "none" || length(var.compute.services) == 0
      error_message = "compute.services must be empty when compute.type is none."
    }

    precondition {
      condition     = !startswith(var.project, "goog") && !strcontains(var.project, "google")
      error_message = "On GCP the project name must not start with \"goog\" or contain \"google\" (Cloud Storage and Pub/Sub reject such names)."
    }

    precondition {
      condition     = length(local.subscription_list) == length(local.subscriptions)
      error_message = "Messaging subscription names must be unique across all topics."
    }

    precondition {
      condition = alltrue(flatten([
        for s in values(local.services) : [for b in keys(s.access.buckets) : contains(keys(local.buckets), b)]
      ]))
      error_message = "A service access.buckets entry names a bucket that is not in storage.buckets (or storage is disabled)."
    }

    precondition {
      condition = alltrue(flatten([
        for s in values(local.services) : [for t in s.access.publish : contains(keys(local.topics), t)]
      ]))
      error_message = "A service access.publish entry names a topic that is not in messaging.topics (or messaging is disabled)."
    }

    precondition {
      condition = alltrue(flatten([
        for s in values(local.services) : [for c in s.access.consume : contains(keys(local.subscriptions), c)]
      ]))
      error_message = "A service access.consume entry names a subscription that is not in messaging.topics[*].subscriptions."
    }

    precondition {
      condition     = local.database || length(local.db_services) == 0
      error_message = "A service has access.database = true but database.enabled is false."
    }

    precondition {
      condition = alltrue(flatten([
        for s in values(local.services) : [for n in values(s.secret_env) : contains(local.secrets, n)]
      ]))
      error_message = "A service secret_env value names a secret that is not in secrets.names (or secrets are disabled)."
    }

    precondition {
      condition = alltrue(flatten([
        for s in values(local.services) : [for k in keys(s.env) : !startswith(k, "HERMES_") && !startswith(k, "OTEL_")]
      ]))
      error_message = "Service env keys must not start with HERMES_ or OTEL_ (reserved for the binding contract)."
    }
  }
}
