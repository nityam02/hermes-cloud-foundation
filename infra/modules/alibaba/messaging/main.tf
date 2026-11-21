# Messaging capability: Simple Message Queue (formerly MNS) topics; each subscription is a
# durable queue that the topic pushes to. When max_delivery_attempts > 0 the queue gets a
# dead-letter queue and moves a message there after that many receives without delete.

locals {
  tags = merge(var.tags, { "hermes-component" = "messaging" })

  topics = { for t in var.topics : t.name => t }

  subscriptions = merge([
    for t in var.topics : {
      for s in t.subscriptions : s.name => merge(s, {
        topic           = t.name
        retention_hours = t.retention_hours
      })
    }
  ]...)

  dead_lettered = { for k, s in local.subscriptions : k => s if s.max_delivery_attempts > 0 }

  # Server-side encryption must first be enabled for the account through a support ticket,
  # so it is opt-in. With a customer-managed key, messages are encrypted with that key.
  sse = var.sse_enabled ? {
    enable_sse    = true
    sse_type      = var.kms_key_id == null ? "SMQ" : "KMS"
    sse_algorithm = "AES-256-GCM"
    kms_key_id    = var.kms_key_id
    } : {
    enable_sse    = null
    sse_type      = null
    sse_algorithm = null
    kms_key_id    = null
  }
}

module "names" {
  source = "../naming"
  names = merge(
    { for k, _ in local.topics : "topic/${k}" => { parts = [var.prefix, k], max_length = 120 } },
    { for k, _ in local.subscriptions : "queue/${k}" => { parts = [var.prefix, k], max_length = 120 } },
    { for k, _ in local.dead_lettered : "dlq/${k}" => { parts = [var.prefix, k, "dlq"], max_length = 120 } },
  )
}

resource "alicloud_message_service_topic" "this" {
  for_each = local.topics

  topic_name       = module.names.names["topic/${each.key}"]
  max_message_size = 65536
  enable_logging   = false
  enable_sse       = local.sse.enable_sse
  sse_type         = local.sse.sse_type
  sse_algorithm    = local.sse.sse_algorithm
  kms_key_id       = local.sse.kms_key_id
  tags             = local.tags
}

resource "alicloud_message_service_queue" "dead_letter" {
  for_each = local.dead_lettered

  queue_name               = module.names.names["dlq/${each.key}"]
  message_retention_period = 604800 # 7 days, the queue maximum
  visibility_timeout       = each.value.ack_deadline_seconds
  maximum_message_size     = 65536
  enable_sse               = local.sse.enable_sse
  sse_type                 = local.sse.sse_type
  sse_algorithm            = local.sse.sse_algorithm
  kms_key_id               = local.sse.kms_key_id
  tags                     = local.tags
}

resource "alicloud_message_service_queue" "this" {
  for_each = local.subscriptions

  queue_name               = module.names.names["queue/${each.key}"]
  message_retention_period = each.value.retention_hours * 3600
  visibility_timeout       = each.value.ack_deadline_seconds
  maximum_message_size     = 65536
  enable_sse               = local.sse.enable_sse
  sse_type                 = local.sse.sse_type
  sse_algorithm            = local.sse.sse_algorithm
  kms_key_id               = local.sse.kms_key_id
  tags                     = local.tags

  dynamic "dlq_policy" {
    for_each = each.value.max_delivery_attempts > 0 ? [each.value.max_delivery_attempts] : []
    content {
      enabled                  = true
      dead_letter_target_queue = alicloud_message_service_queue.dead_letter[each.key].queue_name
      max_receive_count        = dlq_policy.value
    }
  }
}

resource "alicloud_message_service_subscription" "this" {
  for_each = local.subscriptions

  topic_name            = alicloud_message_service_topic.this[each.value.topic].topic_name
  subscription_name     = each.key
  push_type             = "queue"
  endpoint              = alicloud_message_service_queue.this[each.key].queue_name
  notify_content_format = "SIMPLIFIED" # deliver the raw message body
}
