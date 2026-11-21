variable "trusted_principal_arn" {
  description = "ARN of the principal that runs `hermes inventory aws` (an IAM role or user in the operator account, or arn:aws:iam::<account>:root)."
  type        = string

  validation {
    condition     = can(regex("^arn:aws[a-zA-Z-]*:iam::[0-9]{12}:(root|role/.+|user/.+)$", var.trusted_principal_arn))
    error_message = "trusted_principal_arn must be an IAM role, user or account root ARN."
  }
}

variable "external_id" {
  description = "Random value shared out-of-band with the operator; required as sts:ExternalId (prevents confused-deputy use of the role)."
  type        = string
  sensitive   = true

  validation {
    condition     = length(var.external_id) >= 16 && length(var.external_id) <= 1224 && can(regex("^[\\w+=,.@:/-]+$", var.external_id))
    error_message = "external_id must be 16-1224 characters from [A-Za-z0-9+=,.@:/_-]."
  }
}

variable "role_name" {
  description = "Name of the discovery role."
  type        = string
  default     = "HermesDiscoveryReadOnly"
}

variable "tags" {
  description = "Additional tags for the role."
  type        = map(string)
  default     = {}
}
