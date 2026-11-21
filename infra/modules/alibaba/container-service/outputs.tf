output "services" {
  description = "Service name -> {url, identity, app_id}. url is \"\" for private services."
  value = {
    for k, _ in local.services : k => {
      url      = contains(keys(local.public), k) ? "${local.https ? "https" : "http"}://${alicloud_sae_load_balancer_internet.this[k].internet_ip}" : ""
      identity = alicloud_ram_role.service[k].arn
      app_id   = alicloud_sae_application.this[k].id
    }
  }
}

output "policy_documents" {
  description = "Service name -> RAM policy JSON (services without access have no policy)."
  value       = local.policy_documents
}

output "trust_policy" {
  description = "Trust policy (JSON) shared by the workload RAM roles."
  value       = local.trust_policy
}

output "instance_sizes" {
  description = "Service name -> {cpu_millicores, memory_mb} after rounding up to SAE instance types."
  value       = { for k, p in local.size : k => { cpu_millicores = p[0], memory_mb = p[1] } }
}

output "namespace_id" {
  description = "SAE namespace ID."
  value       = alicloud_sae_namespace.this.id
}
