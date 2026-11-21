# Hermes native naming (data-contracts §2) for Alibaba Cloud.
#
# Each request joins its parts with a delimiter. When the result is longer than the
# resource's limit, it is truncated and suffixed with the first 6 hex characters of
# sha1(full name), so distinct long names stay distinct and stable.
# Pure function: no provider, no resources.

variable "names" {
  description = "Map of logical key -> name request. Parts that are empty strings are dropped."
  type = map(object({
    parts      = list(string)
    max_length = number
    delimiter  = optional(string, "-")
  }))

  validation {
    condition     = alltrue([for n in values(var.names) : n.max_length >= 8])
    error_message = "Each max_length must be at least 8 (room for a 6-character hash and a delimiter)."
  }
}

locals {
  full = { for k, n in var.names : k => join(n.delimiter, compact(n.parts)) }

  names = {
    for k, n in var.names : k => (
      length(local.full[k]) <= n.max_length
      ? local.full[k]
      : format("%s%s%s",
        trimsuffix(substr(local.full[k], 0, n.max_length - length(n.delimiter) - 6), n.delimiter),
        n.delimiter,
        substr(sha1(local.full[k]), 0, 6),
      )
    )
  }
}

output "names" {
  description = "Map of logical key -> native name (within max_length)."
  value       = local.names
}
