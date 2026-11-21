# Mock-provider tests (plan only: the bucket has prevent_destroy, so an applied
# test could not be torn down). No Google Cloud API is called.

mock_provider "google" {
  mock_data "google_storage_project_service_account" {
    defaults = {
      email_address = "service-123456789012@gs-project-accounts.iam.gserviceaccount.com"
    }
  }
}

variables {
  project     = "demo"
  region      = "europe-north1"
  bucket_name = "demo-hermes-state-0a1b2c3d"
  labels      = { "Cost Center" = "Platform" }
}

run "private_versioned_bucket" {
  command = plan

  assert {
    condition     = google_storage_bucket.state.uniform_bucket_level_access == true && google_storage_bucket.state.public_access_prevention == "enforced"
    error_message = "The state bucket must use uniform access and enforce public access prevention."
  }

  assert {
    condition     = google_storage_bucket.state.versioning[0].enabled == true && google_storage_bucket.state.force_destroy == false
    error_message = "The state bucket must be versioned and never force-destroyed."
  }

  assert {
    condition     = google_storage_bucket.state.location == "EUROPE-NORTH1"
    error_message = "The bucket must be regional in the requested region."
  }

  assert {
    condition     = length(google_storage_bucket.state.encryption) == 0 && length(google_kms_crypto_key_iam_member.gcs) == 0
    error_message = "Without kms_key the bucket uses Google-managed encryption and no key grant."
  }

  assert {
    condition     = anytrue([for r in google_storage_bucket.state.lifecycle_rule : one(r.action).type == "Delete" && one(r.condition).days_since_noncurrent_time == 90])
    error_message = "Noncurrent state versions must be cleaned up after 90 days."
  }

  assert {
    condition     = google_storage_bucket.state.labels["hermes-managed-by"] == "hermes" && google_storage_bucket.state.labels["cost_center"] == "platform"
    error_message = "Labels must be sanitized and include Hermes labels."
  }

  assert {
    condition     = output.backend_config["bucket"] == "demo-hermes-state-0a1b2c3d" && output.backend_config["prefix"] == "hermes/demo" && output.bucket == "demo-hermes-state-0a1b2c3d"
    error_message = "backend_config must expose bucket and prefix."
  }
}

run "customer_managed_key" {
  command = plan

  variables {
    kms_key = "projects/example-project/locations/europe-north1/keyRings/state/cryptoKeys/state"
  }

  assert {
    condition     = google_storage_bucket.state.encryption[0].default_kms_key_name == "projects/example-project/locations/europe-north1/keyRings/state/cryptoKeys/state"
    error_message = "The bucket must use the customer-managed key."
  }

  assert {
    condition     = google_kms_crypto_key_iam_member.gcs[0].member == "serviceAccount:service-123456789012@gs-project-accounts.iam.gserviceaccount.com" && google_kms_crypto_key_iam_member.gcs[0].role == "roles/cloudkms.cryptoKeyEncrypterDecrypter"
    error_message = "The Cloud Storage service agent must be granted use of the key."
  }
}

run "rejects_invalid_bucket_name" {
  command = plan

  variables {
    bucket_name = "google-state"
  }

  expect_failures = [var.bucket_name]
}
