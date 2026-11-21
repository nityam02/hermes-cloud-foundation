# Mock-provider tests: wiring and Cloud Run resource rounding. No cloud calls.

mock_provider "google" {}

variables {
  name                  = "demo-dev-api"
  region                = "europe-north1"
  service_account_email = "demo-dev-api@example-project.iam.gserviceaccount.com"
  image                 = "ghcr.io/example/api:1.0.0"
  network               = "demo-dev-vpc"
  subnetwork            = "demo-dev-subnet"
}

run "fractional_cpu_rounds_up_to_one" {
  command = apply

  variables {
    cpu       = 0.25
    memory_mb = 512
  }

  assert {
    condition     = output.effective_cpu == 1 && output.effective_memory_mb == 512
    error_message = "0.25 vCPU must round up to 1 vCPU and keep 512 MiB."
  }

  assert {
    condition     = google_cloud_run_v2_service.this.template[0].containers[0].resources[0].limits["cpu"] == "1" && google_cloud_run_v2_service.this.template[0].containers[0].resources[0].limits["memory"] == "512Mi"
    error_message = "The container limits must use the rounded values."
  }
}

run "large_memory_raises_cpu" {
  command = apply

  variables {
    cpu       = 1
    memory_mb = 12288
  }

  assert {
    condition     = output.effective_cpu == 4 && output.effective_memory_mb == 12288
    error_message = "12 GiB requires 4 vCPU on Cloud Run."
  }
}

run "four_cpu_raises_memory" {
  command = apply

  variables {
    cpu       = 4
    memory_mb = 1024
  }

  assert {
    condition     = output.effective_cpu == 4 && output.effective_memory_mb == 2048
    error_message = "4 vCPU requires at least 2 GiB."
  }
}

run "private_service_has_no_public_invoker" {
  command = apply

  assert {
    condition     = google_cloud_run_v2_service.this.ingress == "INGRESS_TRAFFIC_INTERNAL_ONLY" && length(google_cloud_run_v2_service_iam_member.public_invoker) == 0
    error_message = "Private services must be internal-only without an allUsers invoker."
  }

  assert {
    condition     = output.url == ""
    error_message = "Private services must expose an empty URL."
  }

  assert {
    condition     = length(google_cloud_run_v2_service.this.template[0].containers) == 1 && google_cloud_run_v2_service.this.template[0].containers[0].resources[0].cpu_idle == true
    error_message = "Without the collector there is one container and CPU is request-based."
  }
}

run "public_service_with_collector_and_secrets" {
  command = apply

  variables {
    public              = true
    deletion_protection = true
    env                 = { HERMES_SERVICE = "api" }
    secret_env          = { MAPS_API_KEY = "demo-dev-maps-api-key" }
    otel_collector      = { enabled = true, image = "otel/opentelemetry-collector-contrib:0.130.0", metrics = true }
  }

  assert {
    condition     = google_cloud_run_v2_service.this.ingress == "INGRESS_TRAFFIC_ALL" && google_cloud_run_v2_service_iam_member.public_invoker[0].member == "allUsers"
    error_message = "Public services must allow all ingress and unauthenticated invocation."
  }

  assert {
    condition     = google_cloud_run_v2_service.this.deletion_protection == true
    error_message = "deletion_protection must be passed through."
  }

  assert {
    condition = anytrue([
      for e in google_cloud_run_v2_service.this.template[0].containers[0].env :
      e.name == "MAPS_API_KEY" && e.value_source[0].secret_key_ref[0].secret == "demo-dev-maps-api-key" && e.value_source[0].secret_key_ref[0].version == "latest"
    ])
    error_message = "Secret env must be injected through secret_key_ref, never as a plain value."
  }

  assert {
    condition     = length(google_cloud_run_v2_service.this.template[0].containers) == 2 && google_cloud_run_v2_service.this.template[0].containers[1].name == "otel-collector"
    error_message = "Tracing must add the OpenTelemetry Collector sidecar."
  }

  assert {
    condition     = anytrue([for e in google_cloud_run_v2_service.this.template[0].containers[1].env : e.name == "OTELCOL_CONFIG" && strcontains(e.value, "googlecloud") && strcontains(e.value, "\"metrics\":")])
    error_message = "The collector config must export traces and metrics to Google Cloud."
  }

  assert {
    condition     = google_cloud_run_v2_service.this.template[0].containers[0].resources[0].cpu_idle == false
    error_message = "With the collector sidecar CPU must stay allocated between requests."
  }
}
