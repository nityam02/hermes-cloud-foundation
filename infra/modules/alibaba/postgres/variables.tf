variable "prefix" {
  description = "Native name prefix: <project>-<environment>."
  type        = string
}

variable "engine_version" {
  description = "PostgreSQL major version (e.g. \"16\"); rendered in Alibaba's \"16.0\" format."
  type        = string
}

variable "size" {
  description = "Portable size: small | medium | large | xlarge."
  type        = string
}

variable "instance_type" {
  description = "Explicit RDS instance class; overrides the size mapping when not empty."
  type        = string
  default     = ""
}

variable "storage_gb" {
  description = "Initial storage (GB). ESSD minimum is 20 GB."
  type        = number
}

variable "max_storage_gb" {
  description = "Storage auto-scaling ceiling (GB). Auto-scaling is off when equal to storage_gb."
  type        = number
}

variable "high_availability" {
  description = "HighAvailability Edition with the secondary node in the second zone."
  type        = bool
}

variable "backup_retention_days" {
  description = "Data backup retention (days); values below 7 are raised to 7 (RDS minimum)."
  type        = number
}

variable "point_in_time_recovery" {
  description = "Enable log backup for point-in-time recovery (HighAvailability Edition only)."
  type        = bool
}

variable "deletion_protection" {
  description = "Enable RDS deletion protection."
  type        = bool
}

variable "protected" {
  description = "Protected environment: retain the latest archived backup after release."
  type        = bool
  default     = false
}

variable "kms_key_id" {
  description = "KMS key ID for cloud-disk encryption, or null for the service-managed default."
  type        = string
  default     = null
}

variable "vpc_id" {
  description = "VPC ID."
  type        = string
}

variable "vpc_cidr" {
  description = "VPC CIDR: the only allow-listed source."
  type        = string
}

variable "vswitch_ids" {
  description = "vSwitch IDs in zone order (the first two are used)."
  type        = list(string)
}

variable "zone_ids" {
  description = "Zone IDs matching vswitch_ids."
  type        = list(string)
}

variable "database_name" {
  description = "Application database name."
  type        = string
  default     = "app"
}

variable "tags" {
  description = "Tags applied to the instance."
  type        = map(string)
  default     = {}
}
