# Mock-provider plan of a rendered Hermes root (multi-cloud example, environment gcpdev), used by
# scripts/gen-policy-fixtures.sh to derive policies/tests/fixtures/gcp_default/plan.json.
# Mock providers only: no cloud API is called.

mock_provider "google" {
  mock_data "google_client_config" {
    defaults = {
      project = "example-project-1234"
    }
  }
  mock_data "google_project" {
    defaults = {
      number = "123456789012"
    }
  }
  # Realistic id where the provider validates the format of a referencing argument.
  mock_resource "google_compute_network" {
    defaults = {
      id = "projects/example-project-1234/global/networks/mock-network"
    }
  }
  # Realistic id instead of a random mock string (it is copied into IAM members' secret_id).
  mock_resource "google_secret_manager_secret" {
    defaults = {
      id = "projects/example-project-1234/secrets/sample-portable-gcpdev-signing-key"
    }
  }
  mock_data "google_storage_project_service_account" {
    defaults = {
      email_address = "service-123456789012@gs-project-accounts.iam.gserviceaccount.com"
    }
  }
}

mock_provider "google-beta" {}

run "plan" {
  command = plan
}
