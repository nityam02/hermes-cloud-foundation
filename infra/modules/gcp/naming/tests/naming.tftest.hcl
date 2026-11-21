# Pure-function tests: no providers, no cloud calls.

run "short_names_are_unchanged" {
  command = plan

  variables {
    project     = "demo"
    environment = "dev"
    resources = {
      service = { name = "api", max_length = 49 }
      bare    = { name = "", max_length = 63 }
      bucket  = { name = "artifacts", max_length = 63, suffix = "0a1b2c3d" }
    }
  }

  assert {
    condition     = output.names["service"] == "demo-dev-api"
    error_message = "Short names must be <project>-<environment>-<name>."
  }

  assert {
    condition     = output.names["bare"] == "demo-dev"
    error_message = "An empty name part must yield <project>-<environment>."
  }

  assert {
    condition     = output.names["bucket"] == "demo-dev-artifacts-0a1b2c3d"
    error_message = "The suffix must be appended after the name."
  }
}

run "long_names_are_hash_truncated_within_limits" {
  command = plan

  variables {
    project     = "sample-logistics-platform"
    environment = "production"
    resources = {
      service_account = { name = "telemetry-ingest-eu1", max_length = 30 }
      cloud_run       = { name = "telemetry-ingest-eu1", max_length = 49 }
      bucket          = { name = "telemetry-ingest-eu1", max_length = 63, suffix = "0a1b2c3d" }
      topic           = { name = "telemetry-ingest-eu1", max_length = 255 }
    }
  }

  assert {
    condition     = length(output.names["service_account"]) <= 30 && length(output.names["cloud_run"]) <= 49 && length(output.names["bucket"]) <= 63
    error_message = "Truncated names must respect each provider limit."
  }

  assert {
    condition     = endswith(output.names["service_account"], "-${substr(sha1("sample-logistics-platform-production-telemetry-ingest-eu1"), 0, 6)}")
    error_message = "Truncated names must end with the first 6 hex characters of sha1(full name)."
  }

  assert {
    # The cut point here falls on a hyphen, which is trimmed: 62 characters.
    condition     = endswith(output.names["bucket"], "-0a1b2c3d") && output.names["bucket"] == "sample-logistics-platform-production-telemetry-${substr(sha1("sample-logistics-platform-production-telemetry-ingest-eu1"), 0, 6)}-0a1b2c3d"
    error_message = "The suffix must be kept after the hash-truncated name, with no trailing hyphen before the hash."
  }

  assert {
    condition     = alltrue([for n in values(output.names) : can(regex("^[a-z][a-z0-9-]*[a-z0-9]$", n)) && !strcontains(n, "--")])
    error_message = "Names must start with a letter, end alphanumeric and contain no double hyphens."
  }

  assert {
    # GCP service account ids: ^[a-z](?:[-a-z0-9]{4,28}[a-z0-9])$
    condition     = can(regex("^[a-z][-a-z0-9]{4,28}[a-z0-9]$", output.names["service_account"]))
    error_message = "Service account ids must match the GCP account_id pattern."
  }

  assert {
    condition     = output.names["topic"] == "sample-logistics-platform-production-telemetry-ingest-eu1"
    error_message = "Names within the limit must not be truncated."
  }
}

run "labels_are_sanitized_and_hermes_labels_win" {
  command = plan

  variables {
    project     = "demo"
    environment = "dev"
    labels = {
      "Cost Center"    = "Team A/B"
      "hermes-project" = "spoofed"
      "1team"          = "x"
    }
  }

  assert {
    condition     = output.labels["cost_center"] == "team_a_b"
    error_message = "Label keys and values must be lowercased with invalid characters replaced."
  }

  assert {
    condition     = output.labels["hermes-project"] == "demo" && output.labels["hermes-managed-by"] == "hermes"
    error_message = "Hermes labels must override user labels."
  }

  assert {
    condition     = output.labels["l_1team"] == "x"
    error_message = "Label keys must start with a lowercase letter."
  }

  assert {
    condition     = output.component_labels["database"]["hermes-component"] == "database" && length(output.component_labels) == 7
    error_message = "component_labels must add hermes-component for every component."
  }
}

run "rejects_too_small_budget" {
  command = plan

  variables {
    project     = "demo"
    environment = "dev"
    resources = {
      tiny = { name = "x", max_length = 12, suffix = "0a1b2c3d" }
    }
  }

  expect_failures = [var.resources]
}
