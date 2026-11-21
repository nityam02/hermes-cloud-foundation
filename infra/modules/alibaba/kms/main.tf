# KMS capability: one symmetric customer-managed key per environment.
#
# KMS 3.0: without dkms_instance_id the provider creates the region's default key
# (customer master key); with it, the key lives in that (pre-existing, separately billed)
# KMS instance. Secrets Manager secrets in KMS 3.0 must be encrypted with a key that is in
# the same instance as the secret, so the composition passes kms_instance_id whenever
# secrets are used.

variable "prefix" {
  description = "Native name prefix: <project>-<environment>."
  type        = string
}

variable "kms_instance_id" {
  description = "KMS 3.0 instance ID to create the key in, or empty for the region's default key."
  type        = string
  default     = ""
}

variable "protected" {
  description = "Protected environment: deletion protection and a 30-day pending-deletion window."
  type        = bool
  default     = false
}

variable "tags" {
  description = "Tags applied to the key."
  type        = map(string)
  default     = {}
}

resource "alicloud_kms_key" "this" {
  description            = "Hermes customer-managed key for ${var.prefix}"
  key_spec               = "Aliyun_AES_256" # gitleaks:allow (key-spec enum, not a secret)
  key_usage              = "ENCRYPT/DECRYPT"
  origin                 = "Aliyun_KMS"
  protection_level       = "SOFTWARE"
  dkms_instance_id       = var.kms_instance_id == "" ? null : var.kms_instance_id
  automatic_rotation     = "Enabled"
  rotation_interval      = "365d"
  pending_window_in_days = var.protected ? 30 : 7
  deletion_protection    = var.protected ? "Enabled" : "Disabled"
  status                 = "Enabled"
  tags                   = merge(var.tags, { "hermes-component" = "secrets" })
}

output "key_id" {
  description = "KMS key ID."
  value       = alicloud_kms_key.this.id
}
