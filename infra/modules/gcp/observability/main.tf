# -----------------------------------------------------------------------------
# Logs: a dedicated regional log bucket with retention = log_retention_days and a
# project sink routing exactly this environment's Cloud Run, Cloud SQL, subnet
# flow and Cloud NAT logs into it. The same filter is excluded from the _Default
# sink (a project exclusion), so the logs are stored (and billed) once.
# Alerts (only when an alert email is set): an email notification channel,
# one 5xx alert per Cloud Run service, Cloud SQL CPU and disk alerts.
# Tracing is exported by the collector sidecar (container-service module).
# -----------------------------------------------------------------------------

variable "project_id" {
  description = "Project id."
  type        = string
}

variable "region" {
  description = "Log bucket location."
  type        = string
}

variable "logs_enabled" {
  description = "Create the dedicated log bucket, sink and _Default exclusion."
  type        = bool
}

variable "names" {
  description = "Native names: log_bucket (unique, never reused), log_sink, log_exclusion."
  type = object({
    log_bucket    = string
    log_sink      = string
    log_exclusion = string
  })
}

variable "log_retention_days" {
  description = "Log bucket retention in days."
  type        = number
}

variable "log_filter" {
  description = "Logging query selecting this environment's logs."
  type        = string
}

variable "alert_email" {
  description = "Email notification target; \"\" disables alerting."
  type        = string
  default     = ""
}

variable "display_prefix" {
  description = "Prefix for display names (<project>-<environment>)."
  type        = string
}

variable "cloud_run_services" {
  description = "Spec service name -> native Cloud Run service name (5xx alerts)."
  type        = map(string)
  default     = {}
}

variable "sql_enabled" {
  description = "Create Cloud SQL alerts (known at plan time)."
  type        = bool
  default     = false
}

variable "sql_database_id" {
  description = "Cloud SQL monitored-resource database_id (<project_id>:<instance>)."
  type        = string
  default     = ""
}

variable "labels" {
  description = "User labels for monitoring resources."
  type        = map(string)
  default     = {}
}

locals {
  alerts = var.alert_email != ""
}

resource "google_logging_project_bucket_config" "this" {
  count = var.logs_enabled ? 1 : 0

  project        = var.project_id
  location       = var.region
  bucket_id      = var.names.log_bucket
  retention_days = var.log_retention_days
  description    = "Hermes ${var.display_prefix} logs."
}

resource "google_logging_project_sink" "this" {
  count = var.logs_enabled ? 1 : 0

  name                   = var.names.log_sink
  description            = "Routes ${var.display_prefix} logs to its log bucket."
  destination            = "logging.googleapis.com/${google_logging_project_bucket_config.this[0].id}"
  filter                 = var.log_filter
  unique_writer_identity = true
}

# Project exclusions apply to the _Default sink: this environment's logs are kept
# only in the dedicated bucket (single ingestion, environment-specific retention).
resource "google_logging_project_exclusion" "default_sink" {
  count = var.logs_enabled ? 1 : 0

  name        = var.names.log_exclusion
  description = "Excludes ${var.display_prefix} logs from _Default (routed to the environment log bucket)."
  filter      = var.log_filter

  depends_on = [google_logging_project_sink.this]
}

resource "google_monitoring_notification_channel" "email" {
  count = local.alerts ? 1 : 0

  display_name = "${var.display_prefix} alerts"
  type         = "email"
  labels = {
    email_address = var.alert_email
  }
  user_labels = var.labels
}

resource "google_monitoring_alert_policy" "cloud_run_5xx" {
  for_each = local.alerts ? var.cloud_run_services : {}

  display_name          = "${var.display_prefix} ${each.key}: 5xx responses"
  combiner              = "OR"
  severity              = "ERROR"
  user_labels           = var.labels
  notification_channels = [google_monitoring_notification_channel.email[0].id]

  conditions {
    display_name = "More than 5 server errors in 5 minutes"
    condition_threshold {
      filter          = "resource.type = \"cloud_run_revision\" AND resource.labels.service_name = \"${each.value}\" AND metric.type = \"run.googleapis.com/request_count\" AND metric.labels.response_code_class = \"5xx\""
      comparison      = "COMPARISON_GT"
      threshold_value = 5
      duration        = "0s"
      aggregations {
        alignment_period     = "300s"
        per_series_aligner   = "ALIGN_SUM"
        cross_series_reducer = "REDUCE_SUM"
        group_by_fields      = ["resource.label.service_name"]
      }
      trigger {
        count = 1
      }
    }
  }

  alert_strategy {
    auto_close = "1800s"
  }

  documentation {
    mime_type = "text/markdown"
    content   = "Cloud Run service `${each.value}` returned more than 5 HTTP 5xx responses in 5 minutes. Check its logs in the environment log bucket."
  }
}

resource "google_monitoring_alert_policy" "sql_cpu" {
  count = local.alerts && var.sql_enabled ? 1 : 0

  display_name          = "${var.display_prefix} database: CPU above 80%"
  combiner              = "OR"
  severity              = "WARNING"
  user_labels           = var.labels
  notification_channels = [google_monitoring_notification_channel.email[0].id]

  conditions {
    display_name = "CPU utilization above 80% for 10 minutes"
    condition_threshold {
      filter          = "resource.type = \"cloudsql_database\" AND resource.labels.database_id = \"${var.sql_database_id}\" AND metric.type = \"cloudsql.googleapis.com/database/cpu/utilization\""
      comparison      = "COMPARISON_GT"
      threshold_value = 0.8
      duration        = "600s"
      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_MEAN"
      }
      trigger {
        count = 1
      }
    }
  }

  alert_strategy {
    auto_close = "1800s"
  }

  documentation {
    mime_type = "text/markdown"
    content   = "Cloud SQL instance `${var.sql_database_id}` CPU above 80% for 10 minutes. Consider a larger database.size."
  }
}

resource "google_monitoring_alert_policy" "sql_disk" {
  count = local.alerts && var.sql_enabled ? 1 : 0

  display_name          = "${var.display_prefix} database: disk above 85%"
  combiner              = "OR"
  severity              = "CRITICAL"
  user_labels           = var.labels
  notification_channels = [google_monitoring_notification_channel.email[0].id]

  conditions {
    display_name = "Disk utilization above 85%"
    condition_threshold {
      filter          = "resource.type = \"cloudsql_database\" AND resource.labels.database_id = \"${var.sql_database_id}\" AND metric.type = \"cloudsql.googleapis.com/database/disk/utilization\""
      comparison      = "COMPARISON_GT"
      threshold_value = 0.85
      duration        = "300s"
      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_MEAN"
      }
      trigger {
        count = 1
      }
    }
  }

  alert_strategy {
    auto_close = "1800s"
  }

  documentation {
    mime_type = "text/markdown"
    content   = "Cloud SQL instance `${var.sql_database_id}` disk above 85%. Storage auto-resizes up to database.max_storage_gb; raise it if the cap is reached."
  }
}

output "log_destination" {
  description = "Log bucket resource name, or the project _Default bucket when logs are not routed."
  value       = var.logs_enabled ? google_logging_project_bucket_config.this[0].id : "projects/${var.project_id}/locations/global/buckets/_Default"
}

output "alert_channel" {
  description = "Notification channel id, or \"\"."
  value       = local.alerts ? google_monitoring_notification_channel.email[0].id : ""
}
