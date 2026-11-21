# Pure unit tests for the naming module (no providers involved).

run "short_names_are_unchanged" {
  command = apply

  variables {
    names = {
      vpc = { parts = ["shop", "dev", "vpc"], max_length = 128 }
      ns  = { parts = ["shop", "dev"], max_length = 20, delimiter = "" }
    }
  }

  assert {
    condition     = output.names["vpc"] == "shop-dev-vpc" && output.names["ns"] == "shopdev"
    error_message = "Names within the limit must be <project>-<environment>-<name> verbatim."
  }
}

run "long_names_are_hash_truncated_within_limits" {
  command = apply

  variables {
    names = {
      # 25 + 10 + 20 characters: the long-name case from the brief.
      app    = { parts = ["sample-logistics-platform", "production", "telemetry-ingest-api"], max_length = 36 }
      bucket = { parts = ["sample-logistics-platform", "production", "telemetry-ingest-api"], max_length = 56 }
      ns     = { parts = ["samplelogisticsplatform", "production"], max_length = 20, delimiter = "" }
      alarm  = { parts = ["sample-logistics-platform", "production", "oncall"], max_length = 40 }
      ots    = { parts = ["sample-logistics-platform", "tf"], max_length = 16 }
    }
  }

  assert {
    condition     = length(output.names["app"]) <= 36 && length(output.names["bucket"]) <= 56 && length(output.names["ns"]) <= 20 && length(output.names["alarm"]) <= 40 && length(output.names["ots"]) <= 16
    error_message = "Truncated names must respect max_length."
  }

  assert {
    condition     = output.names["app"] == format("sample-logistics-platform-pro-%s", substr(sha1("sample-logistics-platform-production-telemetry-ingest-api"), 0, 6))
    error_message = "Truncation must keep a prefix and append '-' + 6 hex characters of sha1(full name)."
  }

  assert {
    condition     = can(regex("^[a-z0-9]{20}$", output.names["ns"]))
    error_message = "Delimiter-less names must stay delimiter-less (SAE namespace short ids allow only [a-z0-9])."
  }

  assert {
    condition     = alltrue([for n in values(output.names) : !can(regex("--", n)) && !endswith(n, "-")])
    error_message = "Truncation must not produce double or trailing hyphens."
  }
}

run "distinct_long_names_stay_distinct" {
  command = apply

  variables {
    names = {
      a = { parts = ["sample-logistics-platform", "production", "telemetry-ingest-aaa"], max_length = 36 }
      b = { parts = ["sample-logistics-platform", "production", "telemetry-ingest-bbb"], max_length = 36 }
    }
  }

  assert {
    condition     = output.names["a"] != output.names["b"]
    error_message = "Two names that share a long prefix must not collide after truncation."
  }
}
