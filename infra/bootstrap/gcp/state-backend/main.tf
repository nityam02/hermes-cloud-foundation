# -----------------------------------------------------------------------------
# Remote state bucket for one Hermes project on GCP (outputs.md "State backend
# bootstrap contract"). Run once per project with local state, before the first
# `hermes plan`. The OpenTofu/Terraform "gcs" backend locks natively (lock object
# with generation preconditions), so no separate lock store is needed.
#
# Private (UBLA + public access prevention), versioned (old state versions kept
# 90 days), Google-managed or customer-managed encryption, deletion-protected
# (prevent_destroy + force_destroy = false).
# -----------------------------------------------------------------------------

variable "project" {
  description = "Hermes project name (state prefix and labels)."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,28}[a-z0-9]$", var.project))
    error_message = "The project must match ^[a-z][a-z0-9-]{1,28}[a-z0-9]$."
  }
}

variable "region" {
  description = "Bucket location (region)."
  type        = string
}

variable "bucket_name" {
  description = "Globally unique bucket name."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9_-]{1,61}[a-z0-9]$", var.bucket_name)) && !startswith(var.bucket_name, "goog") && !strcontains(var.bucket_name, "google")
    error_message = "bucket_name must be 3-63 characters of lowercase letters, digits, '-' or '_', start and end alphanumeric, and must not start with \"goog\" or contain \"google\"."
  }
}

variable "labels" {
  description = "Additional labels."
  type        = map(string)
  default     = {}
}

variable "kms_key" {
  description = "Optional Cloud KMS crypto key id (same location as the bucket). \"\" = Google-managed encryption."
  type        = string
  default     = ""
}

locals {
  labels = merge(
    { for k, v in var.labels : substr(replace(lower(k), "/[^a-z0-9_-]/", "_"), 0, 63) => substr(replace(lower(v), "/[^a-z0-9_-]/", "_"), 0, 63) },
    {
      "hermes-project"    = var.project
      "hermes-managed-by" = "hermes"
      "hermes-component"  = "storage"
    },
  )
  use_cmek = var.kms_key != ""
}

# Grants the Cloud Storage service agent use of the key (requires permission to set
# IAM on the key).
data "google_storage_project_service_account" "gcs" {
  count = local.use_cmek ? 1 : 0
}

resource "google_kms_crypto_key_iam_member" "gcs" {
  count = local.use_cmek ? 1 : 0

  crypto_key_id = var.kms_key
  role          = "roles/cloudkms.cryptoKeyEncrypterDecrypter"
  member        = "serviceAccount:${data.google_storage_project_service_account.gcs[0].email_address}"
}

# CMEK is optional by contract (kms_key = "" keeps Google-managed encryption).
#trivy:ignore:GCP-0066
resource "google_storage_bucket" "state" {
  #checkov:skip=CKV_GCP_62:State access is audited with Cloud Audit Logs (Data Access) rather than usage-log buckets.
  name                        = var.bucket_name
  location                    = upper(var.region)
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = false
  labels                      = local.labels

  versioning {
    enabled = true
  }

  dynamic "encryption" {
    for_each = local.use_cmek ? [var.kms_key] : []
    content {
      default_kms_key_name = encryption.value
    }
  }

  # Keep superseded state versions for 90 days.
  lifecycle_rule {
    action {
      type = "Delete"
    }
    condition {
      days_since_noncurrent_time = 90
      with_state                 = "ARCHIVED"
    }
  }

  lifecycle_rule {
    action {
      type = "AbortIncompleteMultipartUpload"
    }
    condition {
      age = 1
    }
  }

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [google_kms_crypto_key_iam_member.gcs]
}

output "bucket" {
  description = "State bucket name."
  value       = google_storage_bucket.state.name
}

output "backend_config" {
  description = "gcs backend settings; hermes appends /<environment>/gcp to prefix for each environment."
  value = {
    bucket = google_storage_bucket.state.name
    prefix = "hermes/${var.project}"
  }
}
