# Secret containers only. Values are set out-of-band (never by IaC), so no
# aws_secretsmanager_secret_version is ever created and no value reaches state.

variable "secrets" {
  description = "Spec secret name -> native secret name."
  type        = map(string)
  default     = {}
}

variable "kms_key_arn" {
  description = "CMK; null uses the AWS-managed aws/secretsmanager key."
  type        = string
  default     = null
}

variable "recovery_window_in_days" {
  description = "Recovery window after deletion (7-30)."
  type        = number
  default     = 7
}

variable "tags" {
  description = "Tags."
  type        = map(string)
  default     = {}
}

resource "aws_secretsmanager_secret" "this" {
  #checkov:skip=CKV2_AWS_57:Values are application-owned and set out-of-band; rotation depends on the issuer of each secret and is configured outside Hermes.
  #checkov:skip=CKV_AWS_149:A CMK is used when security.customer_managed_keys is true; otherwise the AWS-managed key is the documented default.
  for_each = var.secrets

  name                    = each.value
  description             = "Hermes secret \"${each.key}\". The value is set out-of-band; Hermes never manages it."
  kms_key_id              = var.kms_key_arn
  recovery_window_in_days = var.recovery_window_in_days
  tags                    = var.tags
}

output "arns" {
  description = "Spec secret name -> secret ARN."
  value       = { for k, s in aws_secretsmanager_secret.this : k => s.arn }
}
