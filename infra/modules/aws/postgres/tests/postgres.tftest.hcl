mock_provider "aws" {
  mock_resource "aws_db_instance" {
    defaults = {
      address     = "shop-dev-db.abcdefghijkl.eu-north-1.rds.amazonaws.com"
      resource_id = "db-ABCDEFGHIJKLMNOPQRSTUVWXY"
      master_user_secret = [{
        secret_arn    = "arn:aws:secretsmanager:eu-north-1:123456789012:secret:rds!db-00000000-0000-4000-8000-000000000000-AbCdEf"
        kms_key_id    = "alias/aws/secretsmanager"
        secret_status = "active"
      }]
    }
  }
}

variables {
  identifier                 = "shop-dev-db"
  engine_version             = "16"
  size                       = "small"
  storage_gb                 = 20
  max_storage_gb             = 100
  high_availability          = false
  backup_retention_days      = 7
  deletion_protection        = false
  protected                  = false
  extensions                 = ["postgis"]
  vpc_id                     = "vpc-0123456789abcdef0"
  subnet_ids                 = ["subnet-0123456789abcdef0", "subnet-0123456789abcdef1"]
  security_group_name        = "shop-dev-db"
  allowed_security_group_ids = { api = "sg-0123456789abcdef0" }
  account_id                 = "123456789012"
  region                     = "eu-north-1"
}

run "secure_defaults" {
  command = apply

  assert {
    condition     = aws_db_instance.this.instance_class == "db.t4g.small" && aws_db_instance.this.engine == "postgres" && aws_db_instance.this.engine_version == "16"
    error_message = "small maps to db.t4g.small on the requested major version."
  }

  assert {
    condition     = aws_db_instance.this.storage_encrypted && !aws_db_instance.this.publicly_accessible && aws_db_instance.this.storage_type == "gp3"
    error_message = "Storage must be encrypted gp3 and the instance private."
  }

  assert {
    condition     = aws_db_instance.this.manage_master_user_password && aws_db_instance.this.iam_database_authentication_enabled && aws_db_instance.this.password == null
    error_message = "Master password must be RDS-managed (never in state) and IAM auth enabled."
  }

  assert {
    condition     = tolist([for p in aws_db_parameter_group.this.parameter : p.value if p.name == "rds.force_ssl"]) == tolist(["1"]) && aws_db_parameter_group.this.family == "postgres16"
    error_message = "rds.force_ssl must be 1 in a postgres16 parameter group."
  }

  assert {
    condition     = aws_db_instance.this.skip_final_snapshot && aws_db_instance.this.final_snapshot_identifier == null && !aws_db_instance.this.multi_az
    error_message = "Unprotected, non-HA: single-AZ and no final snapshot."
  }

  assert {
    condition     = aws_db_instance.this.max_allocated_storage == 100 && aws_db_instance.this.backup_retention_period == 7 && aws_db_instance.this.copy_tags_to_snapshot
    error_message = "Storage autoscaling ceiling, backups and snapshot tags must be configured."
  }

  assert {
    condition     = tolist(sort(aws_db_instance.this.enabled_cloudwatch_logs_exports)) == tolist(["postgresql", "upgrade"]) && aws_cloudwatch_log_group.exports["postgresql"].name == "/aws/rds/instance/shop-dev-db/postgresql"
    error_message = "PostgreSQL logs must be exported into pre-created, retained log groups."
  }

  assert {
    condition     = tolist(keys(aws_vpc_security_group_ingress_rule.from_service)) == tolist(["api"]) && aws_vpc_security_group_ingress_rule.from_service["api"].from_port == 5432 && aws_vpc_security_group_ingress_rule.from_service["api"].referenced_security_group_id == "sg-0123456789abcdef0"
    error_message = "5432 must be open only from the allowed service security groups."
  }

  assert {
    condition     = output.iam_connect_arn == "arn:aws:rds-db:eu-north-1:123456789012:dbuser:db-ABCDEFGHIJKLMNOPQRSTUVWXY/app" && output.auth == "iam" && output.port == 5432
    error_message = "IAM connect ARN must target dbuser app on this instance's resource id."
  }

  assert {
    condition     = startswith(output.master_secret_arn, "arn:aws:secretsmanager:")
    error_message = "credentials_ref must be the RDS-managed master secret ARN."
  }
}

run "protected_high_availability_with_cmk" {
  command = apply

  variables {
    size                 = "medium"
    high_availability    = true
    deletion_protection  = true
    protected            = true
    enhanced_monitoring  = true
    monitoring_role_name = "shop-prod-rds-monitoring"
    alarms_enabled       = true
    alarm_topic_arn      = "arn:aws:sns:eu-north-1:123456789012:shop-prod-alerts"
    kms_key_arn          = "arn:aws:kms:eu-north-1:123456789012:key/00000000-0000-4000-8000-000000000000"
  }

  assert {
    condition     = aws_db_instance.this.multi_az && aws_db_instance.this.instance_class == "db.m7g.large"
    error_message = "high_availability must enable Multi-AZ; medium maps to db.m7g.large."
  }

  assert {
    condition     = aws_db_instance.this.deletion_protection && !aws_db_instance.this.skip_final_snapshot && aws_db_instance.this.final_snapshot_identifier == "shop-dev-db-final" && !aws_db_instance.this.delete_automated_backups
    error_message = "Protected: deletion protection, final snapshot, retained automated backups."
  }

  assert {
    condition = (aws_db_instance.this.kms_key_id == var.kms_key_arn
      && aws_db_instance.this.performance_insights_kms_key_id == var.kms_key_arn
      && aws_db_instance.this.master_user_secret_kms_key_id == var.kms_key_arn
    && aws_cloudwatch_log_group.exports["postgresql"].kms_key_id == var.kms_key_arn)
    error_message = "The CMK must encrypt storage, Performance Insights, the master secret and exported logs."
  }

  assert {
    condition     = aws_db_instance.this.monitoring_interval == 60 && aws_db_instance.this.monitoring_role_arn == "arn:aws:iam::123456789012:role/shop-prod-rds-monitoring"
    error_message = "Enhanced Monitoring must be wired to its role."
  }

  assert {
    condition     = length(aws_cloudwatch_metric_alarm.cpu_high) == 1 && aws_cloudwatch_metric_alarm.free_storage_low[0].threshold == 2147483648
    error_message = "CPU and free-storage alarms must exist (threshold 10% of 20 GiB)."
  }
}

run "instance_class_override" {
  command = plan

  variables {
    instance_class_override = "db.r7g.large"
  }

  assert {
    condition     = aws_db_instance.this.instance_class == "db.r7g.large"
    error_message = "extensions.aws.db_instance_class must override the size map."
  }
}

run "rejects_unknown_extension" {
  command = plan

  variables {
    extensions = ["postgis", "timescaledb"]
  }

  expect_failures = [var.extensions]
}
