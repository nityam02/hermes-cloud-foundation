# Output contract: infra/contract/outputs.md. No secret values; nothing is sensitive.

output "bindings" {
  description = "Non-secret connection metadata (contract shape)."
  value = {
    provider    = "alibaba"
    region      = var.region
    project     = var.project
    environment = var.environment

    network = {
      id   = module.network.vpc_id
      name = module.network.vpc_name
      cidr = module.network.cidr
    }

    services = {
      for k, s in try(module.compute[0].services, {}) : k => {
        url      = s.url
        identity = s.identity
      }
    }

    database = var.database.enabled ? {
      endpoint        = module.postgres[0].endpoint
      port            = module.postgres[0].port
      name            = module.postgres[0].database_name
      user            = "app"
      auth            = "password"
      credentials_ref = local.db_credentials_secret
      instance_id     = module.postgres[0].instance_id
    } : null

    buckets = module.storage.buckets
    topics  = module.messaging.topics
    secrets = local.secret_names_native

    observability = {
      log_destination = var.observability.enabled ? module.observability[0].log_project : ""
      otlp_endpoint   = local.otlp_endpoint_used
      alert_channel   = var.observability.enabled ? module.observability[0].alert_channel : ""
    }
  }
}

output "service_environment" {
  description = "Service name -> exact non-secret environment variables injected into the service."
  value       = local.service_environment
}

output "secret_environment" {
  description = "Service name -> env var name -> native secret name (references only; the service reads the value with its RAM role)."
  value       = local.secret_environment
}
