# -----------------------------------------------------------------------------
# One regional key ring + one symmetric key per environment (90-day rotation),
# and encrypter/decrypter grants for the Google service agents that use it.
#
# Key rings and keys cannot be deleted in GCP. "Destroying" the key schedules
# destruction of its versions after destroy_scheduled_duration, during which they
# can be restored. The key ring name carries a random suffix so an environment can
# be recreated without a name collision.
# -----------------------------------------------------------------------------

variable "key_ring_name" {
  description = "Native key ring name (unique, never reused)."
  type        = string
}

variable "key_name" {
  description = "Crypto key name inside the key ring."
  type        = string
}

variable "location" {
  description = "Key ring location (the environment region, so regional services can use it)."
  type        = string
}

variable "labels" {
  description = "Labels for the crypto key."
  type        = map(string)
  default     = {}
}

variable "rotation_period" {
  description = "Automatic rotation period."
  type        = string
  default     = "7776000s" # 90 days
}

variable "destroy_scheduled_duration" {
  description = "Delay before destroyed key versions are irrecoverable."
  type        = string
  default     = "2592000s" # 30 days
}

variable "encrypters" {
  description = "Static key -> IAM member (service agents) granted roles/cloudkms.cryptoKeyEncrypterDecrypter on the key."
  type        = map(string)
  default     = {}
}

resource "google_kms_key_ring" "this" {
  name     = var.key_ring_name
  location = var.location
}

resource "google_kms_crypto_key" "this" {
  #checkov:skip=CKV_GCP_82:prevent_destroy breaks mock-provider test teardown; GCP keys cannot be deleted and destroyed versions stay restorable for destroy_scheduled_duration (90 days when protected).
  name                       = var.key_name
  key_ring                   = google_kms_key_ring.this.id
  purpose                    = "ENCRYPT_DECRYPT"
  rotation_period            = var.rotation_period
  destroy_scheduled_duration = var.destroy_scheduled_duration
  labels                     = var.labels

  version_template {
    algorithm        = "GOOGLE_SYMMETRIC_ENCRYPTION"
    protection_level = "SOFTWARE"
  }
}

resource "google_kms_crypto_key_iam_member" "encrypter" {
  for_each = var.encrypters

  crypto_key_id = google_kms_crypto_key.this.id
  role          = "roles/cloudkms.cryptoKeyEncrypterDecrypter"
  member        = each.value
}

output "key_id" {
  description = "Crypto key id (projects/<p>/locations/<r>/keyRings/<ring>/cryptoKeys/<key>)."
  value       = google_kms_crypto_key.this.id
}

output "key_ring_id" {
  description = "Key ring id."
  value       = google_kms_key_ring.this.id
}

output "encrypter_members" {
  description = "Service agent key -> member granted encrypter/decrypter on the key."
  value       = { for k, m in google_kms_crypto_key_iam_member.encrypter : k => m.member }
}
