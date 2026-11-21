# -----------------------------------------------------------------------------
# One Cloud Run (v2) service.
#
# Resource rounding (Cloud Run rules, documented in docs/providers/gcp.md):
#   - CPU below 1 vCPU forces concurrency 1 and other restrictions, so CPU is
#     rounded UP to the next Cloud Run value in [1, 2, 4, 6, 8];
#   - memory above 4/8/16/24 GiB needs at least 2/4/6/8 vCPU (CPU is raised);
#   - 4 vCPU needs >= 2 GiB and 6+ vCPU >= 4 GiB (memory is raised).
# CPU is allocated only during requests (cheapest), except when the collector
# sidecar runs: it exports in the background and needs CPU between requests.
# -----------------------------------------------------------------------------

locals {
  cpu_values         = [1, 2, 4, 6, 8]
  min_cpu_for_memory = var.memory_mb > 24576 ? 8 : var.memory_mb > 16384 ? 6 : var.memory_mb > 8192 ? 4 : var.memory_mb > 4096 ? 2 : 1
  cpu                = [for c in local.cpu_values : c if c >= max(var.cpu, local.min_cpu_for_memory)][0]
  memory_mb          = max(var.memory_mb, local.cpu >= 6 ? 4096 : local.cpu >= 4 ? 2048 : 512)

  collector_config = yamlencode({
    receivers = {
      otlp = {
        protocols = {
          grpc = { endpoint = "localhost:4317" }
          http = { endpoint = "localhost:4318" }
        }
      }
    }
    processors = {
      memory_limiter    = { check_interval = "1s", limit_mib = 400, spike_limit_mib = 100 }
      resourcedetection = { detectors = ["env", "gcp"], timeout = "10s", override = false }
      batch             = { send_batch_size = 200, timeout = "5s" }
    }
    exporters = {
      # Project and credentials come from the Cloud Run metadata server.
      googlecloud = {}
    }
    service = {
      telemetry = { logs = { level = "warn" } }
      pipelines = merge(
        {
          traces = {
            receivers  = ["otlp"]
            processors = ["memory_limiter", "resourcedetection", "batch"]
            exporters  = ["googlecloud"]
          }
        },
        var.otel_collector.metrics ? {
          metrics = {
            receivers  = ["otlp"]
            processors = ["memory_limiter", "resourcedetection", "batch"]
            exporters  = ["googlecloud"]
          }
        } : {},
      )
    }
  })
}

resource "google_cloud_run_v2_service" "this" {
  name                = var.name
  location            = var.region
  description         = "Hermes containerized service."
  labels              = var.labels
  ingress             = var.public ? "INGRESS_TRAFFIC_ALL" : "INGRESS_TRAFFIC_INTERNAL_ONLY"
  deletion_protection = var.deletion_protection

  template {
    service_account       = var.service_account_email
    execution_environment = "EXECUTION_ENVIRONMENT_GEN2"

    scaling {
      min_instance_count = var.min_instances
      max_instance_count = var.max_instances
    }

    vpc_access {
      egress = var.vpc_egress
      network_interfaces {
        network    = var.network
        subnetwork = var.subnetwork
      }
    }

    containers {
      name    = "app"
      image   = var.image
      command = length(var.command) > 0 ? var.command : null

      ports {
        name           = "http1"
        container_port = var.port
      }

      resources {
        limits = {
          cpu    = tostring(local.cpu)
          memory = "${local.memory_mb}Mi"
        }
        cpu_idle          = !var.otel_collector.enabled
        startup_cpu_boost = true
      }

      dynamic "env" {
        for_each = var.env
        content {
          name  = env.key
          value = env.value
        }
      }

      dynamic "env" {
        for_each = var.secret_env
        content {
          name = env.key
          value_source {
            secret_key_ref {
              secret  = env.value
              version = "latest"
            }
          }
        }
      }

      startup_probe {
        initial_delay_seconds = 0
        timeout_seconds       = 3
        period_seconds        = 10
        failure_threshold     = 6
        http_get {
          path = var.health_check_path
          port = var.port
        }
      }

      liveness_probe {
        timeout_seconds   = 3
        period_seconds    = 30
        failure_threshold = 3
        http_get {
          path = var.health_check_path
          port = var.port
        }
      }
    }

    dynamic "containers" {
      for_each = var.otel_collector.enabled ? [var.otel_collector.image] : []
      content {
        name  = "otel-collector"
        image = containers.value
        # The collector reads its YAML configuration from an environment variable.
        args = ["--config=env:OTELCOL_CONFIG"]

        env {
          name  = "OTELCOL_CONFIG"
          value = local.collector_config
        }

        resources {
          limits = {
            cpu    = "500m"
            memory = "512Mi"
          }
          cpu_idle = false
        }
      }
    }
  }
}

resource "google_cloud_run_v2_service_iam_member" "public_invoker" {
  #checkov:skip=CKV_GCP_102:Created only for services the spec marks public (public HTTPS API); private services get no invoker binding.
  count = var.public ? 1 : 0

  name     = google_cloud_run_v2_service.this.name
  location = google_cloud_run_v2_service.this.location
  role     = "roles/run.invoker"
  member   = "allUsers"
}
