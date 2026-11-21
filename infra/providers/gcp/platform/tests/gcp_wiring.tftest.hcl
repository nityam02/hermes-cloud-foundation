# GCP-specific composition tests: least privilege, CMEK wiring, HA, deletion
# protection, ingress, dead-letter wiring and the binding environment.
# Mock providers only (no Google Cloud API calls); mocks prove wiring, not cloud behaviour.

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
  region      = "europe-north1"
  extensions  = {}
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
        name          = "api"
        image         = "ghcr.io/example/api:1.0.0"
        cpu           = 0.5
        memory_mb     = 1024
        min_instances = 1
        max_instances = 3
        public        = true
        env           = { LOG_LEVEL = "info" }
        secret_env    = { MAPS_API_KEY = "maps-api-key" }
        access = {
          buckets  = { artifacts = "read-write", images = "read" }
          publish  = ["telemetry"]
          database = true
        }
      },
      {
        name          = "worker"
        image         = "ghcr.io/example/worker:1.0.0"
        cpu           = 0.25
        memory_mb     = 512
        min_instances = 0
        max_instances = 2
        access = {
          consume = ["telemetry-ingest"]
        }
      },
    ]
  }

  database = {
    enabled             = true
    high_availability   = true
    deletion_protection = false
    extensions          = ["postgis"]
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
        name = "telemetry"
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
    enabled     = true
    alert_email = "oncall@example.com"
  }
}

run "least_privilege_access" {
  command = apply

  assert {
    condition     = toset(keys(google_storage_bucket_iam_member.service)) == toset(["api/artifacts", "api/images"])
    error_message = "Only api may access buckets; worker must not read any bucket."
  }

  assert {
    condition     = google_storage_bucket_iam_member.service["api/artifacts"].role == "roles/storage.objectUser" && google_storage_bucket_iam_member.service["api/images"].role == "roles/storage.objectViewer"
    error_message = "read-write must map to objectUser and read to objectViewer."
  }

  assert {
    condition     = google_storage_bucket_iam_member.service["api/images"].bucket == output.bindings.buckets["images"].name && google_storage_bucket_iam_member.service["api/images"].member == "serviceAccount:${google_service_account.service["api"].email}"
    error_message = "Bucket grants must target the exact bucket and the service's own identity."
  }

  assert {
    condition     = toset(keys(google_pubsub_topic_iam_member.service)) == toset(["api/telemetry"]) && google_pubsub_topic_iam_member.service["api/telemetry"].role == "roles/pubsub.publisher" && google_pubsub_topic_iam_member.service["api/telemetry"].topic == output.bindings.topics["telemetry"].id
    error_message = "api may publish only to the telemetry topic."
  }

  assert {
    condition     = toset(keys(google_pubsub_subscription_iam_member.service)) == toset(["worker/telemetry-ingest"]) && google_pubsub_subscription_iam_member.service["worker/telemetry-ingest"].role == "roles/pubsub.subscriber"
    error_message = "worker may consume only telemetry-ingest."
  }

  assert {
    condition     = toset(keys(google_secret_manager_secret_iam_member.service)) == toset(["api/maps-api-key"]) && google_secret_manager_secret_iam_member.service["api/maps-api-key"].role == "roles/secretmanager.secretAccessor"
    error_message = "api may read only the secret it references; nobody reads webhook-signing-key."
  }

  assert {
    condition     = toset(keys(google_project_iam_member.database)) == toset(["api/client", "api/instanceUser"]) && alltrue([for m in values(google_project_iam_member.database) : strcontains(m.condition[0].expression, "/instances/${module.postgres[0].instance_name}")])
    error_message = "Only api gets Cloud SQL roles, conditioned on this environment's instance."
  }

  assert {
    condition     = length(google_project_iam_member.observability) == 6 && alltrue([for m in values(google_project_iam_member.observability) : contains(["roles/logging.logWriter", "roles/monitoring.metricWriter", "roles/cloudtrace.agent"], m.role)])
    error_message = "Each service gets only the log, metric and trace writer roles at project level."
  }

  assert {
    # Mocks give every service account the same random email; account_id is configuration.
    condition     = google_service_account.service["api"].account_id == "contract-full-staging-api" && google_service_account.service["worker"].account_id == "contract-full-staging-worker"
    error_message = "Each service must have its own identity named <project>-<environment>-<service>."
  }
}

run "cmek_ha_ingress_and_bindings" {
  command = apply

  assert {
    condition     = toset(keys(module.kms[0].encrypter_members)) == toset(["pubsub", "secretmanager", "sql", "storage"])
    error_message = "Every service agent using the key must be granted encrypter/decrypter."
  }

  assert {
    condition     = module.kms[0].encrypter_members["pubsub"] == "serviceAccount:service-123456789012@gcp-sa-pubsub.iam.gserviceaccount.com" && module.kms[0].encrypter_members["storage"] == "serviceAccount:service-123456789012@gs-project-accounts.iam.gserviceaccount.com"
    error_message = "Pub/Sub and Cloud Storage service agents must be derived from the project."
  }

  assert {
    condition     = module.kms[0].encrypter_members["sql"] == "serviceAccount:${google_project_service_identity.sql[0].email}" && module.kms[0].encrypter_members["secretmanager"] == "serviceAccount:${google_project_service_identity.secretmanager[0].email}"
    error_message = "Cloud SQL and Secret Manager agents must come from their service identities."
  }

  assert {
    condition     = alltrue([for k in values(module.storage.kms_keys) : k == module.kms[0].key_id]) && module.postgres[0].encryption_key_name == module.kms[0].key_id
    error_message = "Buckets and the database must use the environment key."
  }

  assert {
    condition     = module.postgres[0].availability_type == "REGIONAL" && module.postgres[0].deletion_protection == false
    error_message = "high_availability must give a REGIONAL instance; deletion protection follows the spec when not protected."
  }

  assert {
    condition     = module.service["api"].ingress == "INGRESS_TRAFFIC_ALL" && module.service["worker"].ingress == "INGRESS_TRAFFIC_INTERNAL_ONLY" && output.bindings.services["worker"].url == ""
    error_message = "Public services accept all ingress; private ones are internal-only with no URL."
  }

  assert {
    condition     = module.service["api"].containers == ["app", "otel-collector"] && module.service["worker"].effective_cpu == 1
    error_message = "Tracing adds the collector sidecar; fractional CPU rounds up to 1."
  }

  assert {
    condition     = toset(keys(module.messaging.dead_letter_topic_ids)) == toset(["telemetry-ingest"])
    error_message = "Only subscriptions with max_delivery_attempts > 0 get a dead-letter topic."
  }

  assert {
    condition     = output.service_environment["api"]["HERMES_DB_USER"] == trimsuffix(google_service_account.service["api"].email, ".gserviceaccount.com") && output.service_environment["api"]["HERMES_DB_AUTH"] == "iam" && output.bindings.database.user == output.service_environment["api"]["HERMES_DB_USER"] && output.bindings.database.credentials_ref == ""
    error_message = "IAM database auth: the per-service IAM user, no stored credential."
  }

  assert {
    condition     = output.service_environment["api"]["HERMES_BUCKET_ARTIFACTS"] == output.bindings.buckets["artifacts"].name && output.service_environment["api"]["HERMES_TOPIC_TELEMETRY"] == output.bindings.topics["telemetry"].id && output.service_environment["worker"]["HERMES_SUBSCRIPTION_TELEMETRY_INGEST"] == output.bindings.topics["telemetry"].subscriptions["telemetry-ingest"]
    error_message = "Binding env vars must carry the native identifiers from bindings."
  }

  assert {
    condition     = output.service_environment["api"]["OTEL_EXPORTER_OTLP_ENDPOINT"] == "http://localhost:4317" && output.service_environment["api"]["OTEL_EXPORTER_OTLP_PROTOCOL"] == "grpc" && output.bindings.observability.otlp_endpoint == "http://localhost:4317"
    error_message = "Tracing uses the localhost gRPC collector endpoint."
  }

  assert {
    condition     = output.secret_environment["api"]["MAPS_API_KEY"] == output.bindings.secrets["maps-api-key"]
    error_message = "secret_environment must reference the Secret Manager secret."
  }

  assert {
    condition     = alltrue([for k, b in output.bindings.buckets : can(regex("^contract-full-staging-${k}-[0-9a-f]{8}$", b.name)) && b.url == "gs://${b.name}"])
    error_message = "Bucket names must be <project>-<environment>-<name>-<random hex> with gs:// URLs."
  }

  assert {
    condition     = output.bindings.observability.alert_channel != "" && !endswith(output.bindings.observability.log_destination, "/_Default")
    error_message = "An alert email must create a notification channel; logs go to the environment log bucket."
  }

  assert {
    condition     = alltrue([for api in ["run.googleapis.com", "sqladmin.googleapis.com", "servicenetworking.googleapis.com", "cloudkms.googleapis.com", "pubsub.googleapis.com", "secretmanager.googleapis.com", "cloudtrace.googleapis.com"] : contains(keys(google_project_service.this), api)]) && alltrue([for s in values(google_project_service.this) : s.disable_on_destroy == false])
    error_message = "Required APIs must be enabled and never disabled on destroy."
  }
}

run "protected_lean_environment" {
  command = apply

  variables {
    project   = "contract-prot"
    protected = true
    security  = { customer_managed_keys = false }
    network   = { cidr = "10.40.0.0/20", zones = 3, nat = false }
    database = {
      enabled             = true
      high_availability   = false
      deletion_protection = false
    }
    observability = { enabled = false }
  }

  assert {
    condition     = module.postgres[0].deletion_protection == true && module.service["api"].deletion_protection == true && module.service["worker"].deletion_protection == true
    error_message = "Protected environments must enable deletion protection on the database and every service."
  }

  assert {
    condition     = module.postgres[0].availability_type == "ZONAL" && module.postgres[0].encryption_key_name == ""
    error_message = "Without HA the instance is ZONAL; without CMK it uses Google-managed encryption."
  }

  assert {
    condition     = length(module.kms) == 0 && length(google_project_service_identity.sql) == 0 && alltrue([for k in values(module.storage.kms_keys) : k == ""])
    error_message = "No key, key grants or service identities without customer_managed_keys."
  }

  assert {
    condition     = module.service["api"].containers == ["app"] && length(google_project_iam_member.observability) == 0
    error_message = "Observability disabled: no collector sidecar and no telemetry writer roles."
  }

  assert {
    condition     = length([for k in keys(output.service_environment["api"]) : k if startswith(k, "OTEL_")]) == 0
    error_message = "Observability disabled: no OTEL_ variables."
  }

  assert {
    condition     = output.bindings.observability.log_destination == "projects/example-project/locations/global/buckets/_Default" && output.bindings.observability.otlp_endpoint == "" && output.bindings.observability.alert_channel == ""
    error_message = "Observability disabled: logs stay in _Default, no OTLP endpoint, no alert channel."
  }
}
