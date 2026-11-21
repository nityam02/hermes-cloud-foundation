# Mock-provider tests: bucket security settings and lifecycle. No cloud calls.

mock_provider "google" {}

variables {
  location = "europe-north1"
  buckets = {
    artifacts = { name = "demo-dev-artifacts-0a1b2c3d", versioning = true, retention_days = 0 }
    images    = { name = "demo-dev-images-0a1b2c3d", versioning = false, retention_days = 30 }
  }
}

run "private_versioned_buckets" {
  command = apply

  assert {
    condition     = alltrue([for b in values(google_storage_bucket.this) : b.uniform_bucket_level_access && b.public_access_prevention == "enforced" && b.force_destroy == false && b.location == "EUROPE-NORTH1"])
    error_message = "Buckets must be private (UBLA + PAP enforced), regional and never force-destroyed."
  }

  assert {
    condition     = google_storage_bucket.this["artifacts"].versioning[0].enabled == true && google_storage_bucket.this["images"].versioning[0].enabled == false
    error_message = "Versioning must follow the spec."
  }

  assert {
    condition     = length([for r in google_storage_bucket.this["artifacts"].lifecycle_rule : r if one(r.action).type == "Delete" && one(r.condition).with_state == "LIVE"]) == 0
    error_message = "retention_days = 0 must keep current objects forever."
  }

  assert {
    condition     = anytrue([for r in google_storage_bucket.this["images"].lifecycle_rule : one(r.action).type == "Delete" && one(r.condition).age == 30 && one(r.condition).with_state == "LIVE"])
    error_message = "retention_days > 0 must expire current objects at that age."
  }

  assert {
    condition     = anytrue([for r in google_storage_bucket.this["artifacts"].lifecycle_rule : one(r.action).type == "Delete" && one(r.condition).days_since_noncurrent_time == 30])
    error_message = "Versioned buckets must clean up noncurrent versions."
  }

  assert {
    condition     = alltrue([for k in values(output.kms_keys) : k == ""]) && output.urls["images"] == "gs://demo-dev-images-0a1b2c3d"
    error_message = "Without CMEK no default key is set; URLs use gs://."
  }
}

run "customer_managed_key" {
  command = apply

  variables {
    enable_cmek = true
    kms_key     = "projects/example-project/locations/europe-north1/keyRings/r/cryptoKeys/k"
  }

  assert {
    condition     = alltrue([for b in values(google_storage_bucket.this) : b.encryption[0].default_kms_key_name == "projects/example-project/locations/europe-north1/keyRings/r/cryptoKeys/k"])
    error_message = "Every bucket must use the customer-managed key."
  }
}
