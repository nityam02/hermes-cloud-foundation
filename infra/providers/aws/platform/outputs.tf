# Outputs exactly as infra/contract/outputs.md. No secret values: secrets are ARNs only.

output "bindings" {
  description = "Non-secret connection metadata (contract shape)."

  value = {
    provider    = "aws"
    region      = var.region
    project     = var.project
    environment = var.environment

    network = {
      id   = module.network.vpc_id
      name = module.network.name
      cidr = module.network.cidr
    }

    services = {
      for k, s in local.services : k => {
        url      = module.container_service[0].service_urls[k]
        identity = module.container_service[0].task_role_arns[k]
      }
    }

    database = local.db_enabled ? {
      endpoint        = module.postgres[0].address
      port            = module.postgres[0].port
      name            = module.postgres[0].db_name
      user            = module.postgres[0].app_user
      auth            = module.postgres[0].auth
      credentials_ref = module.postgres[0].master_secret_arn
      instance_id     = module.postgres[0].identifier
    } : null

    buckets = { for k, b in module.object_storage.buckets : k => { name = b.name, url = "s3://${b.name}" } }

    topics = {
      for k, t in module.messaging.topics : k => {
        id            = t.arn
        subscriptions = { for sk, s in t.subscriptions : sk => s.url }
      }
    }

    secrets = module.secrets.arns

    observability = {
      log_destination = module.observability.log_destination
      otlp_endpoint   = local.otlp_endpoint
      alert_channel   = local.obs.enabled ? module.observability.alert_topic_arn : ""
    }
  }

  precondition {
    condition     = length(setsubtract(local.extension_keys, keys(local.ext))) == 0
    error_message = "Unknown extensions.aws key(s): ${join(", ", setsubtract(local.extension_keys, keys(local.ext)))}. Supported: ${join(", ", keys(local.ext))}."
  }

  precondition {
    condition     = contains(["X86_64", "ARM64"], local.ext.cpu_architecture)
    error_message = "extensions.aws.cpu_architecture must be X86_64 or ARM64."
  }

  precondition {
    condition     = length(local.ext.alb_ingress_cidrs) > 0 && alltrue([for c in local.ext.alb_ingress_cidrs : can(cidrnetmask(c))])
    error_message = "extensions.aws.alb_ingress_cidrs must be a non-empty list of IPv4 CIDRs."
  }

  precondition {
    condition     = local.ext.acm_certificate_arn == "" || can(regex("^arn:aws[a-z-]*:acm:${var.region}:[0-9]{12}:certificate/.+$", local.ext.acm_certificate_arn))
    error_message = "extensions.aws.acm_certificate_arn must be an ACM certificate ARN in ${var.region}."
  }

  precondition {
    condition     = length(local.missing_buckets) == 0
    error_message = "access.buckets refers to buckets that are not defined (or storage is disabled): ${join(", ", local.missing_buckets)}."
  }

  precondition {
    condition     = length(local.missing_topics) == 0
    error_message = "access.publish refers to topics that are not defined (or messaging is disabled): ${join(", ", local.missing_topics)}."
  }

  precondition {
    condition     = length(local.missing_subscriptions) == 0
    error_message = "access.consume refers to subscriptions that are not defined (or messaging is disabled): ${join(", ", local.missing_subscriptions)}."
  }

  precondition {
    condition     = length(local.missing_secrets) == 0
    error_message = "secret_env refers to secrets that are not defined (or secrets are disabled): ${join(", ", local.missing_secrets)}."
  }

  precondition {
    condition     = length(local.db_without_database) == 0
    error_message = "access.database is true for ${join(", ", local.db_without_database)} but the database is disabled."
  }

  precondition {
    condition     = length(var.compute.services) == length(distinct([for s in var.compute.services : s.name]))
    error_message = "Service names must be unique."
  }
}

output "service_environment" {
  description = "Service name -> non-secret environment variables injected into the service (binding contract)."
  value       = local.service_environment
}

output "secret_environment" {
  description = "Service name -> env var name -> Secrets Manager secret ARN (references only)."
  value       = local.secret_environment
}
