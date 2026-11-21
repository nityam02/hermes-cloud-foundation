# Simple Message Queue (MNS) module tests (mock provider only; no cloud API is called).

mock_provider "alicloud" {}

variables {
  prefix = "shop-dev"
  topics = [{
    name            = "telemetry"
    retention_hours = 24
    subscriptions = [
      { name = "telemetry-ingest", ack_deadline_seconds = 30, max_delivery_attempts = 5 },
      { name = "telemetry-archive", ack_deadline_seconds = 60, max_delivery_attempts = 0 },
    ]
  }]
}

run "dead_letter_only_when_attempts_positive" {
  command = apply

  assert {
    condition     = sort(keys(alicloud_message_service_queue.dead_letter)) == sort(["telemetry-ingest"])
    error_message = "Only subscriptions with max_delivery_attempts > 0 get a dead-letter queue."
  }

  assert {
    condition = (
      one(alicloud_message_service_queue.this["telemetry-ingest"].dlq_policy).dead_letter_target_queue == "shop-dev-telemetry-ingest-dlq" &&
      tostring(one(alicloud_message_service_queue.this["telemetry-ingest"].dlq_policy).max_receive_count) == "5" &&
      one(alicloud_message_service_queue.this["telemetry-ingest"].dlq_policy).enabled == true
    )
    error_message = "The ingest queue must dead-letter after 5 receives."
  }

  assert {
    condition     = length(alicloud_message_service_queue.this["telemetry-archive"].dlq_policy) == 0
    error_message = "max_delivery_attempts = 0 must not configure a dead-letter policy."
  }

  assert {
    condition     = output.dead_letter_queues == { "telemetry-archive" = "", "telemetry-ingest" = "shop-dev-telemetry-ingest-dlq" }
    error_message = "dead_letter_queues must report the DLQ per subscription."
  }
}

run "queues_follow_topic_retention_and_ack_deadline" {
  command = apply

  assert {
    condition     = alicloud_message_service_queue.this["telemetry-ingest"].message_retention_period == 86400 && alicloud_message_service_queue.this["telemetry-archive"].visibility_timeout == 60
    error_message = "Retention (hours -> seconds) and ack deadline (visibility timeout) must map onto the queue."
  }

  assert {
    condition = alltrue([for k, s in alicloud_message_service_subscription.this : (
      s.push_type == "queue" && s.endpoint == alicloud_message_service_queue.this[k].queue_name && s.topic_name == "shop-dev-telemetry"
    )])
    error_message = "Each subscription must push from the topic to its own queue."
  }

  assert {
    condition     = output.topics["telemetry"].id == "shop-dev-telemetry" && output.topics["telemetry"].subscriptions["telemetry-ingest"] == "shop-dev-telemetry-ingest"
    error_message = "Bindings must expose native topic and queue names."
  }
}
