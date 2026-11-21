variable "identifier" {
  description = "RDS instance identifier (at most 63 characters, starts with a letter)."
  type        = string
}

variable "engine_version" {
  description = "PostgreSQL major version, e.g. \"16\" (RDS picks the latest minor; auto minor upgrades are on)."
  type        = string
}

variable "size" {
  description = "Portable size: small | medium | large | xlarge."
  type        = string
  default     = "small"

  validation {
    condition     = contains(["small", "medium", "large", "xlarge"], var.size)
    error_message = "size must be small, medium, large or xlarge."
  }
}

variable "instance_class_override" {
  description = "Explicit DB instance class (extensions.aws.db_instance_class); \"\" uses the size map."
  type        = string
  default     = ""
}

variable "storage_gb" {
  description = "Initial allocated storage (GiB)."
  type        = number
  default     = 20
}

variable "max_storage_gb" {
  description = "Storage autoscaling ceiling (GiB); equal to storage_gb disables autoscaling."
  type        = number
  default     = 100
}

variable "high_availability" {
  description = "Multi-AZ (synchronous standby in another zone)."
  type        = bool
  default     = false
}

variable "backup_retention_days" {
  description = "Automated backup retention (1-35). Point-in-time recovery is inherent to RDS automated backups."
  type        = number
  default     = 7
}

variable "deletion_protection" {
  description = "Enable RDS deletion protection."
  type        = bool
  default     = true
}

variable "protected" {
  description = "Protected environment: final snapshot on delete, keep automated backups, changes wait for the maintenance window."
  type        = bool
  default     = false
}

variable "extensions" {
  description = "Requested PostgreSQL extensions (validated against an allowlist; created with CREATE EXTENSION after provisioning)."
  type        = list(string)
  default     = []

  validation {
    condition = alltrue([for e in var.extensions : contains([
      "postgis", "postgis_raster", "postgis_topology", "postgis_tiger_geocoder", "address_standardizer",
      "fuzzystrmatch", "pgrouting", "pg_stat_statements", "pgcrypto", "uuid-ossp", "hstore", "citext",
      "pg_trgm", "btree_gin", "btree_gist", "vector", "pg_partman", "tablefunc", "intarray", "ltree",
    ], e)])
    error_message = "Unsupported PostgreSQL extension. Allowed on AWS RDS by Hermes: postgis, postgis_raster, postgis_topology, postgis_tiger_geocoder, address_standardizer, fuzzystrmatch, pgrouting, pg_stat_statements, pgcrypto, uuid-ossp, hstore, citext, pg_trgm, btree_gin, btree_gist, vector, pg_partman, tablefunc, intarray, ltree."
  }
}

variable "kms_key_arn" {
  description = "CMK for storage, Performance Insights, the master-user secret and log groups; null uses AWS-managed keys."
  type        = string
  default     = null
}

variable "vpc_id" {
  description = "VPC id."
  type        = string
}

variable "subnet_ids" {
  description = "Private subnet ids for the DB subnet group."
  type        = list(string)
}

variable "security_group_name" {
  description = "Name of the database security group."
  type        = string
}

variable "allowed_security_group_ids" {
  description = "Spec service name -> task security group id allowed to connect on 5432."
  type        = map(string)
  default     = {}
}

variable "log_retention_days" {
  description = "Retention of the exported PostgreSQL logs."
  type        = number
  default     = 30
}

variable "enhanced_monitoring" {
  description = "Enable Enhanced Monitoring (60 s)."
  type        = bool
  default     = false
}

variable "monitoring_role_name" {
  description = "IAM role name for Enhanced Monitoring."
  type        = string
  default     = ""
}

variable "alarms_enabled" {
  description = "Create CPU and free-storage alarms."
  type        = bool
  default     = false
}

variable "alarm_topic_arn" {
  description = "SNS topic for alarm notifications."
  type        = string
  default     = null
}

variable "account_id" {
  description = "AWS account id."
  type        = string
}

variable "partition" {
  description = "AWS partition."
  type        = string
  default     = "aws"
}

variable "region" {
  description = "AWS region."
  type        = string
}

variable "tags" {
  description = "Tags."
  type        = map(string)
  default     = {}
}
