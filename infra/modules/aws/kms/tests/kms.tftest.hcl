mock_provider "aws" {}

variables {
  alias_name              = "alias/hermes/shop-prod"
  description             = "Hermes shop-prod"
  deletion_window_in_days = 30
  account_id              = "123456789012"
  region                  = "eu-north-1"
  tags                    = { hermes-component = "secrets" }
  log_group_patterns      = ["/hermes/shop/prod/*", "/aws/rds/instance/shop-prod/*"]
  sns_topic_arns          = ["arn:aws:sns:eu-north-1:123456789012:shop-prod-orders"]
}

run "rotation_and_policy" {
  command = apply

  assert {
    condition     = aws_kms_key.this.enable_key_rotation && aws_kms_key.this.deletion_window_in_days == 30
    error_message = "The CMK must have rotation enabled and the requested deletion window."
  }

  assert {
    condition = sort([for s in jsondecode(aws_kms_key.this.policy).Statement : s.Sid]) == sort([
      "EnableAccountIamPolicies", "AllowCloudWatchLogsInThisAccount", "AllowSnsToEncryptedQueuesInThisAccount", "AllowCloudWatchAlarmsToEncryptedTopic",
    ])
    error_message = "The key policy must contain exactly the account-root, Logs, SNS and CloudWatch statements."
  }

  assert {
    condition     = [for s in jsondecode(aws_kms_key.this.policy).Statement : s.Principal.AWS if s.Sid == "EnableAccountIamPolicies"][0] == "arn:aws:iam::123456789012:root"
    error_message = "Only the account root may hold kms:* (no wildcard principals)."
  }

  assert {
    condition = [for s in jsondecode(aws_kms_key.this.policy).Statement : s.Condition.ArnLike["kms:EncryptionContext:aws:logs:arn"] if s.Sid == "AllowCloudWatchLogsInThisAccount"][0] == [
      "arn:aws:logs:eu-north-1:123456789012:log-group:/hermes/shop/prod/*",
      "arn:aws:logs:eu-north-1:123456789012:log-group:/aws/rds/instance/shop-prod/*",
    ]
    error_message = "CloudWatch Logs use must be scoped to this environment's log groups (never log-group:*)."
  }

  assert {
    condition = [for s in jsondecode(aws_kms_key.this.policy).Statement : s.Condition if s.Sid == "AllowSnsToEncryptedQueuesInThisAccount"][0] == {
      StringEquals = { "aws:SourceAccount" = "123456789012" }
      ArnLike      = { "aws:SourceArn" = ["arn:aws:sns:eu-north-1:123456789012:shop-prod-orders"] }
    }
    error_message = "SNS may use the key only for this account's environment topics."
  }

  assert {
    condition = [for s in jsondecode(aws_kms_key.this.policy).Statement : s.Condition if s.Sid == "AllowCloudWatchAlarmsToEncryptedTopic"][0] == {
      StringEquals = { "aws:SourceAccount" = "123456789012" }
      ArnLike      = { "aws:SourceArn" = "arn:aws:cloudwatch:eu-north-1:123456789012:alarm:*" }
    }
    error_message = "CloudWatch may use the key only for alarms in this account and region."
  }

  assert {
    condition     = alltrue([for s in jsondecode(aws_kms_key.this.policy).Statement : s.Sid == "EnableAccountIamPolicies" || can(s.Condition)])
    error_message = "Every service-principal statement must carry a condition."
  }
}

run "no_topics_no_sns_statement" {
  command = apply

  variables {
    sns_topic_arns = []
  }

  assert {
    condition     = !contains([for s in jsondecode(aws_kms_key.this.policy).Statement : s.Sid], "AllowSnsToEncryptedQueuesInThisAccount")
    error_message = "Without topics SNS gets no access to the key."
  }
}

run "rejects_wildcard_log_groups" {
  command = plan

  variables {
    log_group_patterns = ["*"]
  }

  expect_failures = [var.log_group_patterns]
}
