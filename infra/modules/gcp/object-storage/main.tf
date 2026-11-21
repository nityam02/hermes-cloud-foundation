# -----------------------------------------------------------------------------
# Private, regional Cloud Storage buckets.
#   - uniform bucket-level access + public access prevention "enforced"
#   - versioning per spec; noncurrent versions deleted after N days
#   - optional expiry of current objects (retention_days > 0)
#   - CMEK default key when enabled (the GCS service agent must already hold
#     encrypter/decrypter on the key: the composition orders this)
#   - force_destroy = false: a bucket that still holds objects is never deleted
#   - soft delete keeps the Google default (7 days)
# TLS: Cloud Storage authenticated access is HTTPS-only and public access is
# prevented, so no "TLS-only" bucket policy exists or is needed on GCP.
# Per-service access is granted by the composition (bucket-level IAM).
# -----------------------------------------------------------------------------

variable "buckets" {
  description = "Spec bucket name -> native name, versioning, retention_days."
  type = map(object({
    name           = string
    versioning     = bool
    retention_days = number
  }))
  default = {}
}

variable "location" {
  description = "Bucket location (the environment region)."
  type        = string
}

variable "enable_cmek" {
  description = "Encrypt with var.kms_key (known at plan time)."
  type        = bool
  default     = false
}

variable "kms_key" {
  description = "Crypto key id used when enable_cmek."
  type        = string
  default     = ""
}

variable "noncurrent_version_retention_days" {
  description = "Days a noncurrent object version is kept before deletion."
  type        = number
  default     = 30
}

variable "labels" {
  description = "Bucket labels."
  type        = map(string)
  default     = {}
}

resource "google_storage_bucket" "this" {
  #checkov:skip=CKV_GCP_62:Access is audited with Cloud Audit Logs (Data Access) instead of legacy usage-log buckets.
  #checkov:skip=CKV_GCP_78:Versioning follows the spec (default true); a bucket may opt out explicitly (same as CKV_AWS_21 on AWS).
  for_each = var.buckets

  name                        = each.value.name
  location                    = upper(var.location)
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = false
  labels                      = var.labels

  versioning {
    enabled = each.value.versioning
  }

  dynamic "encryption" {
    for_each = var.enable_cmek ? [1] : []
    content {
      default_kms_key_name = var.kms_key
    }
  }

  # Expire current objects after retention_days (0 = keep forever). On a versioned
  # bucket the object becomes noncurrent and the next rule removes it.
  dynamic "lifecycle_rule" {
    for_each = each.value.retention_days > 0 ? [each.value.retention_days] : []
    content {
      action {
        type = "Delete"
      }
      condition {
        age        = lifecycle_rule.value
        with_state = "LIVE"
      }
    }
  }

  dynamic "lifecycle_rule" {
    for_each = each.value.versioning ? [var.noncurrent_version_retention_days] : []
    content {
      action {
        type = "Delete"
      }
      condition {
        days_since_noncurrent_time = lifecycle_rule.value
        with_state                 = "ARCHIVED"
      }
    }
  }

  lifecycle_rule {
    action {
      type = "AbortIncompleteMultipartUpload"
    }
    condition {
      age = 7
    }
  }
}

output "names" {
  description = "Spec bucket name -> native bucket name."
  value       = { for k, b in google_storage_bucket.this : k => b.name }
}

output "urls" {
  description = "Spec bucket name -> gs:// URL."
  value       = { for k, b in google_storage_bucket.this : k => "gs://${b.name}" }
}

output "kms_keys" {
  description = "Spec bucket name -> default customer-managed key (\"\" = Google-managed)."
  value       = { for k, b in google_storage_bucket.this : k => try(b.encryption[0].default_kms_key_name, "") }
}
