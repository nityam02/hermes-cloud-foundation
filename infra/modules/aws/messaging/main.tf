# Topic + subscription semantics on AWS: one SNS standard topic per spec topic and one SQS
# standard queue per spec subscription (raw message delivery). max_delivery_attempts > 0 adds a
# dead-letter queue with maxReceiveCount = max_delivery_attempts.

variable "topics" {
  description = "Spec topic name -> { name, subscriptions = spec subscription name -> settings }. Queue names are at most 76 characters (\"-dlq\" is appended for the DLQ)."
  type = map(object({
    name = string
    subscriptions = map(object({
      queue_name            = string
      ack_deadline_seconds  = number
      max_delivery_attempts = number
      retention_seconds     = number
    }))
  }))
  default = {}

  validation {
    condition     = alltrue(flatten([for t in values(var.topics) : [for s in values(t.subscriptions) : length(s.queue_name) <= 76]]))
    error_message = "queue_name must be at most 76 characters."
  }
}

variable "kms_enabled" {
  description = "True when kms_key_arn is set (a known boolean, so encryption settings are known at plan time)."
  type        = bool
  default     = false
}

variable "kms_key_arn" {
  description = "CMK for topics and queues; null uses alias/aws/sns for topics and SSE-SQS for queues."
  type        = string
  default     = null
}

variable "dlq_retention_seconds" {
  description = "Dead-letter queue retention (longer than the source queue so failed messages are not lost)."
  type        = number
  default     = 1209600
}

variable "alarms_enabled" {
  description = "Create a CloudWatch alarm per dead-letter queue."
  type        = bool
  default     = false
}

variable "alarm_topic_arn" {
  description = "SNS topic for alarm notifications."
  type        = string
  default     = null
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

variable "dns_suffix" {
  description = "Partition DNS suffix (amazonaws.com)."
  type        = string
  default     = "amazonaws.com"
}

variable "tags" {
  description = "Tags."
  type        = map(string)
  default     = {}
}

locals {
  subscriptions = merge({}, [
    for tk, t in var.topics : { for sk, s in t.subscriptions : sk => merge(s, { topic = tk }) }
  ]...)
  with_dlq = { for k, s in local.subscriptions : k => s if s.max_delivery_attempts > 0 }

  topic_arns = { for k, t in var.topics : k => "arn:${var.partition}:sns:${var.region}:${var.account_id}:${t.name}" }
  queue_arns = { for k, s in local.subscriptions : k => "arn:${var.partition}:sqs:${var.region}:${var.account_id}:${s.queue_name}" }
  dlq_arns   = { for k, s in local.with_dlq : k => "arn:${var.partition}:sqs:${var.region}:${var.account_id}:${s.queue_name}-dlq" }
}

resource "aws_sns_topic" "this" {
  for_each = var.topics

  name              = each.value.name
  kms_master_key_id = var.kms_enabled ? var.kms_key_arn : "alias/aws/sns"
  tags              = var.tags
}

resource "aws_sns_topic_policy" "this" {
  for_each = var.topics

  arn = "arn:${var.partition}:sns:${var.region}:${var.account_id}:${aws_sns_topic.this[each.key].name}"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "AccountOwnerManage"
        Effect    = "Allow"
        Principal = { AWS = "arn:${var.partition}:iam::${var.account_id}:root" }
        Action    = ["sns:GetTopicAttributes", "sns:SetTopicAttributes", "sns:AddPermission", "sns:RemovePermission", "sns:DeleteTopic", "sns:Subscribe", "sns:ListSubscriptionsByTopic", "sns:Publish"]
        Resource  = local.topic_arns[each.key]
      },
      {
        Sid       = "DenyInsecureTransport"
        Effect    = "Deny"
        Principal = "*"
        Action    = "sns:Publish"
        Resource  = local.topic_arns[each.key]
        Condition = { Bool = { "aws:SecureTransport" = "false" } }
      },
    ]
  })
}

resource "aws_sqs_queue" "dlq" {
  for_each = local.with_dlq

  name                              = "${each.value.queue_name}-dlq"
  message_retention_seconds         = var.dlq_retention_seconds
  kms_master_key_id                 = var.kms_key_arn
  kms_data_key_reuse_period_seconds = var.kms_enabled ? 300 : null
  sqs_managed_sse_enabled           = var.kms_enabled ? null : true
  tags                              = var.tags
}

resource "aws_sqs_queue" "this" {
  for_each = local.subscriptions

  name                              = each.value.queue_name
  visibility_timeout_seconds        = each.value.ack_deadline_seconds
  message_retention_seconds         = each.value.retention_seconds
  receive_wait_time_seconds         = 20
  kms_master_key_id                 = var.kms_key_arn
  kms_data_key_reuse_period_seconds = var.kms_enabled ? 300 : null
  sqs_managed_sse_enabled           = var.kms_enabled ? null : true
  redrive_policy = each.value.max_delivery_attempts > 0 ? jsonencode({
    deadLetterTargetArn = local.dlq_arns[each.key]
    maxReceiveCount     = each.value.max_delivery_attempts
  }) : null
  tags = var.tags

  depends_on = [aws_sqs_queue.dlq]
}

# Only the source queue may use its DLQ.
resource "aws_sqs_queue_redrive_allow_policy" "dlq" {
  for_each = local.with_dlq

  queue_url = aws_sqs_queue.dlq[each.key].id
  redrive_allow_policy = jsonencode({
    redrivePermission = "byQueue"
    sourceQueueArns   = [local.queue_arns[each.key]]
  })
}

# Only the subscribed topic may send to the queue; non-TLS access is denied.
resource "aws_sqs_queue_policy" "this" {
  for_each = local.subscriptions

  queue_url = aws_sqs_queue.this[each.key].id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "AllowSubscribedTopicOnly"
        Effect    = "Allow"
        Principal = { Service = "sns.amazonaws.com" }
        Action    = "sqs:SendMessage"
        Resource  = local.queue_arns[each.key]
        Condition = { ArnEquals = { "aws:SourceArn" = local.topic_arns[each.value.topic] } }
      },
      {
        Sid       = "DenyInsecureTransport"
        Effect    = "Deny"
        Principal = "*"
        Action    = "sqs:*"
        Resource  = local.queue_arns[each.key]
        Condition = { Bool = { "aws:SecureTransport" = "false" } }
      },
    ]
  })
}

resource "aws_sns_topic_subscription" "this" {
  for_each = local.subscriptions

  topic_arn            = "arn:${var.partition}:sns:${var.region}:${var.account_id}:${aws_sns_topic.this[each.value.topic].name}"
  protocol             = "sqs"
  endpoint             = local.queue_arns[each.key]
  raw_message_delivery = true

  depends_on = [aws_sqs_queue_policy.this, aws_sqs_queue.this]
}

resource "aws_cloudwatch_metric_alarm" "dlq_not_empty" {
  for_each = var.alarms_enabled ? local.with_dlq : {}

  alarm_name          = "${each.value.queue_name}-dlq-not-empty"
  alarm_description   = "Messages of subscription \"${each.key}\" were dead-lettered after ${each.value.max_delivery_attempts} delivery attempts."
  namespace           = "AWS/SQS"
  metric_name         = "ApproximateNumberOfMessagesVisible"
  dimensions          = { QueueName = aws_sqs_queue.dlq[each.key].name }
  statistic           = "Maximum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 1
  comparison_operator = "GreaterThanOrEqualToThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [var.alarm_topic_arn]
  ok_actions          = [var.alarm_topic_arn]
  tags                = var.tags
}

output "topics" {
  description = "Spec topic name -> { arn, name, subscriptions = spec subscription -> { queue_name, arn, url, dlq_arn } }. ARNs and URLs are built from names (known at plan time)."
  value = {
    for tk, t in var.topics : tk => {
      arn  = local.topic_arns[tk]
      name = aws_sns_topic.this[tk].name
      subscriptions = {
        for sk, s in t.subscriptions : sk => {
          queue_name = aws_sqs_queue.this[sk].name
          arn        = local.queue_arns[sk]
          url        = "https://sqs.${var.region}.${var.dns_suffix}/${var.account_id}/${aws_sqs_queue.this[sk].name}"
          dlq_arn    = lookup(local.dlq_arns, sk, null)
        }
      }
    }
  }
}

output "subscriptions" {
  description = "Spec subscription name -> { queue_name, arn, url } (flat view for access grants)."
  value = {
    for sk, s in local.subscriptions : sk => {
      queue_name = aws_sqs_queue.this[sk].name
      arn        = local.queue_arns[sk]
      url        = "https://sqs.${var.region}.${var.dns_suffix}/${var.account_id}/${aws_sqs_queue.this[sk].name}"
    }
  }
}
