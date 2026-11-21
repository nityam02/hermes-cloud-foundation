# Secrets capability: KMS Secrets Manager secret containers.
#
# The CreateSecret API requires an initial value. The module writes a fixed, non-secret
# placeholder ("set-out-of-band") and then ignores changes to the value, so real values
# are only ever set out-of-band (console, CLI or PutSecretValue) and never pass through
# OpenTofu/Terraform state. KMS 3.0 requires the secret and its encryption key to be in a
# KMS instance (dkms_instance_id + encryption_key_id).

variable "prefix" {
  description = "Native name prefix: <project>-<environment>."
  type        = string
}

variable "names" {
  description = "Spec secret names."
  type        = list(string)
  default     = []
}

variable "kms_instance_id" {
  description = "KMS 3.0 instance ID that holds the secrets."
  type        = string
}

variable "encryption_key_id" {
  description = "KMS key ID (in kms_instance_id) used to encrypt the secrets."
  type        = string
}

variable "protected" {
  description = "Protected environment: 30-day recovery window instead of 7."
  type        = bool
  default     = false
}

variable "tags" {
  description = "Tags applied to every secret."
  type        = map(string)
  default     = {}
}

resource "alicloud_kms_secret" "this" {
  for_each = toset(var.names)

  secret_name                   = "${var.prefix}-${each.key}"
  description                   = "Hermes secret ${each.key} for ${var.prefix}. The value is managed out-of-band."
  secret_type                   = "Generic"
  secret_data_type              = "text"
  secret_data                   = "set-out-of-band" # placeholder, not a secret
  version_id                    = "hermes-placeholder"
  dkms_instance_id              = var.kms_instance_id
  encryption_key_id             = var.encryption_key_id
  recovery_window_in_days       = var.protected ? 30 : 7
  force_delete_without_recovery = false
  tags                          = merge(var.tags, { "hermes-component" = "secrets" })

  lifecycle {
    ignore_changes = [secret_data, version_id]
  }
}

output "secrets" {
  description = "Spec secret name -> native secret name."
  value       = { for k, s in alicloud_kms_secret.this : k => s.secret_name }
}
