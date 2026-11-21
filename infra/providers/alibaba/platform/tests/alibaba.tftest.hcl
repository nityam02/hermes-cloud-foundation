# Alibaba-specific composition tests (mock providers only; no cloud API is called).
mock_provider "alicloud" {
  mock_data "alicloud_account" {
    defaults = {
      id = "1234567890123456"
    }
  }

  mock_data "alicloud_zones" {
    defaults = {
      ids = ["eu-central-1a", "eu-central-1b", "eu-central-1c"]
    }
  }

  mock_resource "alicloud_db_instance" {
    defaults = {
      connection_string = "pgm-mock.pg.rds.aliyuncs.com"
    }
  }

  mock_resource "alicloud_sae_load_balancer_internet" {
    defaults = {
      internet_ip = "203.0.113.10"
    }
  }

  mock_resource "alicloud_eip_address" {
    defaults = {
      ip_address = "203.0.113.20"
    }
  }

  mock_resource "alicloud_ram_role" {
    defaults = {
      arn = "acs:ram::1234567890123456:role/mock-service-role"
    }
  }
}

variables {
  project     = "guard"
  environment = "prod"
  region      = "eu-central-1"
}

run "protected_public_service_requires_certificate" {
  command = plan

  variables {
    protected  = true
    extensions = {}
    compute = {
      type     = "containerized"
      services = [{ name = "api", image = "ghcr.io/example/api:1.0.0", public = true }]
    }
  }

  expect_failures = [terraform_data.guardrails]
}

run "secrets_require_kms_instance" {
  command = plan

  variables {
    extensions = {}
    secrets    = { enabled = true, names = ["db-password"] }
  }

  expect_failures = [terraform_data.guardrails]
}

run "xlarge_basic_database_is_rejected" {
  command = plan

  variables {
    extensions = {}
    database   = { enabled = true, size = "xlarge", high_availability = false }
  }

  expect_failures = [terraform_data.guardrails]
}

run "protected_https_bindings" {
  command = apply

  variables {
    protected = true
    # Top-level extension keys (without the "alibaba" wrapper) are accepted too.
    extensions = {
      certificate_id  = "1234567-cn-mock"
      kms_instance_id = "kst-mock-not-a-real-instance"
      otlp_endpoint   = "http://otel-collector.internal.example.com:4317"
    }
    security = { customer_managed_keys = true }
    compute = {
      type = "containerized"
      services = [
        {
          name       = "api"
          image      = "ghcr.io/example/api:1.0.0"
          public     = true
          secret_env = { DATABASE_PASSWORD = "db-password" } # gitleaks:allow (secret name reference, not a value)
          access     = { buckets = { data = "read" }, database = true }
        },
        { name = "worker", image = "ghcr.io/example/worker:1.0.0" },
      ]
    }
    database = { enabled = true, high_availability = true }
    storage  = { enabled = true, buckets = [{ name = "data" }] }
    secrets  = { enabled = true, names = ["db-password"] }
  }

  assert {
    condition     = output.bindings.services["api"].url == "https://203.0.113.10" && output.bindings.services["worker"].url == ""
    error_message = "Public services must get an HTTPS URL when a certificate is set; private services get no URL."
  }

  assert {
    condition     = output.bindings.database.auth == "password" && output.bindings.database.user == "app" && output.bindings.database.name == "app" && output.bindings.database.credentials_ref == "guard-prod-db-credentials"
    error_message = "Database binding must describe the out-of-band password credential."
  }

  assert {
    condition     = output.bindings.buckets["data"].url == "oss://${output.bindings.buckets["data"].name}" && output.service_environment["api"]["HERMES_BUCKET_DATA"] == output.bindings.buckets["data"].name
    error_message = "Bucket binding and env var must carry the native OSS bucket name."
  }

  assert {
    condition     = output.bindings.secrets["db-password"] == "guard-prod-db-password" && output.secret_environment["api"]["DATABASE_PASSWORD"] == "guard-prod-db-password"
    error_message = "Secret references must be native KMS secret names, never values."
  }

  assert {
    condition     = output.service_environment["api"]["OTEL_EXPORTER_OTLP_ENDPOINT"] == "http://otel-collector.internal.example.com:4317" && output.bindings.observability.otlp_endpoint == "http://otel-collector.internal.example.com:4317"
    error_message = "The OTLP endpoint must come from extensions.alibaba.otlp_endpoint."
  }

  assert {
    condition     = output.service_environment["api"]["HERMES_PROVIDER"] == "alibaba" && output.service_environment["api"]["HERMES_DB_PORT"] == "5432"
    error_message = "Binding env vars must identify the provider and the database port."
  }
}

run "tracing_disabled_keeps_otel_keys" {
  command = apply

  variables {
    extensions    = {}
    compute       = { type = "containerized", services = [{ name = "api", image = "ghcr.io/example/api:1.0.0" }] }
    observability = { enabled = true, tracing = false }
  }

  assert {
    condition     = output.service_environment["api"]["OTEL_EXPORTER_OTLP_ENDPOINT"] == "" && output.bindings.observability.otlp_endpoint == "" && contains(keys(output.service_environment["api"]), "OTEL_SERVICE_NAME")
    error_message = "With tracing off the OTEL keys stay (same keys on every provider) but no endpoint is set."
  }
}

run "workload_roles_never_trust_account_root" {
  command = apply

  variables {
    extensions = {}
    compute    = { type = "containerized", services = [{ name = "api", image = "ghcr.io/example/api:1.0.0" }] }
  }

  assert {
    condition     = !strcontains(module.compute[0].trust_policy, "acs:ram::1234567890123456:root") && jsondecode(module.compute[0].trust_policy).Statement[0].Principal == { Service = ["sae.aliyuncs.com"] }
    error_message = "Workload roles must trust the SAE service principal by default, never acs:ram::<account>:root."
  }
}

run "workload_role_trusted_principals_extension" {
  command = apply

  variables {
    extensions = { alibaba = { workload_role_trusted_principals = ["acs:ram::1234567890123456:role/example-deployer"] } }
    compute    = { type = "containerized", services = [{ name = "api", image = "ghcr.io/example/api:1.0.0" }] }
  }

  assert {
    condition     = jsondecode(module.compute[0].trust_policy).Statement[0].Principal == { RAM = ["acs:ram::1234567890123456:role/example-deployer"] } && !strcontains(module.compute[0].trust_policy, ":root")
    error_message = "extensions.alibaba.workload_role_trusted_principals must replace the SAE service principal."
  }
}
