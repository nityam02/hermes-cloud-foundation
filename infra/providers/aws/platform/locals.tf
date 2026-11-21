locals {
  prefix     = "${var.project}-${var.environment}"
  log_prefix = "/hermes/${var.project}/${var.environment}"

  base_tags = merge(var.labels, {
    "hermes-project"     = var.project
    "hermes-environment" = var.environment
    "hermes-managed-by"  = "hermes"
  })
  tags = {
    for c in ["network", "compute", "database", "storage", "messaging", "secrets", "observability"] :
    c => merge(local.base_tags, { "hermes-component" = c })
  }

  # --- extensions.aws (documented in docs/providers/aws.md) -----------------------------------
  extension_keys = try(keys(var.extensions), [])
  ext = {
    acm_certificate_arn      = try(var.extensions.acm_certificate_arn, "")
    alb_ingress_cidrs        = try(tolist(var.extensions.alb_ingress_cidrs), ["0.0.0.0/0"])
    task_egress_cidrs        = try(tolist(var.extensions.task_egress_cidrs), ["0.0.0.0/0"])
    cpu_architecture         = try(var.extensions.cpu_architecture, "X86_64")
    db_instance_class        = try(var.extensions.db_instance_class, "")
    readonly_root_filesystem = try(tobool(var.extensions.readonly_root_filesystem), true)
    container_health_check   = try(tobool(var.extensions.container_health_check), false)
  }

  # --- capabilities ----------------------------------------------------------------------------
  containerized        = var.compute.type == "containerized"
  services             = local.containerized ? { for s in var.compute.services : s.name => s } : {}
  public_service_order = local.containerized ? [for s in var.compute.services : s.name if s.public] : []
  db_enabled           = var.database.enabled
  buckets              = var.storage.enabled ? { for b in var.storage.buckets : b.name => b } : {}
  topics               = var.messaging.enabled ? { for t in var.messaging.topics : t.name => t } : {}
  subscriptions = merge({}, [
    for t in values(local.topics) : { for s in t.subscriptions : s.name => merge(s, { topic = t.name, retention_hours = t.retention_hours }) }
  ]...)
  secret_names = var.secrets.enabled ? toset(var.secrets.names) : toset([])

  obs     = var.observability
  tracing = local.obs.enabled && local.obs.tracing
  metrics = local.obs.enabled && local.obs.metrics
  cmk     = var.security.customer_managed_keys

  kms_key_arn = local.cmk ? module.kms[0].key_arn : null

  # CloudWatch Logs accepts only these retention values: round up to the next one.
  allowed_retention  = [1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653]
  log_retention_days = try(min([for d in local.allowed_retention : d if d >= local.obs.log_retention_days]...), 3653)

  otlp_endpoint = local.tracing ? "http://localhost:4317" : ""

  # --- native names (data-contracts §2) ----------------------------------------------------------
  name_requests = merge(
    {
      network        = { name = "", max_length = 255 }
      cluster        = { name = "", max_length = 255 }
      alb            = { name = "web", max_length = 32 }
      alb_sg         = { name = "alb", max_length = 255 }
      db             = { name = "db", max_length = 63 }
      db_sg          = { name = "db", max_length = 255 }
      alerts         = { name = "hermes-alerts", max_length = 256 }
      flow_logs_role = { name = "vpc-flow-logs", max_length = 64 }
      rds_monitoring = { name = "rds-monitoring", max_length = 64 }
    },
    { for k in keys(local.services) : "svc:${k}" => { name = k, max_length = 255 } },
    { for k in keys(local.services) : "tg:${k}" => { name = k, max_length = 32 } },
    { for k in keys(local.services) : "task_role:${k}" => { name = "${k}-task", max_length = 64 } },
    { for k in keys(local.services) : "exec_role:${k}" => { name = "${k}-exec", max_length = 64 } },
    { for k in keys(local.services) : "task_sg:${k}" => { name = "${k}-tasks", max_length = 255 } },
    { for k in keys(local.buckets) : "bucket:${k}" => { name = k, max_length = 54 } },
    { for k in keys(local.topics) : "topic:${k}" => { name = k, max_length = 256 } },
    { for k in keys(local.subscriptions) : "queue:${k}" => { name = k, max_length = 76 } },
    { for k in local.secret_names : "secret:${k}" => { name = k, max_length = 512 } },
  )
  names = module.naming.names

  # --- per-service access, resolved to exact ARNs ------------------------------------------------
  service_access = {
    for k, s in local.services : k => {
      bucket_read_arns       = [for b, mode in s.access.buckets : module.object_storage.buckets[b].arn if mode == "read" && contains(keys(local.buckets), b)]
      bucket_read_write_arns = [for b, mode in s.access.buckets : module.object_storage.buckets[b].arn if mode == "read-write" && contains(keys(local.buckets), b)]
      topic_arns             = [for t in s.access.publish : module.messaging.topics[t].arn if contains(keys(local.topics), t)]
      queue_arns             = [for c in s.access.consume : module.messaging.subscriptions[c].arn if contains(keys(local.subscriptions), c)]
      queue_names            = [for c in s.access.consume : module.messaging.subscriptions[c].queue_name if contains(keys(local.subscriptions), c)]
      database               = s.access.database && local.db_enabled
    }
  }

  # --- binding contract: computed ONCE, injected into tasks and output as service_environment ----
  service_environment = {
    for k, s in local.services : k => merge(
      s.env,
      {
        HERMES_PROVIDER    = "aws"
        HERMES_REGION      = var.region
        HERMES_PROJECT     = var.project
        HERMES_ENVIRONMENT = var.environment
        HERMES_SERVICE     = k
      },
      { for b in keys(s.access.buckets) : "HERMES_BUCKET_${upper(replace(b, "-", "_"))}" => try(module.object_storage.buckets[b].name, "") },
      { for t in s.access.publish : "HERMES_TOPIC_${upper(replace(t, "-", "_"))}" => try(module.messaging.topics[t].arn, "") },
      { for c in s.access.consume : "HERMES_SUBSCRIPTION_${upper(replace(c, "-", "_"))}" => try(module.messaging.subscriptions[c].url, "") },
      s.access.database && local.db_enabled ? {
        HERMES_DB_HOST = module.postgres[0].address
        HERMES_DB_PORT = tostring(module.postgres[0].port)
        HERMES_DB_NAME = module.postgres[0].db_name
        HERMES_DB_USER = module.postgres[0].app_user
        HERMES_DB_AUTH = module.postgres[0].auth
      } : {},
      local.obs.enabled ? {
        OTEL_SERVICE_NAME           = k
        OTEL_RESOURCE_ATTRIBUTES    = "service.namespace=${var.project},deployment.environment.name=${var.environment},cloud.provider=aws,cloud.platform=aws_ecs,cloud.region=${var.region}"
        OTEL_EXPORTER_OTLP_ENDPOINT = local.otlp_endpoint
        OTEL_EXPORTER_OTLP_PROTOCOL = "grpc"
      } : {},
    )
  }

  secret_environment = {
    for k, s in local.services : k => { for env_name, secret in s.secret_env : env_name => try(module.secrets.arns[secret], "") }
  }

  # --- spec reference checks (defence in depth; the hermes CLI validates these first) ------------
  missing_buckets       = flatten([for k, s in local.services : [for b in keys(s.access.buckets) : "${k}->${b}" if !contains(keys(local.buckets), b)]])
  missing_topics        = flatten([for k, s in local.services : [for t in s.access.publish : "${k}->${t}" if !contains(keys(local.topics), t)]])
  missing_subscriptions = flatten([for k, s in local.services : [for c in s.access.consume : "${k}->${c}" if !contains(keys(local.subscriptions), c)]])
  missing_secrets       = flatten([for k, s in local.services : [for e, n in s.secret_env : "${k}:${e}->${n}" if !contains(local.secret_names, n)]])
  db_without_database   = [for k, s in local.services : k if s.access.database && !local.db_enabled]
}
