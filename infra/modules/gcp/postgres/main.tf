# -----------------------------------------------------------------------------
# Cloud SQL for PostgreSQL (Enterprise edition, custom tiers).
#   - private IP only (no public IPv4), TLS required (ssl_mode ENCRYPTED_ONLY)
#   - IAM database authentication: one CLOUD_IAM_SERVICE_ACCOUNT user per service
#     with access.database; no passwords are created, so none reach state
#   - REGIONAL (primary + standby in two zones) when high availability, else ZONAL
#   - automated backups, point-in-time recovery, storage auto-resize up to a cap
#   - deletion protection twice: the Terraform argument and the Cloud SQL API flag
#   - CMEK when enabled (Cloud SQL service agent must already hold the key grant)
# Extensions (e.g. PostGIS) are enabled with CREATE EXTENSION after provisioning;
# Cloud SQL ships them, Terraform cannot run SQL.
# -----------------------------------------------------------------------------

variable "name" {
  description = "Native instance name (unique, never reused: Cloud SQL blocks reuse for up to a week)."
  type        = string
}

variable "region" {
  description = "Instance region."
  type        = string
}

variable "database_version" {
  description = "PostgreSQL major version, e.g. \"16\"."
  type        = string
}

variable "tier" {
  description = "Machine tier, e.g. db-custom-1-3840."
  type        = string
}

variable "storage_gb" {
  description = "Initial SSD size in GB."
  type        = number
}

variable "max_storage_gb" {
  description = "Auto-resize ceiling in GB."
  type        = number
}

variable "high_availability" {
  description = "REGIONAL availability (synchronous standby in a second zone)."
  type        = bool
}

variable "backup_retention_days" {
  description = "Number of daily automated backups retained."
  type        = number
}

variable "point_in_time_recovery" {
  description = "Enable point-in-time recovery (write-ahead log retention)."
  type        = bool
}

variable "deletion_protection" {
  description = "Protect the instance from deletion (Terraform and API)."
  type        = bool
}

variable "private_network" {
  description = "VPC network id with private services access."
  type        = string
}

variable "enable_cmek" {
  description = "Encrypt with var.kms_key (known at plan time)."
  type        = bool
  default     = false
}

variable "kms_key" {
  description = "Crypto key id used when enable_cmek (same region as the instance)."
  type        = string
  default     = ""
}

variable "database_name" {
  description = "Application database name."
  type        = string
  default     = "app"
}

variable "iam_users" {
  description = "Static key (service name) -> service account email that gets an IAM database user."
  type        = map(string)
  default     = {}
}

variable "labels" {
  description = "Instance user labels."
  type        = map(string)
  default     = {}
}

resource "google_sql_database_instance" "this" {
  #checkov:skip=CKV_GCP_6:False positive: ip_configuration.ssl_mode = ENCRYPTED_ONLY rejects unencrypted connections (the check only reads the deprecated require_ssl argument).
  #checkov:skip=CKV_GCP_79:The major version follows the spec (database.version) so the same version runs on all three providers; Hermes does not force the newest major.
  name                = var.name
  database_version    = "POSTGRES_${var.database_version}"
  region              = var.region
  deletion_protection = var.deletion_protection
  encryption_key_name = var.enable_cmek ? var.kms_key : null

  settings {
    tier                        = var.tier
    edition                     = "ENTERPRISE"
    availability_type           = var.high_availability ? "REGIONAL" : "ZONAL"
    disk_type                   = "PD_SSD"
    disk_size                   = var.storage_gb
    disk_autoresize             = true
    disk_autoresize_limit       = var.max_storage_gb
    deletion_protection_enabled = var.deletion_protection
    user_labels                 = var.labels

    ip_configuration {
      ipv4_enabled    = false
      private_network = var.private_network
      ssl_mode        = "ENCRYPTED_ONLY"
    }

    backup_configuration {
      enabled                        = true
      start_time                     = "02:00"
      point_in_time_recovery_enabled = var.point_in_time_recovery
      transaction_log_retention_days = min(7, var.backup_retention_days)

      backup_retention_settings {
        retained_backups = var.backup_retention_days
        retention_unit   = "COUNT"
      }
    }

    maintenance_window {
      day          = 7 # Sunday
      hour         = 3
      update_track = "stable"
    }

    insights_config {
      query_insights_enabled  = true
      query_string_length     = 1024
      record_application_tags = true
      record_client_address   = false
    }

    # Security-relevant PostgreSQL flags (CIS-aligned logging) + IAM authentication. Static
    # blocks so static scanners can read them. Statement text is not logged (log_statement
    # = ddl, log_min_duration_statement = -1); log_duration logs one line per statement.
    database_flags {
      name  = "cloudsql.iam_authentication"
      value = "on"
    }
    database_flags {
      name  = "cloudsql.enable_pgaudit" # CREATE EXTENSION pgaudit activates it (post-provision)
      value = "on"
    }
    database_flags {
      name  = "log_checkpoints"
      value = "on"
    }
    database_flags {
      name  = "log_connections"
      value = "on"
    }
    database_flags {
      name  = "log_disconnections"
      value = "on"
    }
    database_flags {
      name  = "log_duration"
      value = "on"
    }
    database_flags {
      name  = "log_hostname"
      value = "on"
    }
    database_flags {
      name  = "log_lock_waits"
      value = "on"
    }
    database_flags {
      name  = "log_min_error_statement"
      value = "error"
    }
    database_flags {
      name  = "log_min_messages"
      value = "warning"
    }
    database_flags {
      name  = "log_statement"
      value = "ddl"
    }
    database_flags {
      name  = "log_temp_files"
      value = "0"
    }
    database_flags {
      name  = "log_min_duration_statement"
      value = "-1"
    }
  }

  lifecycle {
    precondition {
      condition     = var.max_storage_gb >= var.storage_gb
      error_message = "database.max_storage_gb must be >= database.storage_gb."
    }
  }
}

resource "google_sql_database" "app" {
  name     = var.database_name
  instance = google_sql_database_instance.this.name
  # Dropping a database with live connections or owned objects fails; the instance
  # deletion removes it anyway.
  deletion_policy = "ABANDON"
}

resource "google_sql_user" "iam" {
  for_each = var.iam_users

  # Cloud SQL IAM user name = service account email without ".gserviceaccount.com".
  name            = trimsuffix(each.value, ".gserviceaccount.com")
  instance        = google_sql_database_instance.this.name
  type            = "CLOUD_IAM_SERVICE_ACCOUNT"
  deletion_policy = "ABANDON"
}

output "instance_name" {
  description = "Native instance name."
  value       = google_sql_database_instance.this.name
}

output "connection_name" {
  description = "Instance connection name <project>:<region>:<instance> (Cloud SQL connectors / Auth Proxy)."
  value       = google_sql_database_instance.this.connection_name
}

output "private_ip" {
  description = "Private IP address."
  value       = google_sql_database_instance.this.private_ip_address
}

output "database_name" {
  description = "Application database name."
  value       = google_sql_database.app.name
}

output "iam_users" {
  description = "Service name -> IAM database user name."
  value       = { for k, u in google_sql_user.iam : k => u.name }
}

output "availability_type" {
  description = "REGIONAL (HA) or ZONAL."
  value       = google_sql_database_instance.this.settings[0].availability_type
}

output "deletion_protection" {
  description = "True when both Terraform and API deletion protection are on."
  value       = google_sql_database_instance.this.deletion_protection && google_sql_database_instance.this.settings[0].deletion_protection_enabled
}

output "encryption_key_name" {
  description = "Customer-managed key, or \"\" for Google-managed encryption."
  value       = var.enable_cmek ? google_sql_database_instance.this.encryption_key_name : ""
}
