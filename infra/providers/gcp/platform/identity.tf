# -----------------------------------------------------------------------------
# Per-service identity and least-privilege access derived from compute.services[*].access.
# Every grant is on the exact resource (bucket, topic, subscription, secret);
# only Cloud SQL (no resource-level IAM) and the telemetry writer roles are
# project-level, and the Cloud SQL ones carry an IAM condition that limits them
# to this environment's instance.
# Grants whose target does not exist are skipped here and reported by spec_guard.
# -----------------------------------------------------------------------------

resource "google_service_account" "service" {
  for_each = local.services

  account_id   = module.naming.names["sa/${each.key}"]
  display_name = "Hermes ${local.prefix} ${each.key}"
  description  = "Runtime identity of the ${each.key} service (least privilege, managed by Hermes)."

  depends_on = [google_project_service.this, terraform_data.spec_guard]
}

locals {
  members = { for k, sa in google_service_account.service : k => "serviceAccount:${sa.email}" }

  bucket_grants = merge([
    for svc, s in local.services : {
      for b, mode in s.access.buckets : "${svc}/${b}" => {
        service = svc
        bucket  = b
        # read: get/list objects. read-write: also create/overwrite/delete objects.
        role = mode == "read-write" ? "roles/storage.objectUser" : "roles/storage.objectViewer"
      } if contains(keys(local.buckets), b)
    }
  ]...)

  publish_grants = merge([
    for svc, s in local.services : {
      for t in s.access.publish : "${svc}/${t}" => { service = svc, topic = t } if contains(keys(local.topics), t)
    }
  ]...)

  consume_grants = merge([
    for svc, s in local.services : {
      for c in s.access.consume : "${svc}/${c}" => { service = svc, subscription = c } if contains(keys(local.subscriptions), c)
    }
  ]...)

  secret_grants = merge([
    for svc, s in local.services : {
      for n in distinct(values(s.secret_env)) : "${svc}/${n}" => { service = svc, secret = n } if contains(local.secrets, n)
    }
  ]...)

  database_grants = local.database ? merge([
    for svc in keys(local.db_services) : {
      "${svc}/client"       = { service = svc, role = "roles/cloudsql.client" }
      "${svc}/instanceUser" = { service = svc, role = "roles/cloudsql.instanceUser" }
    }
  ]...) : {}

  observability_grants = merge([
    for svc in keys(local.services) : merge(
      local.obs ? { "${svc}/logWriter" = { service = svc, role = "roles/logging.logWriter" } } : {},
      local.metrics ? { "${svc}/metricWriter" = { service = svc, role = "roles/monitoring.metricWriter" } } : {},
      local.tracing ? { "${svc}/traceAgent" = { service = svc, role = "roles/cloudtrace.agent" } } : {},
    )
  ]...)
}

resource "google_storage_bucket_iam_member" "service" {
  for_each = local.bucket_grants

  bucket = module.storage.names[each.value.bucket]
  role   = each.value.role
  member = local.members[each.value.service]
}

resource "google_pubsub_topic_iam_member" "service" {
  for_each = local.publish_grants

  topic  = module.messaging.topic_ids[each.value.topic]
  role   = "roles/pubsub.publisher"
  member = local.members[each.value.service]
}

resource "google_pubsub_subscription_iam_member" "service" {
  for_each = local.consume_grants

  subscription = module.messaging.subscription_ids[each.value.subscription]
  role         = "roles/pubsub.subscriber"
  member       = local.members[each.value.service]
}

resource "google_secret_manager_secret_iam_member" "service" {
  for_each = local.secret_grants

  secret_id = module.secrets.ids[each.value.secret]
  role      = "roles/secretmanager.secretAccessor"
  member    = local.members[each.value.service]
}

resource "google_project_iam_member" "database" {
  for_each = local.database_grants

  project = local.project_id
  role    = each.value.role
  member  = local.members[each.value.service]

  condition {
    title       = "hermes-${local.prefix}-database"
    description = "Only the ${local.prefix} Cloud SQL instance."
    expression  = "resource.type == \"sqladmin.googleapis.com/Instance\" && resource.name == \"projects/${local.project_id}/instances/${module.postgres[0].instance_name}\""
  }
}

resource "google_project_iam_member" "observability" {
  for_each = local.observability_grants

  project = local.project_id
  role    = each.value.role
  member  = local.members[each.value.service]
}
