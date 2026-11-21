# Spec cross-check tests: terraform_data.spec_guard must reject invalid combinations
# with a clear error. Plan only, mock providers only.

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
  project     = "guard-test"
  environment = "dev"
  storage     = { enabled = true, buckets = [{ name = "data" }] }
  messaging   = { enabled = true, topics = [{ name = "events", subscriptions = [{ name = "events-default" }] }] }
  secrets     = { enabled = true, names = ["api-key"] }
}

run "valid_spec_passes" {
  command = plan

  variables {
    compute = {
      type = "containerized"
      services = [{
        name       = "api"
        image      = "ghcr.io/example/api:1.0.0"
        secret_env = { API_KEY = "api-key" }
        access     = { buckets = { data = "read" }, publish = ["events"], consume = ["events-default"] }
      }]
    }
  }
}

run "rejects_unknown_extension_key" {
  command = plan

  variables {
    extensions = { vpc_egres = "all-traffic" }
  }

  expect_failures = [terraform_data.spec_guard]
}

run "rejects_invalid_vpc_egress" {
  command = plan

  variables {
    extensions = { vpc_egress = "everything" }
  }

  expect_failures = [terraform_data.spec_guard]
}

run "rejects_access_to_undeclared_bucket" {
  command = plan

  variables {
    compute = {
      type     = "containerized"
      services = [{ name = "api", image = "ghcr.io/example/api:1.0.0", access = { buckets = { missing = "read" } } }]
    }
  }

  expect_failures = [terraform_data.spec_guard]
}

run "rejects_undeclared_secret_reference" {
  command = plan

  variables {
    compute = {
      type     = "containerized"
      services = [{ name = "api", image = "ghcr.io/example/api:1.0.0", secret_env = { TOKEN = "not-declared" } }]
    }
  }

  expect_failures = [terraform_data.spec_guard]
}

run "rejects_database_access_without_database" {
  command = plan

  variables {
    compute = {
      type     = "containerized"
      services = [{ name = "api", image = "ghcr.io/example/api:1.0.0", access = { database = true } }]
    }
  }

  expect_failures = [terraform_data.spec_guard]
}

run "rejects_reserved_env_prefix" {
  command = plan

  variables {
    compute = {
      type     = "containerized"
      services = [{ name = "api", image = "ghcr.io/example/api:1.0.0", env = { HERMES_REGION = "spoofed" } }]
    }
  }

  expect_failures = [terraform_data.spec_guard]
}

run "rejects_duplicate_subscription_names" {
  command = plan

  variables {
    messaging = {
      enabled = true
      topics = [
        { name = "a", subscriptions = [{ name = "shared" }] },
        { name = "b", subscriptions = [{ name = "shared" }] },
      ]
    }
  }

  expect_failures = [terraform_data.spec_guard]
}

run "rejects_goog_project_prefix" {
  command = plan

  variables {
    project = "googly-app"
  }

  expect_failures = [terraform_data.spec_guard]
}
