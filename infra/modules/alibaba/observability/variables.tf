variable "prefix" {
  description = "Native name prefix: <project>-<environment>."
  type        = string
}

variable "log_retention_days" {
  description = "Logstore retention (days, 1-3650)."
  type        = number

  validation {
    condition     = var.log_retention_days >= 1 && var.log_retention_days <= 3650
    error_message = "The log_retention_days value must be 1-3650."
  }
}

variable "alert_email" {
  description = "E-mail address for alerts (\"\" disables alerting). The address must confirm an activation e-mail."
  type        = string
  default     = ""
}

variable "database_enabled" {
  description = "Create database alarms (known at plan time, unlike the instance ID)."
  type        = bool
  default     = false
}

variable "database_instance_id" {
  description = "RDS instance ID for database alarms."
  type        = string
  default     = ""
}

variable "tags" {
  description = "Tags applied to taggable resources."
  type        = map(string)
  default     = {}
}
