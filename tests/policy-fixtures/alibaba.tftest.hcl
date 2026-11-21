# Mock-provider plan of a rendered Hermes root (multi-cloud example, environment alidev), used by
# scripts/gen-policy-fixtures.sh to derive policies/tests/fixtures/alibaba_default/plan.json.
# Mock providers only: no cloud API is called.

mock_provider "alicloud" {
  mock_data "alicloud_account" {
    defaults = {
      id = "5000000000000001"
    }
  }
  mock_data "alicloud_zones" {
    defaults = {
      ids = ["eu-central-1a", "eu-central-1b", "eu-central-1c"]
    }
  }
  # Realistic or obviously fake values for computed attributes (random mock strings trip gitleaks).
  mock_resource "alicloud_kms_key" {
    defaults = {
      key_state = "Enabled"
    }
  }
  mock_resource "alicloud_db_instance" {
    defaults = {
      server_key = "demo-not-a-secret"
    }
  }
}

run "plan" {
  command = plan
}
