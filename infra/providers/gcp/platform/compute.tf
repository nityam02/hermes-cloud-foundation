# -----------------------------------------------------------------------------
# Application binding environment (data-contracts §2), computed ONCE: injected
# into Cloud Run and exported unchanged as the service_environment output.
# -----------------------------------------------------------------------------

locals {
  service_environment = {
    for name, s in local.services : name => merge(
      s.env,
      {
        HERMES_PROVIDER    = "gcp"
        HERMES_REGION      = var.region
        HERMES_PROJECT     = var.project
        HERMES_ENVIRONMENT = var.environment
        HERMES_SERVICE     = name
      },
      { for b in keys(s.access.buckets) : "HERMES_BUCKET_${upper(replace(b, "-", "_"))}" => lookup(module.storage.names, b, "") },
      { for t in s.access.publish : "HERMES_TOPIC_${upper(replace(t, "-", "_"))}" => lookup(module.messaging.topic_ids, t, "") },
      { for c in s.access.consume : "HERMES_SUBSCRIPTION_${upper(replace(c, "-", "_"))}" => lookup(module.messaging.subscription_ids, c, "") },
      s.access.database && local.database ? {
        HERMES_DB_HOST = module.postgres[0].private_ip
        HERMES_DB_PORT = "5432"
        HERMES_DB_NAME = module.postgres[0].database_name
        HERMES_DB_USER = module.postgres[0].iam_users[name]
        HERMES_DB_AUTH = "iam"
      } : {},
      local.obs ? {
        OTEL_SERVICE_NAME           = name
        OTEL_RESOURCE_ATTRIBUTES    = "service.namespace=${var.project},deployment.environment.name=${var.environment},cloud.provider=gcp,cloud.platform=gcp_cloud_run,cloud.region=${var.region}"
        OTEL_EXPORTER_OTLP_ENDPOINT = local.otlp_endpoint
        OTEL_EXPORTER_OTLP_PROTOCOL = "grpc"
      } : {},
    )
  }

  # Env var -> full secret resource name (output); Cloud Run gets the short id.
  secret_environment = {
    for name, s in local.services : name => {
      for env_name, secret in s.secret_env : env_name => lookup(module.secrets.ids, secret, "")
    }
  }
}

module "service" {
  source   = "../../../modules/gcp/container-service"
  for_each = local.services

  name                  = module.naming.names["run/${each.key}"]
  region                = var.region
  service_account_email = google_service_account.service[each.key].email
  image                 = each.value.image
  port                  = each.value.port
  cpu                   = each.value.cpu
  memory_mb             = each.value.memory_mb
  min_instances         = each.value.min_instances
  max_instances         = each.value.max_instances
  public                = each.value.public
  health_check_path     = each.value.health_check_path
  command               = each.value.command
  env                   = local.service_environment[each.key]
  secret_env            = { for env_name, secret in each.value.secret_env : env_name => module.secrets.secret_ids[secret] if contains(local.secrets, secret) }
  network               = module.network.network_name
  subnetwork            = module.network.subnet_name
  vpc_egress            = local.cloud_run_vpc_egress
  deletion_protection   = var.protected
  labels                = module.naming.component_labels["compute"]

  otel_collector = {
    enabled = local.tracing
    image   = local.ext_otel_image
    metrics = local.metrics
  }

  # Cloud Run verifies secret access when a revision is created, and the app needs
  # its grants and database user at start-up.
  depends_on = [
    google_project_service.this,
    google_secret_manager_secret_iam_member.service,
    google_storage_bucket_iam_member.service,
    google_pubsub_topic_iam_member.service,
    google_pubsub_subscription_iam_member.service,
    google_project_iam_member.database,
    google_project_iam_member.observability,
    module.postgres,
  ]
}
