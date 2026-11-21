variable "prefix" {
  description = "Native name prefix: <project>-<environment>."
  type        = string
}

variable "buckets" {
  description = "Spec buckets."
  type = list(object({
    name           = string
    versioning     = bool
    retention_days = number
  }))
  default = []
}

variable "kms_key_id" {
  description = "KMS key ID for SSE-KMS, or null for OSS-managed AES256."
  type        = string
  default     = null
}

variable "protected" {
  description = "Protected environment: zone-redundant storage (ZRS) by default."
  type        = bool
  default     = false
}

variable "redundancy_type" {
  description = "Override: LRS or ZRS (empty = ZRS when protected, else LRS)."
  type        = string
  default     = ""

  validation {
    condition     = contains(["", "LRS", "ZRS"], var.redundancy_type)
    error_message = "The redundancy_type value must be empty, LRS or ZRS."
  }
}

variable "tags" {
  description = "Tags applied to every bucket (OSS allows at most 10 tags per bucket)."
  type        = map(string)
  default     = {}
}
