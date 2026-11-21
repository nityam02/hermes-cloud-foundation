variable "project" {
  description = "Hermes project name. Names the CI roles and scopes the deploy permissions to resources named <project>-*."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,28}[a-z0-9]$", var.project))
    error_message = "The project must match ^[a-z][a-z0-9-]{1,28}[a-z0-9]$."
  }
}

variable "github_repository" {
  description = "GitHub repository allowed to federate, as owner/name (exact; no wildcards)."
  type        = string

  validation {
    condition     = can(regex("^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$", var.github_repository))
    error_message = "The github_repository must be owner/name without wildcards."
  }
}

variable "environments" {
  description = "Hermes environments that get an apply role. Each must exist as a GitHub environment with required reviewers."
  type        = list(string)

  validation {
    condition     = length(var.environments) > 0 && alltrue([for e in var.environments : can(regex("^[a-z][a-z0-9]{1,11}$", e))])
    error_message = "The environments list must be non-empty and each name must match ^[a-z][a-z0-9]{1,11}$."
  }
}

variable "plan_branches" {
  description = "Branches whose push/workflow_dispatch runs may assume the plan role (exact names)."
  type        = list(string)
  default     = ["main"]

  validation {
    condition     = alltrue([for b in var.plan_branches : !can(regex("[*?]", b))])
    error_message = "The plan_branches entries must be exact branch names (no wildcards)."
  }
}

variable "plan_on_pull_request" {
  description = "Let pull_request runs assume the plan role. GitHub does not issue OIDC tokens to pull requests from forks."
  type        = bool
  default     = true
}

variable "create_oidc_provider" {
  description = "Create the account's GitHub OIDC provider. Set false when it already exists (one per account) and pass oidc_provider_arn."
  type        = bool
  default     = true
}

variable "oidc_provider_arn" {
  description = "ARN of an existing token.actions.githubusercontent.com OIDC provider (when create_oidc_provider = false)."
  type        = string
  default     = ""
}

variable "state_bucket" {
  description = "S3 bucket that holds Hermes state (from infra/bootstrap/aws/state-backend)."
  type        = string
}

variable "state_prefix" {
  description = "State key prefix (state.prefix in hermes.yaml). Keys are <prefix>/<project>/<environment>/aws/terraform.tfstate."
  type        = string
  default     = "hermes"
}

variable "state_kms_key_arn" {
  description = "KMS key ARN that encrypts the state bucket, or empty for SSE-S3."
  type        = string
  default     = ""
}

variable "state_lock_table" {
  description = "Legacy DynamoDB lock table name, or empty when the S3 native lockfile is used (default)."
  type        = string
  default     = ""
}

variable "plan_policy_arns" {
  description = "Managed policies for the plan role (refresh needs broad read access). Data reads are denied separately."
  type        = list(string)
  default     = ["arn:aws:iam::aws:policy/ReadOnlyAccess"]
}

variable "apply_policy_arns" {
  description = "Additional managed policies for the apply roles (default none; the deploy policy is defined in this module)."
  type        = list(string)
  default     = []
}

variable "workload_permissions_boundary_arn" {
  description = "When set, apply roles may only create or update IAM roles that carry this permissions boundary."
  type        = string
  default     = ""
}

variable "tags" {
  description = "Tags for the IAM resources."
  type        = map(string)
  default     = {}
}
