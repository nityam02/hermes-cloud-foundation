output "network_id" {
  description = "Network id (projects/<p>/global/networks/<name>)."
  value       = google_compute_network.this.id
}

output "network_name" {
  description = "Network name."
  value       = google_compute_network.this.name
}

output "subnet_id" {
  description = "Subnet id."
  value       = google_compute_subnetwork.this.id
}

output "subnet_name" {
  description = "Subnet name."
  value       = google_compute_subnetwork.this.name
}

output "subnet_cidr" {
  description = "Subnet primary range."
  value       = local.subnet_cidr
}

output "psa_cidr" {
  description = "Private services access range (\"\" when not reserved)."
  value       = var.private_services_access ? local.psa_cidr : ""
}

output "private_services_connection" {
  description = "Private services access connection id (\"\" when not created); dependents wait on it."
  value       = var.private_services_access ? google_service_networking_connection.psa[0].id : ""
}
