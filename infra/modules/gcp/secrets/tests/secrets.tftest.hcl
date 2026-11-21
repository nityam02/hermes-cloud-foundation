# Mock-provider tests: residency, CMEK, containers only. No cloud calls.

mock_provider "google" {}

variables {
  region  = "europe-north1"
  secrets = { maps-api-key = "demo-dev-maps-api-key" }
}

run "regional_cmek_secret_without_versions" {
  command = apply

  variables {
    enable_cmek = true
    kms_key     = "projects/example-project/locations/europe-north1/keyRings/r/cryptoKeys/k"
  }

  assert {
    condition     = google_secret_manager_secret.this["maps-api-key"].replication[0].user_managed[0].replicas[0].location == "europe-north1"
    error_message = "Secrets must be replicated only in the environment region."
  }

  assert {
    condition     = google_secret_manager_secret.this["maps-api-key"].replication[0].user_managed[0].replicas[0].customer_managed_encryption[0].kms_key_name == "projects/example-project/locations/europe-north1/keyRings/r/cryptoKeys/k"
    error_message = "The replica must use the customer-managed key."
  }

  assert {
    condition     = google_secret_manager_secret.this["maps-api-key"].version_destroy_ttl == "604800s" && output.secret_ids["maps-api-key"] == "demo-dev-maps-api-key"
    error_message = "Destroyed versions must stay recoverable; Cloud Run gets the short id."
  }
}
