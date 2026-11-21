# Per-service CloudWatch log groups (always created: ECS needs a log destination) and, when
# alerting is enabled, the environment alert topic with an optional e-mail subscription.
# Alarms live next to the resources they watch (container-service, postgres, messaging) and
# publish to this topic.
#
# The alert topic is ALWAYS encrypted (policy HCF-POL-002). CloudWatch alarms cannot publish to a
# topic encrypted with the AWS-managed alias/aws/sns key, so: with the environment CMK
# (kms_enabled) the topic uses it; without one, this module creates a small dedicated key for the
# alert topic only (about 1 USD per month per environment with alerts).

variable "log_group_prefix" {
  description = "Log group prefix, e.g. /hermes/<project>/<environment>."
  type        = string
}

variable "service_names" {
  description = "Spec service names that need a log group."
  type        = set(string)
  default     = []
}

variable "log_retention_days" {
  description = "Retention (a CloudWatch-supported value)."
  type        = number
  default     = 30
}

variable "kms_enabled" {
  description = "The environment has a customer-managed key (kms_key_arn). When false, logs use AWS-managed encryption and the alert topic gets a dedicated key."
  type        = bool
  default     = false
}

variable "kms_key_arn" {
  description = "Environment CMK for log groups and the alert topic (required when kms_enabled)."
  type        = string
  default     = null

  validation {
    condition     = !var.kms_enabled || var.kms_key_arn != null
    error_message = "kms_key_arn is required when kms_enabled is true."
  }
}

variable "alert_key_deletion_window_in_days" {
  description = "Deletion waiting period (7-30) of the dedicated alert-topic key (only created when kms_enabled is false)."
  type        = number
  default     = 30

  validation {
    condition     = var.alert_key_deletion_window_in_days >= 7 && var.alert_key_deletion_window_in_days <= 30
    error_message = "alert_key_deletion_window_in_days must be between 7 and 30."
  }
}

variable "alerts_enabled" {
  description = "Create the alert topic."
  type        = bool
  default     = true
}

variable "alert_topic_name" {
  description = "Alert topic name."
  type        = string
}

variable "alert_email" {
  description = "Optional e-mail address subscribed to the alert topic (\"\" for none)."
  type        = string
  default     = ""
}

variable "account_id" {
  description = "AWS account id."
  type        = string
}

variable "partition" {
  description = "AWS partition."
  type        = string
  default     = "aws"
}

variable "region" {
  description = "AWS region."
  type        = string
}

variable "tags" {
  description = "Tags."
  type        = map(string)
  default     = {}
}

locals {
  alert_topic_arn = "arn:${var.partition}:sns:${var.region}:${var.account_id}:${var.alert_topic_name}"
  dedicated_key   = var.alerts_enabled && !var.kms_enabled

  # Key policy of the dedicated alert-topic key. Every value is known at plan time.
  alert_key_policy = {
    Version = "2012-10-17"
    Statement = [
      {
        # The account root, so IAM policies govern use and the key cannot become unmanageable.
        Sid       = "EnableAccountIamPolicies"
        Effect    = "Allow"
        Principal = { AWS = "arn:${var.partition}:iam::${var.account_id}:root" }
        Action    = "kms:*"
        Resource  = "*"
      },
      {
        # Alarm actions in this account and region publishing to the encrypted topic.
        Sid       = "AllowCloudWatchAlarmsToAlertTopic"
        Effect    = "Allow"
        Principal = { Service = "cloudwatch.amazonaws.com" }
        Action    = ["kms:GenerateDataKey*", "kms:Decrypt"]
        Resource  = "*"
        Condition = {
          StringEquals = { "aws:SourceAccount" = var.account_id }
          ArnLike      = { "aws:SourceArn" = "arn:${var.partition}:cloudwatch:${var.region}:${var.account_id}:alarm:*" }
        }
      },
      {
        # SNS using the key for this topic only.
        Sid       = "AllowSnsForAlertTopic"
        Effect    = "Allow"
        Principal = { Service = "sns.amazonaws.com" }
        Action    = ["kms:GenerateDataKey*", "kms:Decrypt"]
        Resource  = "*"
        Condition = {
          StringEquals = { "aws:SourceAccount" = var.account_id }
          ArnLike      = { "aws:SourceArn" = local.alert_topic_arn }
        }
      },
    ]
  }
}

resource "aws_cloudwatch_log_group" "service" {
  for_each = var.service_names

  name              = "${var.log_group_prefix}/services/${each.key}"
  retention_in_days = var.log_retention_days
  kms_key_id        = var.kms_key_arn
  tags              = var.tags
}

# Dedicated key for the alert topic when the environment has no CMK (CloudWatch alarm actions
# cannot publish to a topic encrypted with the AWS-managed aws/sns key). Not verified against real
# AWS (mock providers only; see docs/providers/aws.md, known limitations).
resource "aws_kms_key" "alerts" {
  count = local.dedicated_key ? 1 : 0

  description             = "Hermes alert topic ${var.alert_topic_name}: encryption at rest"
  enable_key_rotation     = true
  deletion_window_in_days = var.alert_key_deletion_window_in_days
  policy                  = jsonencode(local.alert_key_policy)
  tags                    = var.tags
}

resource "aws_sns_topic" "alerts" {
  count = var.alerts_enabled ? 1 : 0

  name              = var.alert_topic_name
  kms_master_key_id = var.kms_enabled ? var.kms_key_arn : aws_kms_key.alerts[0].arn
  tags              = var.tags
}

resource "aws_sns_topic_policy" "alerts" {
  count = var.alerts_enabled ? 1 : 0

  arn = "arn:${var.partition}:sns:${var.region}:${var.account_id}:${aws_sns_topic.alerts[0].name}"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "AccountOwnerManage"
        Effect    = "Allow"
        Principal = { AWS = "arn:${var.partition}:iam::${var.account_id}:root" }
        Action    = ["sns:GetTopicAttributes", "sns:SetTopicAttributes", "sns:AddPermission", "sns:RemovePermission", "sns:DeleteTopic", "sns:Subscribe", "sns:ListSubscriptionsByTopic", "sns:Publish"]
        Resource  = local.alert_topic_arn
      },
      {
        Sid       = "CloudWatchAlarmsInThisAccount"
        Effect    = "Allow"
        Principal = { Service = "cloudwatch.amazonaws.com" }
        Action    = "sns:Publish"
        Resource  = local.alert_topic_arn
        Condition = {
          StringEquals = { "aws:SourceAccount" = var.account_id }
          ArnLike      = { "aws:SourceArn" = "arn:${var.partition}:cloudwatch:${var.region}:${var.account_id}:alarm:*" }
        }
      },
      {
        Sid       = "DenyInsecureTransport"
        Effect    = "Deny"
        Principal = "*"
        Action    = "sns:Publish"
        Resource  = local.alert_topic_arn
        Condition = { Bool = { "aws:SecureTransport" = "false" } }
      },
    ]
  })
}

# E-mail subscriptions stay "pending confirmation" until the recipient confirms.
resource "aws_sns_topic_subscription" "email" {
  count = var.alerts_enabled && var.alert_email != "" ? 1 : 0

  topic_arn = "arn:${var.partition}:sns:${var.region}:${var.account_id}:${aws_sns_topic.alerts[0].name}"
  protocol  = "email"
  endpoint  = var.alert_email
}

output "service_log_groups" {
  description = "Service name -> { name, arn } (ARN built from the name: known at plan time)."
  value = {
    for k, g in aws_cloudwatch_log_group.service : k => {
      name = g.name
      arn  = "arn:${var.partition}:logs:${var.region}:${var.account_id}:log-group:${g.name}"
    }
  }
}

output "alert_topic_arn" {
  description = "Alert topic ARN, or null when alerts are disabled."
  value       = var.alerts_enabled ? "arn:${var.partition}:sns:${var.region}:${var.account_id}:${aws_sns_topic.alerts[0].name}" : null
}

output "alert_key_arn" {
  description = "ARN of the key that encrypts the alert topic (the environment CMK or the dedicated key), or null when alerts are disabled."
  value       = !var.alerts_enabled ? null : (var.kms_enabled ? var.kms_key_arn : aws_kms_key.alerts[0].arn)
}

output "alert_key_policy" {
  description = "Policy (JSON) of the dedicated alert-topic key, or null when no dedicated key is created."
  value       = local.dedicated_key ? jsonencode(local.alert_key_policy) : null
}

output "log_destination" {
  description = "Log group prefix for every Hermes log group in this environment."
  value       = var.log_group_prefix
}
