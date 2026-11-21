# -----------------------------------------------------------------------------
# Pub/Sub topics with one pull subscription per spec subscription.
#   - messages persist only in the environment region (message_storage_policy)
#   - topic retention = retention_hours (replay); subscription retention = the
#     same value (Pub/Sub subscription maximum is 7 days = the contract maximum)
#   - subscriptions never expire (expiration_policy ttl = "")
#   - exponential retry backoff 10s..600s
#   - max_delivery_attempts > 0: a dedicated dead-letter topic + a subscription on
#     it (messages published to a topic without subscriptions are lost) + the
#     IAM the Pub/Sub service agent needs (publisher on the dead-letter topic,
#     subscriber on the source subscription). 0: no dead-letter policy.
# -----------------------------------------------------------------------------

variable "topics" {
  description = "Spec topic name -> native name and retention_hours."
  type = map(object({
    name            = string
    retention_hours = number
  }))
  default = {}
}

variable "subscriptions" {
  description = "Spec subscription name -> native names, spec topic, delivery settings."
  type = map(object({
    name                          = string
    topic                         = string
    ack_deadline_seconds          = number
    max_delivery_attempts         = number
    retention_hours               = number
    dead_letter_topic_name        = string
    dead_letter_subscription_name = string
  }))
  default = {}
}

variable "region" {
  description = "The only region where message data may be persisted."
  type        = string
}

variable "project_number" {
  description = "Project number (builds the Pub/Sub service agent identity)."
  type        = string
}

variable "enable_cmek" {
  description = "Encrypt topics with var.kms_key (known at plan time)."
  type        = bool
  default     = false
}

variable "kms_key" {
  description = "Crypto key id used when enable_cmek."
  type        = string
  default     = ""
}

variable "labels" {
  description = "Labels for topics and subscriptions."
  type        = map(string)
  default     = {}
}

locals {
  pubsub_agent = "serviceAccount:service-${var.project_number}@gcp-sa-pubsub.iam.gserviceaccount.com"
  dead_letter  = { for k, s in var.subscriptions : k => s if s.max_delivery_attempts > 0 }
}

resource "google_pubsub_topic" "this" {
  for_each = var.topics

  name                       = each.value.name
  labels                     = var.labels
  message_retention_duration = "${each.value.retention_hours * 3600}s"
  kms_key_name               = var.enable_cmek ? var.kms_key : null

  message_storage_policy {
    allowed_persistence_regions = [var.region]
  }
}

resource "google_pubsub_topic" "dead_letter" {
  for_each = local.dead_letter

  name         = each.value.dead_letter_topic_name
  labels       = var.labels
  kms_key_name = var.enable_cmek ? var.kms_key : null

  message_storage_policy {
    allowed_persistence_regions = [var.region]
  }
}

resource "google_pubsub_subscription" "dead_letter" {
  for_each = local.dead_letter

  name                       = each.value.dead_letter_subscription_name
  topic                      = google_pubsub_topic.dead_letter[each.key].id
  labels                     = var.labels
  ack_deadline_seconds       = 60
  message_retention_duration = "604800s" # 7 days, the Pub/Sub maximum
  retain_acked_messages      = false

  expiration_policy {
    ttl = ""
  }
}

resource "google_pubsub_subscription" "this" {
  for_each = var.subscriptions

  name                       = each.value.name
  topic                      = google_pubsub_topic.this[each.value.topic].id
  labels                     = var.labels
  ack_deadline_seconds       = each.value.ack_deadline_seconds
  message_retention_duration = "${each.value.retention_hours * 3600}s"
  retain_acked_messages      = false

  expiration_policy {
    ttl = ""
  }

  retry_policy {
    minimum_backoff = "10s"
    maximum_backoff = "600s"
  }

  dynamic "dead_letter_policy" {
    for_each = each.value.max_delivery_attempts > 0 ? [each.value.max_delivery_attempts] : []
    content {
      dead_letter_topic     = google_pubsub_topic.dead_letter[each.key].id
      max_delivery_attempts = dead_letter_policy.value
    }
  }
}

resource "google_pubsub_topic_iam_member" "dead_letter_publisher" {
  for_each = local.dead_letter

  topic  = google_pubsub_topic.dead_letter[each.key].id
  role   = "roles/pubsub.publisher"
  member = local.pubsub_agent
}

resource "google_pubsub_subscription_iam_member" "dead_letter_subscriber" {
  for_each = local.dead_letter

  subscription = google_pubsub_subscription.this[each.key].id
  role         = "roles/pubsub.subscriber"
  member       = local.pubsub_agent
}

output "topic_ids" {
  description = "Spec topic name -> projects/<p>/topics/<name>."
  value       = { for k, t in google_pubsub_topic.this : k => t.id }
}

output "subscription_ids" {
  description = "Spec subscription name -> projects/<p>/subscriptions/<name>."
  value       = { for k, s in google_pubsub_subscription.this : k => s.id }
}

output "dead_letter_topic_ids" {
  description = "Spec subscription name -> dead-letter topic id (only subscriptions with a dead-letter policy)."
  value       = { for k, t in google_pubsub_topic.dead_letter : k => t.id }
}
