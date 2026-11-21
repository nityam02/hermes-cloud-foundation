mock_provider "aws" {
  mock_resource "aws_kms_key" {
    defaults = {
      arn = "arn:aws:kms:eu-north-1:123456789012:key/11111111-1111-4111-8111-111111111111"
    }
  }
}

variables {
  log_group_prefix   = "/hermes/shop/dev"
  service_names      = ["api", "worker"]
  log_retention_days = 30
  alerts_enabled     = true
  alert_topic_name   = "shop-dev-alerts"
  alert_email        = "oncall@example.com"
  account_id         = "123456789012"
  region             = "eu-north-1"
}

run "log_groups_and_alerts" {
  command = apply

  assert {
    condition     = output.service_log_groups["api"].name == "/hermes/shop/dev/services/api" && aws_cloudwatch_log_group.service["worker"].retention_in_days == 30
    error_message = "Each service gets a retained log group under the environment prefix."
  }

  assert {
    condition     = output.alert_topic_arn == "arn:aws:sns:eu-north-1:123456789012:shop-dev-alerts" && aws_sns_topic_subscription.email[0].protocol == "email"
    error_message = "The alert topic and the e-mail subscription must exist."
  }

  # HCF-POL-002: the alert topic is always encrypted. Without the environment CMK a dedicated
  # key is created (CloudWatch alarms cannot publish to a topic using alias/aws/sns).
  assert {
    condition     = length(aws_kms_key.alerts) == 1 && aws_sns_topic.alerts[0].kms_master_key_id == aws_kms_key.alerts[0].arn && output.alert_key_arn == aws_kms_key.alerts[0].arn
    error_message = "Without a CMK the alert topic must be encrypted with a dedicated key."
  }

  assert {
    condition     = aws_sns_topic.alerts[0].kms_master_key_id != null && aws_sns_topic.alerts[0].kms_master_key_id != "alias/aws/sns"
    error_message = "The alert topic must never be unencrypted or use alias/aws/sns."
  }

  assert {
    condition     = aws_kms_key.alerts[0].enable_key_rotation && aws_kms_key.alerts[0].deletion_window_in_days == 30
    error_message = "The dedicated alert key rotates and keeps the default deletion window."
  }

  # Key policy: account root, CloudWatch alarms of this account/region, SNS for this topic only.
  assert {
    condition = alltrue([
      for s in jsondecode(aws_kms_key.alerts[0].policy).Statement :
      s.Principal == { AWS = "arn:aws:iam::123456789012:root" } || (
        s.Condition.StringEquals["aws:SourceAccount"] == "123456789012" && length(s.Action) == 2
      )
    ])
    error_message = "Every service statement in the alert key policy is limited to this account and to GenerateDataKey*/Decrypt."
  }

  assert {
    condition = (
      [for s in jsondecode(aws_kms_key.alerts[0].policy).Statement : s.Condition.ArnLike["aws:SourceArn"] if try(s.Principal.Service, "") == "cloudwatch.amazonaws.com"][0] == "arn:aws:cloudwatch:eu-north-1:123456789012:alarm:*" &&
      [for s in jsondecode(aws_kms_key.alerts[0].policy).Statement : s.Condition.ArnLike["aws:SourceArn"] if try(s.Principal.Service, "") == "sns.amazonaws.com"][0] == "arn:aws:sns:eu-north-1:123456789012:shop-dev-alerts"
    )
    error_message = "CloudWatch may use the key only for alarms in this account/region; SNS only for the alert topic."
  }

  assert {
    condition     = contains([for s in jsondecode(aws_sns_topic_policy.alerts[0].policy).Statement : s.Sid], "DenyInsecureTransport")
    error_message = "The alert topic must deny non-TLS publishing."
  }
}

run "cmk_and_no_email" {
  command = apply

  variables {
    alert_email = ""
    kms_enabled = true
    kms_key_arn = "arn:aws:kms:eu-north-1:123456789012:key/00000000-0000-4000-8000-000000000000"
  }

  assert {
    condition     = length(aws_sns_topic_subscription.email) == 0 && aws_sns_topic.alerts[0].kms_master_key_id == var.kms_key_arn && aws_cloudwatch_log_group.service["api"].kms_key_id == var.kms_key_arn
    error_message = "With a CMK, log groups and the alert topic use it; no e-mail means no subscription."
  }

  assert {
    condition     = length(aws_kms_key.alerts) == 0 && output.alert_key_policy == null
    error_message = "With the environment CMK no dedicated alert key is created."
  }
}

run "alerts_disabled" {
  command = apply

  variables {
    alerts_enabled = false
  }

  assert {
    condition     = output.alert_topic_arn == null && length(aws_sns_topic.alerts) == 0 && length(output.service_log_groups) == 2
    error_message = "Disabling alerts removes the topic but keeps service log groups."
  }

  assert {
    condition     = length(aws_kms_key.alerts) == 0 && output.alert_key_arn == null
    error_message = "Without alerts there is no alert key."
  }
}

run "rejects_cmk_flag_without_key" {
  command = plan

  variables {
    kms_enabled = true
    kms_key_arn = null
  }

  expect_failures = [var.kms_key_arn]
}
