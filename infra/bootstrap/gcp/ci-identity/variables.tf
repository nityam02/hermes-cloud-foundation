variable "project" {
  description = "Hermes project name; names the CI service accounts."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,28}[a-z0-9]$", var.project))
    error_message = "The project must match ^[a-z][a-z0-9-]{1,28}[a-z0-9]$."
  }
}

variable "gcp_project_id" {
  description = "GCP project the Hermes composition deploys into (receives the plan/apply role bindings)."
  type        = string
}

variable "ci_project_id" {
  description = "GCP project that holds the workload identity pool and the CI service accounts. Default: gcp_project_id. A separate project is recommended: project-level actAs grants in the workload project then cannot reach the CI identities."
  type        = string
  default     = ""
}

variable "github_repository" {
  description = "GitHub repository allowed to federate, as owner/name (exact)."
  type        = string

  validation {
    condition     = can(regex("^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$", var.github_repository))
    error_message = "The github_repository must be owner/name without wildcards."
  }
}

variable "github_repository_id" {
  description = "Numeric GitHub repository id. When set, the provider also pins assertion.repository_id, which survives renames and defeats a re-created repository with the same name."
  type        = string
  default     = ""

  validation {
    condition     = var.github_repository_id == "" || can(regex("^[0-9]+$", var.github_repository_id))
    error_message = "The github_repository_id must be numeric."
  }
}

variable "environments" {
  description = "Hermes environments that get an apply service account (each a GitHub environment with required reviewers)."
  type        = list(string)

  validation {
    condition     = length(var.environments) > 0 && alltrue([for e in var.environments : can(regex("^[a-z][a-z0-9]{1,11}$", e))])
    error_message = "The environments list must be non-empty and each name must match ^[a-z][a-z0-9]{1,11}$."
  }
}

variable "plan_branches" {
  description = "Branches whose runs may impersonate the plan service account (exact names)."
  type        = list(string)
  default     = ["main"]

  validation {
    condition     = alltrue([for b in var.plan_branches : !can(regex("[*?]", b))])
    error_message = "The plan_branches entries must be exact branch names (no wildcards)."
  }
}

variable "plan_on_pull_request" {
  description = "Let pull_request runs impersonate the plan service account (GitHub issues no OIDC token to fork pull requests)."
  type        = bool
  default     = true
}

variable "pool_id" {
  description = "Workload identity pool id."
  type        = string
  default     = "hermes-github"
}

variable "state_bucket" {
  description = "GCS bucket that holds Hermes state (from infra/bootstrap/gcp/state-backend; uniform bucket-level access is required for conditional bindings)."
  type        = string
}

variable "state_prefix" {
  description = "State prefix (state.prefix in hermes.yaml). Objects live under <prefix>/<project>/<environment>/gcp/."
  type        = string
  default     = "hermes"
}

variable "plan_roles" {
  description = "Project roles for the plan service account (refresh needs read access to everything the composition manages)."
  type        = list(string)
  default     = ["roles/viewer"]
}

variable "apply_roles" {
  description = "Project roles for each apply service account. Inherently broad: this is the deployer of the whole composition."
  type        = list(string)
  default = [
    "roles/serviceusage.serviceUsageAdmin",
    "roles/compute.networkAdmin",
    "roles/compute.securityAdmin",
    "roles/servicenetworking.networksAdmin",
    "roles/cloudsql.admin",
    "roles/run.admin",
    "roles/iam.serviceAccountAdmin",
    "roles/iam.serviceAccountUser",
    "roles/storage.admin",
    "roles/pubsub.admin",
    "roles/secretmanager.admin",
    "roles/cloudkms.admin",
    "roles/logging.configWriter",
    "roles/monitoring.alertPolicyEditor",
    "roles/monitoring.notificationChannelEditor",
  ]
}

variable "grantable_project_roles" {
  description = "The only project-level roles the apply service accounts may grant (Project IAM Admin is conditioned on this list)."
  type        = list(string)
  default = [
    "roles/cloudsql.client",
    "roles/cloudsql.instanceUser",
    "roles/logging.logWriter",
    "roles/monitoring.metricWriter",
    "roles/cloudtrace.agent",
  ]

  validation {
    condition     = length(setintersection(var.grantable_project_roles, ["roles/owner", "roles/editor", "roles/resourcemanager.projectIamAdmin", "roles/iam.securityAdmin", "roles/iam.serviceAccountTokenCreator", "roles/iam.workloadIdentityUser"])) == 0
    error_message = "The grantable_project_roles list must not contain basic or IAM-administration roles."
  }
}
