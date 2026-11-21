# GitHub Actions -> Google Cloud federation for Hermes CI (Workload Identity Federation, no keys).
#
# Creates a workload identity pool + OIDC provider for token.actions.githubusercontent.com whose
# attribute_condition pins the repository (and optionally its numeric id), a plan service
# account and one apply service account per environment. Each service account can be
# impersonated only by exact subjects bound as principal://.../subject/<sub>:
#   plan:          repo:<repo>:pull_request, repo:<repo>:ref:refs/heads/<branch>
#   apply (<env>): repo:<repo>:environment:<env>  (GitHub environment protection = human approval)
#
# The apply permission set is BROAD by necessity (it deploys networks, Cloud SQL, Cloud Run,
# IAM service accounts, KMS keys, buckets, topics and secrets). Guardrails: Project IAM Admin is
# conditioned so it can only grant the roles in var.grantable_project_roles; state access is
# limited to the environment's prefix; no keys are ever created. roles/iam.serviceAccountUser and
# roles/secretmanager.admin are project-wide: put CI identities in a separate project and use one
# GCP project per environment (see docs/security/iam-strategy.md).
#
# Status: implemented, mock-tested (tests/ci_identity.tftest.hcl); NOT tested against real GCP.

locals {
  ci_project = var.ci_project_id == "" ? var.gcp_project_id : var.ci_project_id
  issuer     = "https://token.actions.githubusercontent.com"
  subject    = "repo:${var.github_repository}"

  plan_subjects = concat(
    var.plan_on_pull_request ? ["${local.subject}:pull_request"] : [],
    [for b in var.plan_branches : "${local.subject}:ref:refs/heads/${b}"],
  )

  # Service account ids are 6-30 characters: keep a short, stable prefix.
  sa_prefix = length(var.project) <= 14 ? var.project : "${substr(var.project, 0, 7)}-${substr(sha1(var.project), 0, 6)}"

  attribute_condition = join(" && ", compact([
    "assertion.repository == \"${var.github_repository}\"",
    var.github_repository_id == "" ? "" : "assertion.repository_id == \"${var.github_repository_id}\"",
  ]))

  state_bucket_name = "projects/_/buckets/${var.state_bucket}"
  state_objects     = "${local.state_bucket_name}/objects/${var.state_prefix}/${var.project}/"

  apply_bindings = { for pair in setproduct(var.environments, var.apply_roles) : "${pair[0]}|${pair[1]}" => { env = pair[0], role = pair[1] } }

  grantable_list = join(", ", [for r in var.grantable_project_roles : "'${r}'"])
}

resource "google_iam_workload_identity_pool" "github" {
  project                   = local.ci_project
  workload_identity_pool_id = var.pool_id
  display_name              = "Hermes GitHub Actions"
  description               = "GitHub Actions OIDC for ${var.github_repository}"
}

resource "google_iam_workload_identity_pool_provider" "github" {
  #checkov:skip=CKV_GCP_125:attribute_condition pins assertion.repository (and repository_id when given) but is built in locals, which checkov cannot resolve; asserted by tests/ci_identity.tftest.hcl.
  project                            = local.ci_project
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "github"
  display_name                       = "GitHub Actions"
  attribute_condition                = local.attribute_condition

  attribute_mapping = {
    "google.subject"                = "assertion.sub"
    "attribute.repository"          = "assertion.repository"
    "attribute.repository_id"       = "assertion.repository_id"
    "attribute.ref"                 = "assertion.ref"
    "attribute.environment"         = "assertion.environment"
    "attribute.event_name"          = "assertion.event_name"
    "attribute.workflow_ref"        = "assertion.job_workflow_ref"
    "attribute.repository_owner_id" = "assertion.repository_owner_id"
  }

  oidc {
    issuer_uri = local.issuer
  }
}

resource "google_service_account" "plan" {
  project      = local.ci_project
  account_id   = "${local.sa_prefix}-ci-plan"
  display_name = "Hermes CI plan (${var.project})"
  description  = "Read-only plan identity for ${var.github_repository}; state read + lock files only."
}

resource "google_service_account" "apply" {
  for_each = toset(var.environments)

  project      = local.ci_project
  account_id   = "${local.sa_prefix}-ap-${each.key}"
  display_name = "Hermes CI apply ${each.key} (${var.project})"
  description  = "Deploy identity for ${var.github_repository}, GitHub environment ${each.key}."
}

resource "google_service_account_iam_member" "plan" {
  for_each = toset(local.plan_subjects)

  service_account_id = google_service_account.plan.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principal://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/subject/${each.key}"
}

resource "google_service_account_iam_member" "apply" {
  for_each = toset(var.environments)

  service_account_id = google_service_account.apply[each.key].name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principal://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/subject/${local.subject}:environment:${each.key}"
}

resource "google_project_iam_member" "plan" {
  #checkov:skip=CKV_GCP_117:roles/viewer is the default read role refresh needs across every managed service (GCP composition requirement); override plan_roles with service viewer roles to avoid project viewer access to default bucket bindings.
  for_each = toset(var.plan_roles)

  project = var.gcp_project_id
  role    = each.key
  member  = "serviceAccount:${google_service_account.plan.email}"
}

resource "google_project_iam_member" "apply" {
  for_each = local.apply_bindings

  project = var.gcp_project_id
  role    = each.value.role
  member  = "serviceAccount:${google_service_account.apply[each.value.env].email}"
}

# Project IAM Admin, but only for the roles the composition grants (delegated role grants).
resource "google_project_iam_member" "apply_grants" {
  for_each = toset(var.environments)

  project = var.gcp_project_id
  role    = "roles/resourcemanager.projectIamAdmin"
  member  = "serviceAccount:${google_service_account.apply[each.key].email}"

  condition {
    title       = "hermes-grantable-roles-only"
    description = "May only add or remove the project roles the Hermes composition uses."
    expression  = "api.getAttribute('iam.googleapis.com/modifiedGrantsByRole', []).hasOnly([${local.grantable_list}])"
  }
}

# State: plan reads the project's state and writes only lock files; apply reads/writes its environment.
resource "google_storage_bucket_iam_member" "plan_state_read" {
  bucket = var.state_bucket
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.plan.email}"

  condition {
    title      = "hermes-state-read"
    expression = "resource.name == \"${local.state_bucket_name}\" || resource.name.startsWith(\"${local.state_objects}\")"
  }
}

resource "google_storage_bucket_iam_member" "plan_state_lock" {
  bucket = var.state_bucket
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${google_service_account.plan.email}"

  condition {
    title      = "hermes-state-lock-files"
    expression = "resource.name.startsWith(\"${local.state_objects}\") && resource.name.endsWith(\".tflock\")"
  }
}

resource "google_storage_bucket_iam_member" "apply_state" {
  for_each = toset(var.environments)

  bucket = var.state_bucket
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${google_service_account.apply[each.key].email}"

  condition {
    title      = "hermes-state-${each.key}"
    expression = "resource.name == \"${local.state_bucket_name}\" || resource.name.startsWith(\"${local.state_objects}${each.key}/\")"
  }
}
