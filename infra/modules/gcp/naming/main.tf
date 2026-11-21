# -----------------------------------------------------------------------------
# Hermes GCP naming and labelling (pure functions, no providers).
#
# Native names follow data-contracts §2: "<project>-<environment>-<name>". When a
# name would exceed the resource's limit it is truncated and "-" + the first six
# hex characters of sha1(full name) are appended, so truncated names stay unique
# and stable. An optional suffix (a random hex for globally unique or
# non-reusable names) is appended last and always fits inside max_length.
# -----------------------------------------------------------------------------

variable "project" {
  description = "Hermes project name."
  type        = string
}

variable "environment" {
  description = "Hermes environment name."
  type        = string
}

variable "resources" {
  description = "Logical key -> name part (\"\" for the bare <project>-<environment>), native max_length and optional suffix."
  type = map(object({
    name       = string
    max_length = number
    suffix     = optional(string, "")
  }))
  default = {}

  validation {
    # Room for at least one character, "-", the 6-char hash and the optional "-<suffix>".
    condition     = alltrue([for r in values(var.resources) : r.max_length - (r.suffix == "" ? 0 : length(r.suffix) + 1) >= 8])
    error_message = "Each resources entry needs max_length >= 8 plus the length of its suffix and separator."
  }
}

variable "labels" {
  description = "User labels (project.labels); sanitized to GCP label rules."
  type        = map(string)
  default     = {}
}

locals {
  full = {
    for k, r in var.resources : k => r.name == "" ? "${var.project}-${var.environment}" : "${var.project}-${var.environment}-${r.name}"
  }

  # Characters available before the optional "-<suffix>".
  budget = {
    for k, r in var.resources : k => r.suffix == "" ? r.max_length : r.max_length - length(r.suffix) - 1
  }

  base = {
    for k, f in local.full : k => length(f) <= local.budget[k] ? f : format(
      "%s-%s",
      replace(substr(f, 0, local.budget[k] - 7), "/-+$/", ""),
      substr(sha1(f), 0, 6),
    )
  }

  names = {
    for k, b in local.base : k => var.resources[k].suffix == "" ? b : "${b}-${var.resources[k].suffix}"
  }

  # GCP labels: keys start with a lowercase letter; keys and values use [a-z0-9_-] and are <= 63 chars.
  sanitized_user_labels = {
    for k, v in var.labels :
    substr(can(regex("^[a-z]", lower(k))) ? replace(lower(k), "/[^a-z0-9_-]/", "_") : "l_${replace(lower(k), "/[^a-z0-9_-]/", "_")}", 0, 63)
    => substr(replace(lower(v), "/[^a-z0-9_-]/", "_"), 0, 63)
  }

  # Hermes labels win over user labels with the same key.
  base_labels = merge(local.sanitized_user_labels, {
    "hermes-project"     = substr(var.project, 0, 63)
    "hermes-environment" = substr(var.environment, 0, 63)
    "hermes-managed-by"  = "hermes"
  })

  components = ["network", "compute", "database", "storage", "messaging", "secrets", "observability"]
}

output "names" {
  description = "Logical key -> native name."
  value       = local.names
}

output "labels" {
  description = "Base labels (user labels + hermes-project/-environment/-managed-by)."
  value       = local.base_labels
}

output "component_labels" {
  description = "Component -> base labels plus hermes-component."
  value       = { for c in local.components : c => merge(local.base_labels, { "hermes-component" = c }) }
}
