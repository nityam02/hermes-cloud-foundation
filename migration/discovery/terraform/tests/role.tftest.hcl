# Mock-provider test: no credentials, no API calls.
mock_provider "aws" {
  mock_data "aws_iam_policy_document" {
    defaults = {
      json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}"
    }
  }
}

variables {
  trusted_principal_arn = "arn:aws:iam::123456789012:role/hermes-operator"
  external_id           = "demo-external-id-0001"
}

run "role_is_short_session_with_external_id" {
  command = plan

  assert {
    condition     = aws_iam_role.discovery.max_session_duration == 3600
    error_message = "discovery sessions must be limited to one hour"
  }

  assert {
    condition     = one(one(data.aws_iam_policy_document.trust.statement).condition).variable == "sts:ExternalId"
    error_message = "the trust policy must require sts:ExternalId"
  }

  assert {
    condition     = strcontains(aws_iam_role_policy.discovery.policy, "HermesDenySecretAndDataReads")
    error_message = "the inline policy must carry the explicit deny statement"
  }
}

run "rejects_short_external_id" {
  command = plan

  variables {
    external_id = "short"
  }

  expect_failures = [var.external_id]
}
