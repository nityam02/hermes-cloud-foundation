# RDS for PostgreSQL module tests (mock provider only; no cloud API is called).

mock_provider "alicloud" {}

variables {
  prefix                 = "shop-dev"
  engine_version         = "16"
  size                   = "small"
  storage_gb             = 20
  max_storage_gb         = 100
  high_availability      = false
  backup_retention_days  = 1
  point_in_time_recovery = true
  deletion_protection    = false
  vpc_id                 = "vpc-mock"
  vpc_cidr               = "10.20.0.0/16"
  vswitch_ids            = ["vsw-a", "vsw-b"]
  zone_ids               = ["eu-central-1a", "eu-central-1b"]
}

run "basic_edition_is_single_zone_and_private" {
  command = apply

  assert {
    condition     = alicloud_db_instance.this.category == "Basic" && alicloud_db_instance.this.instance_type == "pg.n2.2c.1m" && alicloud_db_instance.this.vswitch_id == "vsw-a"
    error_message = "Without high availability the instance must be Basic Edition in the first zone."
  }

  assert {
    condition     = alicloud_db_instance.this.engine == "PostgreSQL" && alicloud_db_instance.this.engine_version == "16.0" && alicloud_db_instance.this.db_instance_storage_type == "cloud_essd"
    error_message = "Engine, Alibaba version format and ESSD storage must be set."
  }

  assert {
    condition     = tolist(alicloud_db_instance.this.security_ips) == tolist(["10.20.0.0/16"])
    error_message = "Only the VPC CIDR may be allow-listed (no public access)."
  }

  assert {
    condition     = alicloud_db_instance.this.ssl_action == "Open" && tostring(alicloud_db_instance.this.force_encryption) == "1"
    error_message = "TLS must be enabled and enforced."
  }

  assert {
    condition = (alicloud_db_instance.this.sql_collector_status == "Enabled" && alicloud_db_instance.this.sql_collector_config_value >= 180
      && alicloud_db_instance.this.auto_upgrade_minor_version == "Auto"
    && { for p in alicloud_db_instance.this.parameters : p.name => p.value } == { log_connections = "on", log_disconnections = "on", log_duration = "on" })
    error_message = "RDS must keep SQL audit for at least 180 days, apply minor upgrades and log connections, disconnections and durations."
  }

  assert {
    condition     = tostring(alicloud_db_backup_policy.this.backup_retention_period) == "7"
    error_message = "Retention below 7 days must be raised to the RDS minimum of 7."
  }

  assert {
    condition     = alicloud_db_instance.this.storage_auto_scale == "Enable" && tostring(alicloud_db_instance.this.storage_upper_bound) == "100"
    error_message = "Storage auto-scaling must cap at max_storage_gb."
  }

  assert {
    condition     = alicloud_db_database.app.data_base_name == "app" && alicloud_db_database.app.character_set == "UTF8,C,en_US.utf8"
    error_message = "The app database must be created with UTF8."
  }
}

run "high_availability_spans_two_zones_with_pitr" {
  command = apply

  variables {
    high_availability = true
    size              = "xlarge"
  }

  assert {
    condition     = alicloud_db_instance.this.category == "HighAvailability" && alicloud_db_instance.this.zone_id_slave_a == "eu-central-1b" && alicloud_db_instance.this.vswitch_id == "vsw-a,vsw-b"
    error_message = "High availability must place the secondary node in the second zone."
  }

  assert {
    condition     = alicloud_db_instance.this.instance_type == "pg.n4.8c.2m"
    error_message = "xlarge must map to the 8 vCPU / 32 GB High-availability class."
  }

  assert {
    condition     = tostring(alicloud_db_backup_policy.this.enable_backup_log) == "true" && tostring(alicloud_db_backup_policy.this.log_backup_retention_period) == "7"
    error_message = "Point-in-time recovery must enable log backup on High-availability Edition."
  }
}

run "protected_cmk_and_deletion_protection" {
  command = apply

  variables {
    protected           = true
    deletion_protection = true
    kms_key_id          = "key-mock-123"
  }

  assert {
    condition     = tostring(alicloud_db_instance.this.deletion_protection) == "true" && alicloud_db_backup_policy.this.released_keep_policy == "Lastest"
    error_message = "Protected databases need deletion protection and a retained backup after release."
  }

  assert {
    condition     = alicloud_db_instance.this.encryption_key == "key-mock-123"
    error_message = "The customer-managed key must encrypt the cloud disks."
  }
}

run "xlarge_basic_without_override_fails" {
  command = plan

  variables {
    size = "xlarge"
  }

  expect_failures = [alicloud_db_instance.this]
}
