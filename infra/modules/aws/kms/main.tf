# One customer-managed KMS key per environment (used when security.customer_managed_keys).
# Key policy: the account root (so IAM policies govern use; avoids lock-out), CloudWatch Logs
# (only this environment's log groups), SNS (delivery from this environment's topics to
# CMK-encrypted SQS queues) and CloudWatch (alarms in this account notifying the CMK-encrypted
# alert topic). Every value in the policy is known at plan time (names, not resource ARNs), so
# the plan policies can evaluate it.

variable "alias_name" {
  description = "Alias, including the alias/ prefix."
  type        = string
}

variable "description" {
  description = "Key description."
  type        = string
}

variable "deletion_window_in_days" {
  description = "Waiting period before deletion (7-30)."
  type        = number
  default     = 30
}

variable "account_id" {
  description = "AWS account id."
  type        = string
}

variable "partition" {
  description = "AWS partition (aws, aws-cn, aws-us-gov)."
  type        = string
  default     = "aws"
}

variable "region" {
  description = "AWS region."
  type        = string
}

variable "log_group_patterns" {
  description = "Names (or name patterns ending in *) of the log groups that may use the key, e.g. /hermes/<project>/<env>/*."
  type        = list(string)

  validation {
    condition     = length(var.log_group_patterns) > 0 && alltrue([for p in var.log_group_patterns : length(trimsuffix(p, "*")) > 1 && !strcontains(trimsuffix(p, "*"), "*")])
    error_message = "log_group_patterns must be non-empty; each entry is a log group name with at most a trailing * (never a bare *)."
  }
}

variable "sns_topic_arns" {
  description = "ARNs of the topics that deliver to CMK-encrypted SQS queues (empty = no SNS statement)."
  type        = list(string)
  default     = []
}

variable "tags" {
  description = "Tags."
  type        = map(string)
  default     = {}
}

locals {
  account_root = {
    Sid       = "EnableAccountIamPolicies"
    Effect    = "Allow"
    Principal = { AWS = "arn:${var.partition}:iam::${var.account_id}:root" }
    Action    = "kms:*"
    Resource  = "*"
  }

  # Only this environment's log groups (the encryption context names the log group).
  cloudwatch_logs = {
    Sid       = "AllowCloudWatchLogsInThisAccount"
    Effect    = "Allow"
    Principal = { Service = "logs.${var.region}.amazonaws.com" }
    Action    = ["kms:Encrypt*", "kms:Decrypt*", "kms:ReEncrypt*", "kms:GenerateDataKey*", "kms:Describe*"]
    Resource  = "*"
    Condition = {
      ArnLike = { "kms:EncryptionContext:aws:logs:arn" = [for p in var.log_group_patterns : "arn:${var.partition}:logs:${var.region}:${var.account_id}:log-group:${p}"] }
    }
  }

  # SNS delivering from this environment's topics to CMK-encrypted SQS queues.
  sns_to_sqs = {
    Sid       = "AllowSnsToEncryptedQueuesInThisAccount"
    Effect    = "Allow"
    Principal = { Service = "sns.amazonaws.com" }
    Action    = ["kms:GenerateDataKey*", "kms:Decrypt"]
    Resource  = "*"
    Condition = {
      StringEquals = { "aws:SourceAccount" = var.account_id }
      ArnLike      = { "aws:SourceArn" = var.sns_topic_arns }
    }
  }

  # CloudWatch alarm actions publishing to the CMK-encrypted alert topic, limited to alarms in
  # this account and region (confused-deputy protection). Not verified against real AWS (see
  # docs/providers/aws.md, known limitations).
  cloudwatch_alarms = {
    Sid       = "AllowCloudWatchAlarmsToEncryptedTopic"
    Effect    = "Allow"
    Principal = { Service = "cloudwatch.amazonaws.com" }
    Action    = ["kms:GenerateDataKey*", "kms:Decrypt"]
    Resource  = "*"
    Condition = {
      StringEquals = { "aws:SourceAccount" = var.account_id }
      ArnLike      = { "aws:SourceArn" = "arn:${var.partition}:cloudwatch:${var.region}:${var.account_id}:alarm:*" }
    }
  }

  policy = {
    Version = "2012-10-17"
    Statement = concat(
      [local.account_root, local.cloudwatch_logs],
      length(var.sns_topic_arns) > 0 ? [local.sns_to_sqs] : [],
      [local.cloudwatch_alarms],
    )
  }
}

resource "aws_kms_key" "this" {
  description             = var.description
  enable_key_rotation     = true
  deletion_window_in_days = var.deletion_window_in_days
  policy                  = jsonencode(local.policy)
  tags                    = var.tags
}

resource "aws_kms_alias" "this" {
  name          = var.alias_name
  target_key_id = aws_kms_key.this.key_id
}

output "key_arn" {
  description = "Key ARN."
  value       = aws_kms_key.this.arn
}

output "key_id" {
  description = "Key id."
  value       = aws_kms_key.this.key_id
}

output "policy" {
  description = "Key policy (JSON)."
  value       = jsonencode(local.policy)
}

output "alias_name" {
  description = "Alias name."
  value       = aws_kms_alias.this.name
}
