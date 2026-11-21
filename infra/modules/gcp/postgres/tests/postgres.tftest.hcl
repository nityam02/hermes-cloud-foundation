# Mock-provider tests: private IP, TLS, IAM auth, HA and deletion protection. No cloud calls.

mock_provider "google" {}

variables {
  name                   = "demo-dev-pg-0a1b"
  region                 = "europe-north1"
  database_version       = "16"
  tier                   = "db-custom-1-3840"
  storage_gb             = 20
  max_storage_gb         = 100
  high_availability      = true
  backup_retention_days  = 14
  point_in_time_recovery = true
  deletion_protection    = true
  private_network        = "projects/example-project/global/networks/demo-dev-vpc"
  iam_users              = { api = "demo-dev-api@example-project.iam.gserviceaccount.com" }
}

run "secure_highly_available_instance" {
  command = apply

  assert {
    condition     = google_sql_database_instance.this.settings[0].ip_configuration[0].ipv4_enabled == false && google_sql_database_instance.this.settings[0].ip_configuration[0].ssl_mode == "ENCRYPTED_ONLY"
    error_message = "The instance must have no public IP and require TLS."
  }

  assert {
    condition     = output.availability_type == "REGIONAL" && output.deletion_protection == true && google_sql_database_instance.this.database_version == "POSTGRES_16"
    error_message = "HA must be REGIONAL and both deletion protections must be on."
  }

  assert {
    condition     = anytrue([for f in google_sql_database_instance.this.settings[0].database_flags : f.name == "cloudsql.iam_authentication" && f.value == "on"])
    error_message = "IAM database authentication must be enabled."
  }

  assert {
    condition = { for f in google_sql_database_instance.this.settings[0].database_flags : f.name => f.value } == {
      "cloudsql.iam_authentication" = "on", "cloudsql.enable_pgaudit" = "on", "log_checkpoints" = "on", "log_connections" = "on",
      "log_disconnections"          = "on", "log_duration" = "on", "log_hostname" = "on", "log_lock_waits" = "on",
      "log_min_error_statement"     = "error", "log_min_messages" = "warning", "log_statement" = "ddl", "log_temp_files" = "0",
      "log_min_duration_statement"  = "-1",
    }
    error_message = "Cloud SQL must carry exactly the CIS-aligned logging flags (statement text never logged)."
  }

  assert {
    condition     = google_sql_database_instance.this.settings[0].backup_configuration[0].point_in_time_recovery_enabled == true && google_sql_database_instance.this.settings[0].backup_configuration[0].backup_retention_settings[0].retained_backups == 14 && google_sql_database_instance.this.settings[0].backup_configuration[0].transaction_log_retention_days == 7
    error_message = "Backups, PITR and retention must follow the spec (log retention capped at 7 days)."
  }

  assert {
    condition     = google_sql_database_instance.this.settings[0].disk_autoresize == true && google_sql_database_instance.this.settings[0].disk_autoresize_limit == 100
    error_message = "Storage must auto-resize up to max_storage_gb."
  }

  assert {
    condition     = google_sql_user.iam["api"].name == "demo-dev-api@example-project.iam" && google_sql_user.iam["api"].type == "CLOUD_IAM_SERVICE_ACCOUNT" && output.database_name == "app"
    error_message = "Each service gets an IAM database user named after its service account."
  }
}

run "zonal_unprotected_instance" {
  command = apply

  variables {
    high_availability   = false
    deletion_protection = false
  }

  assert {
    condition     = output.availability_type == "ZONAL" && output.deletion_protection == false && output.encryption_key_name == ""
    error_message = "Non-HA is ZONAL; deletion protection off when not requested; Google-managed encryption by default."
  }
}

run "rejects_storage_cap_below_size" {
  command = plan

  variables {
    max_storage_gb = 10
  }

  expect_failures = [google_sql_database_instance.this]
}
