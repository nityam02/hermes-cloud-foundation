mock_provider "aws" {}

run "unprotected_defaults" {
  command = apply

  variables {
    secrets = { maps-api-key = "shop-dev-maps-api-key" }
  }

  assert {
    condition     = aws_secretsmanager_secret.this["maps-api-key"].name == "shop-dev-maps-api-key" && aws_secretsmanager_secret.this["maps-api-key"].recovery_window_in_days == 7
    error_message = "Secrets use the native name and a 7-day recovery window by default."
  }

  assert {
    condition     = aws_secretsmanager_secret.this["maps-api-key"].kms_key_id == null
    error_message = "Without a CMK the AWS-managed key is used."
  }

  assert {
    condition     = tolist(keys(output.arns)) == tolist(["maps-api-key"])
    error_message = "Outputs are keyed by spec secret name."
  }
}

run "protected_with_cmk" {
  command = apply

  variables {
    secrets                 = { maps-api-key = "shop-prod-maps-api-key" }
    recovery_window_in_days = 30
    kms_key_arn             = "arn:aws:kms:eu-north-1:123456789012:key/00000000-0000-4000-8000-000000000000"
  }

  assert {
    condition     = aws_secretsmanager_secret.this["maps-api-key"].recovery_window_in_days == 30 && aws_secretsmanager_secret.this["maps-api-key"].kms_key_id == var.kms_key_arn
    error_message = "Protected environments use a 30-day recovery window and the CMK."
  }
}
