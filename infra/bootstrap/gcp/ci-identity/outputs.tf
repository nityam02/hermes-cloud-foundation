output "workload_identity_provider" {
  description = "Full provider resource name for google-github-actions/auth (repository variable HERMES_GCP_WORKLOAD_IDENTITY_PROVIDER)."
  value       = google_iam_workload_identity_pool_provider.github.name
}

output "plan_service_account" {
  description = "Plan service account email (repository variable HERMES_GCP_PLAN_SERVICE_ACCOUNT)."
  value       = google_service_account.plan.email
}

output "apply_service_accounts" {
  description = "Environment -> apply service account email (GitHub environment variable HERMES_GCP_APPLY_SERVICE_ACCOUNT)."
  value       = { for e, sa in google_service_account.apply : e => sa.email }
}

output "trusted_subjects" {
  description = "Exact OIDC subjects each service account trusts (for review)."
  value = merge(
    { plan = local.plan_subjects },
    { for e in var.environments : "apply-${e}" => ["${local.subject}:environment:${e}"] },
  )
}
