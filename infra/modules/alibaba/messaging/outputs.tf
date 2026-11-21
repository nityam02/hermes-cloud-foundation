output "topics" {
  description = "Spec topic name -> {id = native topic name, subscriptions = {spec subscription -> native queue name}}."
  value = {
    for t, topic in alicloud_message_service_topic.this : t => {
      id = topic.topic_name
      subscriptions = {
        for s, sub in local.subscriptions : s => alicloud_message_service_queue.this[s].queue_name if sub.topic == t
      }
    }
  }
}

output "topic_names" {
  description = "Spec topic name -> native topic name."
  value       = { for t, topic in alicloud_message_service_topic.this : t => topic.topic_name }
}

output "queue_names" {
  description = "Spec subscription name -> native queue name."
  value       = { for s, q in alicloud_message_service_queue.this : s => q.queue_name }
}

output "dead_letter_queues" {
  description = "Spec subscription name -> native dead-letter queue name (\"\" when max_delivery_attempts = 0)."
  value       = { for s, _ in local.subscriptions : s => try(alicloud_message_service_queue.dead_letter[s].queue_name, "") }
}
