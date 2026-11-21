# Hermes native naming (data-contracts §2): "<project>-<environment>-<name>".
# When a name exceeds the provider limit it is truncated and suffixed with
# "-" + the first 6 hex characters of sha1(full name), so it stays unique and stable.
# Pure module: no providers, no resources.

variable "project" {
  description = "Project name."
  type        = string
}

variable "environment" {
  description = "Environment name."
  type        = string
}

variable "names" {
  description = "Logical key -> { name = suffix after \"<project>-<environment>-\" (\"\" for the bare prefix), max_length = provider limit }."
  type = map(object({
    name       = string
    max_length = number
  }))
  default = {}

  validation {
    condition     = alltrue([for v in values(var.names) : v.max_length >= 16])
    error_message = "Each max_length must be at least 16 so a truncated name keeps a meaningful prefix plus the 7-character hash suffix."
  }
}

locals {
  prefix = "${var.project}-${var.environment}"

  # Collapse repeated hyphens: several AWS name formats (RDS identifiers, parameter groups) reject "--".
  full = {
    for k, v in var.names : k => replace(v.name == "" ? local.prefix : "${local.prefix}-${v.name}", "/-{2,}/", "-")
  }

  names = {
    for k, v in var.names : k => (
      length(local.full[k]) <= v.max_length
      ? local.full[k]
      : format("%s-%s", replace(substr(local.full[k], 0, v.max_length - 7), "/-+$/", ""), substr(sha1(local.full[k]), 0, 6))
    )
  }
}

output "prefix" {
  description = "\"<project>-<environment>\"."
  value       = local.prefix
}

output "names" {
  description = "Logical key -> native name (within its max_length)."
  value       = local.names
}
