mock_provider "aws" {}

variables {
  account_id     = "123456789012"
  region         = "eu-north-1"
  alarms_enabled = true
  # Built from the name in real use; a literal here keeps the mock run independent.
  alarm_topic_arn = "arn:aws:sns:eu-north-1:123456789012:shop-dev-alerts"
  topics = {
    telemetry = {
      name = "shop-dev-telemetry"
      subscriptions = {
        telemetry-ingest  = { queue_name = "shop-dev-telemetry-ingest", ack_deadline_seconds = 30, max_delivery_attempts = 5, retention_seconds = 604800 }
        telemetry-archive = { queue_name = "shop-dev-telemetry-archive", ack_deadline_seconds = 60, max_delivery_attempts = 0, retention_seconds = 604800 }
      }
    }
  }
}

run "dead_letter_wiring" {
  command = apply

  assert {
    condition     = tolist(keys(aws_sqs_queue.dlq)) == tolist(["telemetry-ingest"]) && jsondecode(aws_sqs_queue.this["telemetry-ingest"].redrive_policy).maxReceiveCount == 5
    error_message = "max_delivery_attempts > 0 must create a DLQ with maxReceiveCount = attempts."
  }

  assert {
    condition     = jsondecode(aws_sqs_queue.this["telemetry-ingest"].redrive_policy).deadLetterTargetArn == "arn:aws:sqs:eu-north-1:123456789012:shop-dev-telemetry-ingest-dlq"
    error_message = "The redrive policy must target the subscription's own DLQ."
  }

  assert {
    # redrive_policy is Optional+Computed, so mocks fill it in; assert on the DLQ set and the binding instead.
    condition     = !contains(keys(aws_sqs_queue.dlq), "telemetry-archive") && output.topics["telemetry"].subscriptions["telemetry-archive"].dlq_arn == null
    error_message = "max_delivery_attempts = 0 must not create a DLQ or redrive policy."
  }

  assert {
    condition     = tolist(jsondecode(aws_sqs_queue_redrive_allow_policy.dlq["telemetry-ingest"].redrive_allow_policy).sourceQueueArns) == tolist(["arn:aws:sqs:eu-north-1:123456789012:shop-dev-telemetry-ingest"])
    error_message = "Only the source queue may redrive into its DLQ."
  }

  assert {
    condition     = tolist(keys(aws_cloudwatch_metric_alarm.dlq_not_empty)) == tolist(["telemetry-ingest"])
    error_message = "Each DLQ gets a not-empty alarm when alarms are enabled."
  }

  assert {
    condition     = aws_sqs_queue.this["telemetry-archive"].visibility_timeout_seconds == 60 && aws_sqs_queue.this["telemetry-archive"].message_retention_seconds == 604800
    error_message = "Visibility timeout = ack deadline; retention = topic retention."
  }

  assert {
    condition     = alltrue([for s in aws_sns_topic_subscription.this : s.raw_message_delivery && s.protocol == "sqs"])
    error_message = "Subscriptions must use raw SQS delivery."
  }

  assert {
    condition = alltrue([for k, p in aws_sqs_queue_policy.this : tolist([
      for s in jsondecode(p.policy).Statement : s.Condition.ArnEquals["aws:SourceArn"] if s.Effect == "Allow"
    ]) == tolist(["arn:aws:sns:eu-north-1:123456789012:shop-dev-telemetry"])])
    error_message = "Queue policies must allow only the subscribed topic."
  }

  assert {
    condition     = aws_sns_topic.this["telemetry"].kms_master_key_id == "alias/aws/sns" && alltrue([for q in aws_sqs_queue.this : q.sqs_managed_sse_enabled])
    error_message = "Without a CMK topics use aws/sns and queues use SSE-SQS."
  }

  assert {
    condition     = output.topics["telemetry"].subscriptions["telemetry-ingest"].url == "https://sqs.eu-north-1.amazonaws.com/123456789012/shop-dev-telemetry-ingest"
    error_message = "Subscription bindings expose the queue URL."
  }
}

run "cmk_encryption" {
  command = apply

  variables {
    kms_enabled = true
    kms_key_arn = "arn:aws:kms:eu-north-1:123456789012:key/00000000-0000-4000-8000-000000000000"
  }

  assert {
    condition     = aws_sns_topic.this["telemetry"].kms_master_key_id == var.kms_key_arn && alltrue([for q in aws_sqs_queue.this : q.kms_master_key_id == var.kms_key_arn]) && alltrue([for q in aws_sqs_queue.dlq : q.kms_master_key_id == var.kms_key_arn])
    error_message = "With a CMK the topic, queues and DLQs use it."
  }
}

run "no_alarms_when_disabled" {
  command = apply

  variables {
    alarms_enabled = false
  }

  assert {
    condition     = length(aws_cloudwatch_metric_alarm.dlq_not_empty) == 0
    error_message = "No DLQ alarms when alarms are disabled."
  }
}
