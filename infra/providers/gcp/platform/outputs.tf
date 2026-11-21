# Outputs exactly as infra/contract/outputs.md. No secret values; references only.

output "bindings" {
  description = "Non-secret connection metadata (contract v1alpha1)."
  value = {
    provider    = "gcp"
    region      = var.region
    project     = var.project
    environment = var.environment

    network = {
      id   = module.network.network_id
      name = module.network.network_name
      cidr = var.network.cidr
    }

    services = {
      for k in keys(local.services) : k => {
        url      = module.service[k].url
        identity = google_service_account.service[k].email
      }
    }

    # IAM database authentication: one database user per service with
    # access.database (see service_environment[*].HERMES_DB_USER). `user` is that
    # user when exactly one service has database access, otherwise "".
    database = local.database ? {
      endpoint        = module.postgres[0].private_ip
      port            = 5432
      name            = module.postgres[0].database_name
      user            = length(module.postgres[0].iam_users) == 1 ? one(values(module.postgres[0].iam_users)) : ""
      auth            = "iam"
      credentials_ref = ""
      instance_id     = module.postgres[0].instance_name
    } : null

    buckets = {
      for k, n in module.storage.names : k => {
        name = n
        url  = "gs://${n}"
      }
    }

    topics = {
      for k, id in module.messaging.topic_ids : k => {
        id            = id
        subscriptions = { for sk, sid in module.messaging.subscription_ids : sk => sid if local.subscriptions[sk].topic == k }
      }
    }

    secrets = module.secrets.ids

    observability = {
      log_destination = module.observability.log_destination
      otlp_endpoint   = local.otlp_endpoint
      alert_channel   = module.observability.alert_channel
    }
  }
}

output "service_environment" {
  description = "Service name -> the exact non-secret environment injected into it."
  value       = local.service_environment
}

output "secret_environment" {
  description = "Service name -> env var name -> Secret Manager secret resource name (never values)."
  value       = local.secret_environment
}
