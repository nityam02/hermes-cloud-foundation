# Mock-provider tests: retention, residency and dead-letter wiring. No cloud calls.

mock_provider "google" {}

variables {
  region         = "europe-north1"
  project_number = "123456789012"
  topics         = { telemetry = { name = "demo-dev-telemetry", retention_hours = 24 } }
  subscriptions = {
    ingest = {
      name                          = "demo-dev-ingest"
      topic                         = "telemetry"
      ack_deadline_seconds          = 30
      max_delivery_attempts         = 5
      retention_hours               = 24
      dead_letter_topic_name        = "demo-dev-ingest-dlq"
      dead_letter_subscription_name = "demo-dev-ingest-dlq"
    }
    archive = {
      name                          = "demo-dev-archive"
      topic                         = "telemetry"
      ack_deadline_seconds          = 60
      max_delivery_attempts         = 0
      retention_hours               = 24
      dead_letter_topic_name        = ""
      dead_letter_subscription_name = ""
    }
  }
}

run "dead_letter_only_when_attempts_positive" {
  command = apply

  assert {
    condition     = google_pubsub_subscription.this["ingest"].dead_letter_policy[0].max_delivery_attempts == 5 && google_pubsub_subscription.this["ingest"].dead_letter_policy[0].dead_letter_topic == google_pubsub_topic.dead_letter["ingest"].id
    error_message = "max_delivery_attempts > 0 must dead-letter to the dedicated topic."
  }

  assert {
    condition     = length(google_pubsub_subscription.this["archive"].dead_letter_policy) == 0 && toset(keys(google_pubsub_topic.dead_letter)) == toset(["ingest"])
    error_message = "max_delivery_attempts = 0 must not create a dead-letter policy or topic."
  }

  assert {
    condition     = google_pubsub_subscription.dead_letter["ingest"].topic == google_pubsub_topic.dead_letter["ingest"].id
    error_message = "Dead-lettered messages must be retained by a subscription on the dead-letter topic."
  }

  assert {
    condition     = google_pubsub_topic_iam_member.dead_letter_publisher["ingest"].member == "serviceAccount:service-123456789012@gcp-sa-pubsub.iam.gserviceaccount.com" && google_pubsub_topic_iam_member.dead_letter_publisher["ingest"].role == "roles/pubsub.publisher" && google_pubsub_subscription_iam_member.dead_letter_subscriber["ingest"].role == "roles/pubsub.subscriber"
    error_message = "The Pub/Sub service agent needs publisher on the dead-letter topic and subscriber on the source subscription."
  }

  assert {
    condition     = google_pubsub_topic.this["telemetry"].message_retention_duration == "86400s" && google_pubsub_subscription.this["ingest"].message_retention_duration == "86400s" && google_pubsub_subscription.this["ingest"].expiration_policy[0].ttl == ""
    error_message = "Retention must follow retention_hours and subscriptions must never expire."
  }

  assert {
    condition     = contains(google_pubsub_topic.this["telemetry"].message_storage_policy[0].allowed_persistence_regions, "europe-north1") && length(google_pubsub_topic.this["telemetry"].message_storage_policy[0].allowed_persistence_regions) == 1
    error_message = "Messages must persist only in the environment region."
  }
}
