# -----------------------------------------------------------------------------
# GCP provider header for the Hermes contract tests.
# Mock providers only: no Google Cloud API is called and nothing is created.
# hashicorp/random runs for real (it is local-only) so names carry real hex suffixes.
# -----------------------------------------------------------------------------

mock_provider "google" {
  mock_data "google_client_config" {
    defaults = {
      project = "example-project"
    }
  }

  mock_data "google_project" {
    defaults = {
      number = "123456789012"
    }
  }

  # Realistic ids where the provider validates the format of a referencing argument.
  mock_resource "google_compute_network" {
    defaults = {
      id = "projects/example-project/global/networks/mock-network"
    }
  }

  mock_data "google_storage_project_service_account" {
    defaults = {
      email_address = "service-123456789012@gs-project-accounts.iam.gserviceaccount.com"
    }
  }
}

mock_provider "google-beta" {}

variables {
  region     = "europe-north1"
  extensions = {}
}

# --- BEGIN HERMES CONTRACT RUNS (canonical source: tests/contracts/contract-runs.hcl) ---
#
# Every provider composition has infra/providers/<provider>/platform/tests/contract.tftest.hcl
# made of two parts:
#   1. a provider-specific header: mock_provider blocks, plus a file-level
#      `variables { region = "...", extensions = {...} }` block;
#   2. this canonical section, copied verbatim from the BEGIN marker to the end of the file.
# tests/contracts verifies the canonical section is byte-identical in all providers.
# Runs use `command = apply` against mock providers only: no cloud resources are created.

run "contract_minimal" {
  command = apply

  variables {
    project     = "contract-min"
    environment = "dev"
  }

  assert {
    condition     = alltrue([for k in ["provider", "region", "project", "environment", "network", "services", "database", "buckets", "topics", "secrets", "observability"] : contains(keys(output.bindings), k)])
    error_message = "bindings must expose every contract key."
  }

  assert {
    condition     = output.bindings.project == "contract-min" && output.bindings.environment == "dev"
    error_message = "bindings must echo project and environment."
  }

  assert {
    condition     = contains(["aws", "gcp", "alibaba"], output.bindings.provider)
    error_message = "bindings.provider must be aws, gcp or alibaba."
  }

  assert {
    condition     = output.bindings.database == null
    error_message = "database binding must be null when the database is disabled."
  }

  assert {
    condition     = length(output.bindings.buckets) == 0 && length(output.bindings.topics) == 0 && length(output.bindings.services) == 0 && length(output.bindings.secrets) == 0
    error_message = "disabled capabilities must produce empty maps."
  }

  assert {
    condition     = alltrue([for k in ["id", "name", "cidr"] : contains(keys(output.bindings.network), k)])
    error_message = "network binding must expose id, name and cidr."
  }

  assert {
    condition     = alltrue([for k in ["log_destination", "otlp_endpoint", "alert_channel"] : contains(keys(output.bindings.observability), k)])
    error_message = "observability binding must expose log_destination, otlp_endpoint and alert_channel."
  }
}

run "contract_full" {
  command = apply

  variables {
    project     = "contract-full"
    environment = "staging"
    protected   = false
    labels      = { cost-center = "platform" }
    network     = { cidr = "10.40.0.0/16", zones = 2, nat = true }
    security    = { customer_managed_keys = true }

    compute = {
      type = "containerized"
      services = [
        {
          name              = "api"
          image             = "ghcr.io/example/api:1.0.0"
          port              = 8080
          cpu               = 0.5
          memory_mb         = 1024
          min_instances     = 1
          max_instances     = 3
          public            = true
          health_check_path = "/healthz"
          command           = []
          env               = { LOG_LEVEL = "info" }
          secret_env        = { MAPS_API_KEY = "maps-api-key" }
          access = {
            buckets  = { artifacts = "read-write", images = "read" }
            publish  = ["telemetry"]
            consume  = []
            database = true
          }
        },
        {
          name              = "worker"
          image             = "ghcr.io/example/worker:1.0.0"
          port              = 8080
          cpu               = 0.25
          memory_mb         = 512
          min_instances     = 0
          max_instances     = 2
          public            = false
          health_check_path = "/healthz"
          command           = []
          env               = {}
          secret_env        = {}
          access = {
            buckets  = {}
            publish  = []
            consume  = ["telemetry-ingest"]
            database = false
          }
        },
      ]
    }

    database = {
      enabled                = true
      engine                 = "postgresql"
      version                = "16"
      size                   = "small"
      storage_gb             = 20
      max_storage_gb         = 100
      high_availability      = true
      backup_retention_days  = 7
      point_in_time_recovery = true
      deletion_protection    = false
      extensions             = ["postgis"]
    }

    storage = {
      enabled = true
      buckets = [
        { name = "artifacts", versioning = true, retention_days = 0 },
        { name = "images", versioning = false, retention_days = 30 },
      ]
    }

    messaging = {
      enabled = true
      topics = [
        {
          name            = "telemetry"
          retention_hours = 168
          subscriptions = [
            { name = "telemetry-ingest", ack_deadline_seconds = 30, max_delivery_attempts = 5 },
            { name = "telemetry-archive", ack_deadline_seconds = 60, max_delivery_attempts = 0 },
          ]
        },
      ]
    }

    secrets = {
      enabled = true
      names   = ["maps-api-key", "webhook-signing-key"]
    }

    observability = {
      enabled            = true
      log_retention_days = 30
      metrics            = true
      tracing            = true
      alert_email        = "oncall@example.com"
    }
  }

  assert {
    condition     = sort(keys(output.bindings.services)) == tolist(["api", "worker"])
    error_message = "services binding must be keyed by spec service name."
  }

  assert {
    condition     = sort(keys(output.bindings.buckets)) == tolist(["artifacts", "images"])
    error_message = "buckets binding must be keyed by spec bucket name."
  }

  assert {
    condition     = alltrue([for b in values(output.bindings.buckets) : length(b.name) >= 3 && length(b.name) <= 63])
    error_message = "native bucket names must be 3-63 characters."
  }

  assert {
    condition     = sort(keys(output.bindings.topics)) == tolist(["telemetry"]) && sort(keys(output.bindings.topics["telemetry"].subscriptions)) == tolist(["telemetry-archive", "telemetry-ingest"])
    error_message = "topics binding must be keyed by topic and subscription name."
  }

  assert {
    condition     = sort(keys(output.bindings.secrets)) == tolist(["maps-api-key", "webhook-signing-key"])
    error_message = "secrets binding must be keyed by spec secret name."
  }

  assert {
    condition     = output.bindings.database != null && output.bindings.database.port == 5432 && contains(["iam", "password"], output.bindings.database.auth)
    error_message = "database binding must expose port 5432 and an auth mode of iam or password."
  }

  assert {
    condition = sort(keys(output.service_environment["api"])) == sort([
      "HERMES_PROVIDER", "HERMES_REGION", "HERMES_PROJECT", "HERMES_ENVIRONMENT", "HERMES_SERVICE",
      "HERMES_BUCKET_ARTIFACTS", "HERMES_BUCKET_IMAGES", "HERMES_TOPIC_TELEMETRY",
      "HERMES_DB_HOST", "HERMES_DB_PORT", "HERMES_DB_NAME", "HERMES_DB_USER", "HERMES_DB_AUTH",
      "OTEL_SERVICE_NAME", "OTEL_RESOURCE_ATTRIBUTES", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL",
      "LOG_LEVEL",
    ])
    error_message = "api service must receive exactly the binding-contract environment variables."
  }

  assert {
    condition = sort(keys(output.service_environment["worker"])) == sort([
      "HERMES_PROVIDER", "HERMES_REGION", "HERMES_PROJECT", "HERMES_ENVIRONMENT", "HERMES_SERVICE",
      "HERMES_SUBSCRIPTION_TELEMETRY_INGEST",
      "OTEL_SERVICE_NAME", "OTEL_RESOURCE_ATTRIBUTES", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL",
    ])
    error_message = "worker service must receive exactly the binding-contract environment variables."
  }

  assert {
    condition     = sort(keys(output.secret_environment["api"])) == tolist(["MAPS_API_KEY"]) && length(output.secret_environment["worker"]) == 0
    error_message = "secret_environment must map env var names to secret references only."
  }

  assert {
    condition     = output.service_environment["api"]["HERMES_PROVIDER"] == output.bindings.provider && output.service_environment["api"]["HERMES_SERVICE"] == "api"
    error_message = "binding env vars must carry provider and service identity."
  }
}

run "contract_rejects_kubernetes" {
  command = plan

  variables {
    project     = "contract-k8s"
    environment = "dev"
    compute     = { type = "kubernetes" }
  }

  expect_failures = [var.compute]
}

run "contract_rejects_mysql" {
  command = plan

  variables {
    project     = "contract-mysql"
    environment = "dev"
    database    = { enabled = true, engine = "mysql" }
  }

  expect_failures = [var.database]
}

run "contract_rejects_bad_project" {
  command = plan

  variables {
    project     = "Bad_Name"
    environment = "dev"
  }

  expect_failures = [var.project]
}
