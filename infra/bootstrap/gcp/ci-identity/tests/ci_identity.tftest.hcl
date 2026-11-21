# Mock-provider tests: they prove wiring and binding contents, NOT real GCP behaviour.
mock_provider "google" {
  mock_resource "google_iam_workload_identity_pool" {
    defaults = { name = "projects/123456789012/locations/global/workloadIdentityPools/hermes-github" }
  }
  mock_resource "google_iam_workload_identity_pool_provider" {
    defaults = { name = "projects/123456789012/locations/global/workloadIdentityPools/hermes-github/providers/github" }
  }
  mock_resource "google_service_account" {
    defaults = { email = "ci@sample-project.iam.gserviceaccount.com", name = "projects/sample-project/serviceAccounts/ci@sample-project.iam.gserviceaccount.com" }
  }
}

variables {
  project           = "sample-logistics-platform"
  gcp_project_id    = "sample-project"
  github_repository = "example-org/example-repo"
  environments      = ["staging", "production"]
  state_bucket      = "example-hermes-state"
}

run "provider_pins_repository" {
  command = apply

  assert {
    condition     = google_iam_workload_identity_pool_provider.github.attribute_condition == "assertion.repository == \"example-org/example-repo\""
    error_message = "The provider must pin the repository."
  }

  assert {
    condition     = google_iam_workload_identity_pool_provider.github.oidc[0].issuer_uri == "https://token.actions.githubusercontent.com"
    error_message = "The provider must trust the GitHub Actions issuer."
  }
}

run "impersonation_bound_to_exact_subjects" {
  command = apply

  assert {
    condition = sort([for m in google_service_account_iam_member.plan : m.member]) == tolist([
      "principal://iam.googleapis.com/projects/123456789012/locations/global/workloadIdentityPools/hermes-github/subject/repo:example-org/example-repo:pull_request",
      "principal://iam.googleapis.com/projects/123456789012/locations/global/workloadIdentityPools/hermes-github/subject/repo:example-org/example-repo:ref:refs/heads/main",
    ])
    error_message = "The plan service account must be impersonable only by the pull_request and main subjects."
  }

  assert {
    condition     = google_service_account_iam_member.apply["production"].member == "principal://iam.googleapis.com/projects/123456789012/locations/global/workloadIdentityPools/hermes-github/subject/repo:example-org/example-repo:environment:production"
    error_message = "Each apply service account must be impersonable only by its environment subject."
  }

  assert {
    condition     = alltrue([for m in concat(values(google_service_account_iam_member.plan), values(google_service_account_iam_member.apply)) : !strcontains(m.member, "principalSet") && !strcontains(m.member, "*") && m.role == "roles/iam.workloadIdentityUser"])
    error_message = "No principalSet or wildcard bindings."
  }
}

run "roles_and_state_access" {
  command = apply

  assert {
    condition     = keys(google_project_iam_member.plan) == ["roles/viewer"]
    error_message = "The plan service account gets viewer only (plus state access)."
  }

  assert {
    condition     = length(google_project_iam_member.apply) == 30 && !contains([for b in google_project_iam_member.apply : b.role], "roles/owner") && !contains([for b in google_project_iam_member.apply : b.role], "roles/editor")
    error_message = "Apply roles are the explicit list, never basic roles."
  }

  assert {
    condition     = google_project_iam_member.apply_grants["production"].condition[0].expression == "api.getAttribute('iam.googleapis.com/modifiedGrantsByRole', []).hasOnly(['roles/cloudsql.client', 'roles/cloudsql.instanceUser', 'roles/logging.logWriter', 'roles/monitoring.metricWriter', 'roles/cloudtrace.agent'])"
    error_message = "Project IAM Admin must be conditioned to the grantable roles."
  }

  assert {
    condition     = strcontains(google_storage_bucket_iam_member.plan_state_lock.condition[0].expression, "endsWith(\".tflock\")")
    error_message = "The plan service account may only write lock files."
  }

  assert {
    condition     = strcontains(google_storage_bucket_iam_member.apply_state["staging"].condition[0].expression, "objects/hermes/sample-logistics-platform/staging/") && !strcontains(google_storage_bucket_iam_member.apply_state["staging"].condition[0].expression, "production")
    error_message = "The staging apply service account may only touch staging state."
  }

  assert {
    condition     = alltrue([for sa in concat([google_service_account.plan], values(google_service_account.apply)) : length(sa.account_id) >= 6 && length(sa.account_id) <= 30])
    error_message = "Service account ids must be 6-30 characters even for long project names."
  }
}

run "repository_id_pinning" {
  command = apply

  variables {
    github_repository_id = "123456789"
    ci_project_id        = "sample-ci-project"
  }

  assert {
    condition     = google_iam_workload_identity_pool_provider.github.attribute_condition == "assertion.repository == \"example-org/example-repo\" && assertion.repository_id == \"123456789\""
    error_message = "The numeric repository id is pinned when given."
  }

  assert {
    condition     = google_service_account.plan.project == "sample-ci-project" && google_project_iam_member.plan["roles/viewer"].project == "sample-project"
    error_message = "CI identities live in the CI project; role bindings go to the workload project."
  }
}

run "rejects_dangerous_grantable_roles" {
  command = plan

  variables {
    grantable_project_roles = ["roles/owner"]
  }

  expect_failures = [var.grantable_project_roles]
}

run "rejects_wildcard_repository" {
  command = plan

  variables {
    github_repository = "example-org/*"
  }

  expect_failures = [var.github_repository]
}
