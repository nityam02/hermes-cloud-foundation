# Mock-provider tests: they prove wiring and policy contents, NOT real Alibaba Cloud behaviour.
mock_provider "alicloud" {
  mock_data "alicloud_account" {
    defaults = { id = "1234567890123456" }
  }
  mock_resource "alicloud_ims_oidc_provider" {
    defaults = { arn = "acs:ram::1234567890123456:oidc-provider/hermes-github" }
  }
}

variables {
  project                  = "sample-platform"
  github_repository        = "example-org/example-repo"
  environments             = ["staging", "production"]
  state_bucket             = "example-hermes-state"
  lock_tablestore_instance = "example-hermes-lock"
  oidc_fingerprints        = ["0000000000000000000000000000000000000000"]
}

run "trust_pins_issuer_audience_subject" {
  command = apply

  assert {
    condition = jsondecode(alicloud_ram_role.plan.assume_role_policy_document).Statement[0].Condition.StringEquals == {
      "oidc:iss" = "https://token.actions.githubusercontent.com"
      "oidc:aud" = "sts.aliyuncs.com"
      "oidc:sub" = ["repo:example-org/example-repo:pull_request", "repo:example-org/example-repo:ref:refs/heads/main"]
    }
    error_message = "The plan trust policy must pin issuer, audience and exact subjects."
  }

  assert {
    condition     = jsondecode(alicloud_ram_role.apply["production"].assume_role_policy_document).Statement[0].Condition.StringEquals["oidc:sub"] == ["repo:example-org/example-repo:environment:production"]
    error_message = "Each apply role must trust only its environment subject."
  }

  assert {
    condition     = alltrue([for r in concat([alicloud_ram_role.plan], values(alicloud_ram_role.apply)) : r.max_session_duration == 3600 && !strcontains(r.assume_role_policy_document, "*") && startswith(r.role_name, "hermes-ci-sample-platform-")])
    error_message = "CI roles: 1-hour sessions, no wildcards in trust, hermes-ci-<project>- names."
  }
}

run "policies_have_no_wildcard_data_access" {
  command = apply

  assert {
    condition = alltrue([for p in concat([alicloud_ram_policy.plan], values(alicloud_ram_policy.apply)) :
      alltrue([for s in jsondecode(p.policy_document).Statement :
        !contains(s.Action, "*") && !(contains(s.Resource, "*") && length([for a in s.Action : a if can(regex("^(oss|mns|log|ots):", a)) && !can(regex("^[a-z]+:(Get|List|Describe)", a))]) > 0)
    if s.Effect == "Allow"])])
    error_message = "No Allow statement may grant Action \"*\" or write data-plane actions on Resource \"*\"."
  }

  assert {
    condition     = one([for s in jsondecode(alicloud_ram_policy.apply["staging"].policy_document).Statement : s.Resource if s.Effect == "Allow" && contains(s.Action, "oss:PutObject")]) == ["acs:oss:*:1234567890123456:example-hermes-state/hermes/sample-platform/staging/*"]
    error_message = "The staging apply role may only write staging state."
  }

  assert {
    condition     = length([for s in jsondecode(alicloud_ram_policy.plan.policy_document).Statement : s if s.Effect == "Allow" && contains(s.Action, "oss:PutObject")]) == 0
    error_message = "The plan role cannot write state objects (locking uses Tablestore rows)."
  }

  assert {
    condition     = length([for s in jsondecode(alicloud_ram_policy.plan.policy_document).Statement : s if s.Effect == "Deny" && contains(s.Action, "kms:GetSecretValue")]) == 1
    error_message = "Reading secret values must be explicitly denied."
  }

  assert {
    condition     = one([for s in jsondecode(alicloud_ram_policy.apply["production"].policy_document).Statement : s.Resource if s.Effect == "Deny" && contains(s.Action, "ram:*")])[0] == "acs:ram::1234567890123456:role/hermes-ci-sample-platform-*"
    error_message = "The CI roles must not be able to modify themselves."
  }
}

run "existing_provider_without_lock" {
  command = apply

  variables {
    create_oidc_provider     = false
    oidc_provider_arn        = "acs:ram::1234567890123456:oidc-provider/shared-github"
    lock_tablestore_instance = ""
  }

  assert {
    condition     = length(alicloud_ims_oidc_provider.github) == 0 && jsondecode(alicloud_ram_role.plan.assume_role_policy_document).Statement[0].Principal.Federated == ["acs:ram::1234567890123456:oidc-provider/shared-github"]
    error_message = "An existing provider ARN is used as the federated principal."
  }

  assert {
    condition     = !strcontains(alicloud_ram_policy.plan.policy_document, "acs:ots")
    error_message = "No Tablestore permissions without a lock instance."
  }
}

run "provider_requires_fingerprints" {
  command = plan

  variables {
    oidc_fingerprints = []
  }

  expect_failures = [alicloud_ims_oidc_provider.github]
}
