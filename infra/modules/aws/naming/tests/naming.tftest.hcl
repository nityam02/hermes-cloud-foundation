# Naming unit tests. No providers are involved.

run "short_names_are_untouched" {
  command = plan

  variables {
    project     = "shop"
    environment = "dev"
    names = {
      cluster = { name = "", max_length = 255 }
      api_tg  = { name = "api", max_length = 32 }
    }
  }

  assert {
    condition     = output.names["cluster"] == "shop-dev" && output.names["api_tg"] == "shop-dev-api"
    error_message = "Names within limits must be <project>-<environment>[-<name>] verbatim."
  }
}

run "long_names_are_truncated_with_hash" {
  command = plan

  variables {
    project     = "sample-logistics-platform"
    environment = "production"
    names = {
      tg        = { name = "abcdefghijklmnopqrst", max_length = 32 }
      alb       = { name = "web", max_length = 32 }
      role      = { name = "abcdefghijklmnopqrst-exec", max_length = 64 }
      db        = { name = "db", max_length = 63 }
      queue     = { name = "abcdefghijklmnopqrst", max_length = 76 }
      bucket    = { name = "abcdefghijklmnopqrst", max_length = 54 }
      tg_other  = { name = "abcdefghijklmnopqrsu", max_length = 32 }
      max_case  = { name = "abcdefghijklmnopqrst-task", max_length = 64 }
      exact_fit = { name = "x", max_length = 38 }
    }
  }

  assert {
    condition     = alltrue([for k, v in output.names : length(v) <= { tg = 32, alb = 32, role = 64, db = 63, queue = 76, bucket = 54, tg_other = 32, max_case = 64, exact_fit = 38 }[k]])
    error_message = "Every name must fit its provider limit."
  }

  assert {
    condition     = output.names["tg"] == "sample-logistics-platform-${substr(sha1("sample-logistics-platform-production-abcdefghijklmnopqrst"), 0, 6)}"
    error_message = "Truncated names must be the prefix cut to max_length-7 (trailing hyphens removed) plus '-' and 6 hex chars of sha1(full)."
  }

  assert {
    condition     = output.names["tg"] != output.names["tg_other"]
    error_message = "Names that only differ after the truncation point must still be distinct."
  }

  assert {
    condition     = output.names["exact_fit"] == "sample-logistics-platform-production-x" && output.names["role"] == "sample-logistics-platform-production-abcdefghijklmnopqrst-exec"
    error_message = "Names that fit exactly must not be truncated."
  }

  assert {
    condition     = alltrue([for v in values(output.names) : can(regex("^[a-z][a-z0-9-]*[a-z0-9]$", v)) && !strcontains(v, "--")])
    error_message = "Names must start with a letter, end alphanumeric and contain no double hyphens (RDS/ALB rules)."
  }
}

run "maximum_length_inputs" {
  command = plan

  variables {
    project     = "abcdefghijklmnopqrstuvwxyz0123"
    environment = "environment1"
    names = {
      role   = { name = "abcdefghijklmnopqrst-exec", max_length = 64 }
      tg     = { name = "abcdefghijklmnopqrst", max_length = 32 }
      bucket = { name = "abcdefghijklmnopqrst", max_length = 54 }
    }
  }

  assert {
    condition     = length(output.names["role"]) == 64 && length(output.names["tg"]) == 32 && length(output.names["bucket"]) == 54
    error_message = "30-char project + 12-char environment + 20-char names must truncate to exactly the limit."
  }
}

run "double_hyphens_are_collapsed" {
  command = plan

  variables {
    project     = "a--b"
    environment = "dev"
    names       = { db = { name = "db", max_length = 63 } }
  }

  assert {
    condition     = output.names["db"] == "a-b-dev-db"
    error_message = "Repeated hyphens must be collapsed."
  }
}

run "rejects_tiny_limits" {
  command = plan

  variables {
    project     = "shop"
    environment = "dev"
    names       = { x = { name = "x", max_length = 8 } }
  }

  expect_failures = [var.names]
}
