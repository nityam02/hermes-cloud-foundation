output "name" {
  description = "Native service name."
  value       = google_cloud_run_v2_service.this.name
}

output "id" {
  description = "Service id."
  value       = google_cloud_run_v2_service.this.id
}

output "uri" {
  description = "run.app URI (reachable from the internet only when public)."
  value       = google_cloud_run_v2_service.this.uri
}

output "url" {
  description = "Public HTTPS URL, or \"\" for internal-only services."
  value       = var.public ? google_cloud_run_v2_service.this.uri : ""
}

output "effective_cpu" {
  description = "vCPU after Cloud Run rounding."
  value       = local.cpu
}

output "effective_memory_mb" {
  description = "Memory (MiB) after Cloud Run rounding."
  value       = local.memory_mb
}

output "ingress" {
  description = "Cloud Run ingress setting."
  value       = google_cloud_run_v2_service.this.ingress
}

output "deletion_protection" {
  description = "Cloud Run deletion protection."
  value       = google_cloud_run_v2_service.this.deletion_protection
}

output "containers" {
  description = "Container names (app, plus otel-collector when tracing)."
  value       = [for c in google_cloud_run_v2_service.this.template[0].containers : c.name]
}
