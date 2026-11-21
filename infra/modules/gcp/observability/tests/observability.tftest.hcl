# Mock-provider tests: log routing and alerting. No cloud calls.

mock_provider "google" {}

variables {
  project_id         = "example-project"
  region             = "europe-north1"
  logs_enabled       = true
  names              = { log_bucket = "demo-dev-logs-0a1b2c3d", log_sink = "demo-dev-logs", log_exclusion = "demo-dev-logs" }
  log_retention_days = 90
  log_filter         = "(resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"demo-dev-api\")"
  display_prefix     = "demo-dev"
  cloud_run_services = { api = "demo-dev-api" }
  sql_enabled        = true
  sql_database_id    = "example-project:demo-dev-pg-0a1b"
}

run "logs_routed_with_retention_no_alerts" {
  command = apply

  assert {
    condition     = google_logging_project_bucket_config.this[0].retention_days == 90 && google_logging_project_bucket_config.this[0].location == "europe-north1"
    error_message = "The environment log bucket must be regional with the requested retention."
  }

  assert {
    condition     = google_logging_project_sink.this[0].filter == google_logging_project_exclusion.default_sink[0].filter && startswith(google_logging_project_sink.this[0].destination, "logging.googleapis.com/")
    error_message = "The sink and the _Default exclusion must use the same filter."
  }

  assert {
    condition     = output.alert_channel == "" && length(google_monitoring_alert_policy.cloud_run_5xx) == 0 && length(google_monitoring_alert_policy.sql_cpu) == 0
    error_message = "No alert email: no channel and no alert policies."
  }
}

run "alerts_with_email" {
  command = apply

  variables {
    alert_email = "oncall@example.com"
  }

  assert {
    condition     = google_monitoring_notification_channel.email[0].labels["email_address"] == "oncall@example.com" && output.alert_channel != ""
    error_message = "An email notification channel must be created."
  }

  assert {
    condition     = toset(keys(google_monitoring_alert_policy.cloud_run_5xx)) == toset(["api"]) && length(google_monitoring_alert_policy.sql_cpu) == 1 && length(google_monitoring_alert_policy.sql_disk) == 1
    error_message = "One 5xx alert per service plus Cloud SQL CPU and disk alerts."
  }

  assert {
    condition     = strcontains(google_monitoring_alert_policy.cloud_run_5xx["api"].conditions[0].condition_threshold[0].filter, "service_name = \"demo-dev-api\"")
    error_message = "The 5xx alert must target the exact service."
  }
}

run "logs_disabled_use_default_bucket" {
  command = apply

  variables {
    logs_enabled = false
  }

  assert {
    condition     = length(google_logging_project_sink.this) == 0 && output.log_destination == "projects/example-project/locations/global/buckets/_Default"
    error_message = "Without observability no routing is created and logs stay in _Default."
  }
}
