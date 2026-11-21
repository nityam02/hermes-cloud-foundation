variable "project" {
  description = "Hermes project name. Names the CI roles and scopes deploy permissions to resources named <project>-*."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,28}[a-z0-9]$", var.project))
    error_message = "The project must match ^[a-z][a-z0-9-]{1,28}[a-z0-9]$."
  }
}

variable "github_repository" {
  description = "GitHub repository allowed to federate, as owner/name (exact)."
  type        = string

  validation {
    condition     = can(regex("^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$", var.github_repository))
    error_message = "The github_repository must be owner/name without wildcards."
  }
}

variable "environments" {
  description = "Hermes environments that get an apply role (each a GitHub environment with required reviewers)."
  type        = list(string)

  validation {
    condition     = length(var.environments) > 0 && alltrue([for e in var.environments : can(regex("^[a-z][a-z0-9]{1,11}$", e))])
    error_message = "The environments list must be non-empty and each name must match ^[a-z][a-z0-9]{1,11}$."
  }
}

variable "plan_branches" {
  description = "Branches whose runs may assume the plan role (exact names)."
  type        = list(string)
  default     = ["main"]

  validation {
    condition     = alltrue([for b in var.plan_branches : !can(regex("[*?]", b))])
    error_message = "The plan_branches entries must be exact branch names (no wildcards)."
  }
}

variable "plan_on_pull_request" {
  description = "Let pull_request runs assume the plan role (GitHub issues no OIDC token to fork pull requests)."
  type        = bool
  default     = true
}

variable "create_oidc_provider" {
  description = "Create the RAM OIDC provider. Set false to reuse one and pass oidc_provider_arn."
  type        = bool
  default     = true
}

variable "oidc_provider_name" {
  description = "Name of the RAM OIDC provider to create."
  type        = string
  default     = "hermes-github"
}

variable "oidc_provider_arn" {
  description = "ARN of an existing RAM OIDC provider for https://token.actions.githubusercontent.com (when create_oidc_provider = false)."
  type        = string
  default     = ""
}

variable "oidc_fingerprints" {
  description = "SHA-1 fingerprints of the issuer's TLS certificate chain, required by RAM. Obtain and verify them yourself (docs/security/ci-cd-authentication.md); they change when GitHub rotates certificates."
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for f in var.oidc_fingerprints : can(regex("^[0-9a-fA-F]{40}$", f))])
    error_message = "Each fingerprint must be 40 hexadecimal characters."
  }
}

variable "state_bucket" {
  description = "OSS bucket that holds Hermes state (from infra/bootstrap/alibaba/state-backend)."
  type        = string
}

variable "state_prefix" {
  description = "State prefix (state.prefix in hermes.yaml). Objects live under <prefix>/<project>/<environment>/alibaba/."
  type        = string
  default     = "hermes"
}

variable "lock_tablestore_instance" {
  description = "Tablestore instance of the state lock table (empty: no locking permissions are granted)."
  type        = string
  default     = ""
}

variable "lock_table" {
  description = "Tablestore table used for state locking."
  type        = string
  default     = "hermes_state_lock"
}

variable "tags" {
  description = "Tags for the RAM roles and policies."
  type        = map(string)
  default     = {}
}
