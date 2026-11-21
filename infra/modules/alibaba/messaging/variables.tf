variable "prefix" {
  description = "Native name prefix: <project>-<environment>."
  type        = string
}

variable "topics" {
  description = "Spec topics with their subscriptions (subscription names are unique across topics)."
  type = list(object({
    name            = string
    retention_hours = number
    subscriptions = list(object({
      name                  = string
      ack_deadline_seconds  = number
      max_delivery_attempts = number
    }))
  }))
  default = []

  validation {
    condition     = alltrue([for t in var.topics : t.retention_hours >= 1 && t.retention_hours <= 168])
    error_message = "Queue retention must be 1-168 hours (Simple Message Queue keeps messages for at most 7 days)."
  }
}

variable "sse_enabled" {
  description = "Enable server-side encryption on topics and queues (requires account enablement by ticket)."
  type        = bool
  default     = false
}

variable "kms_key_id" {
  description = "KMS key ID for SSE with a customer-managed key, or null."
  type        = string
  default     = null
}

variable "tags" {
  description = "Tags applied to topics and queues."
  type        = map(string)
  default     = {}
}
