# -----------------------------------------------------------------------------
# Secret Manager secret CONTAINERS only. No versions or values are ever created
# here: values are added out-of-band (`gcloud secrets versions add`), so nothing
# secret reaches state or outputs. Replication is user-managed in the
# environment region (data residency), CMEK-encrypted when enabled.
# -----------------------------------------------------------------------------

variable "secrets" {
  description = "Spec secret name -> native secret_id."
  type        = map(string)
  default     = {}
}

variable "region" {
  description = "Single replica location."
  type        = string
}

variable "enable_cmek" {
  description = "Encrypt the replica with var.kms_key (known at plan time)."
  type        = bool
  default     = false
}

variable "kms_key" {
  description = "Crypto key id used when enable_cmek (must be in var.region)."
  type        = string
  default     = ""
}

variable "version_destroy_ttl" {
  description = "Destroyed versions stay disabled (recoverable) for this long before destruction."
  type        = string
  default     = "604800s" # 7 days
}

variable "labels" {
  description = "Secret labels."
  type        = map(string)
  default     = {}
}

resource "google_secret_manager_secret" "this" {
  for_each = var.secrets

  secret_id           = each.value
  labels              = var.labels
  version_destroy_ttl = var.version_destroy_ttl

  replication {
    user_managed {
      replicas {
        location = var.region

        dynamic "customer_managed_encryption" {
          for_each = var.enable_cmek ? [1] : []
          content {
            kms_key_name = var.kms_key
          }
        }
      }
    }
  }
}

output "ids" {
  description = "Spec secret name -> projects/<p>/secrets/<id>."
  value       = { for k, s in google_secret_manager_secret.this : k => s.id }
}

output "secret_ids" {
  description = "Spec secret name -> short secret id (same-project reference for Cloud Run)."
  value       = { for k, s in google_secret_manager_secret.this : k => s.secret_id }
}
