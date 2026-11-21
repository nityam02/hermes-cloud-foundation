# Database capability: ApsaraDB RDS for PostgreSQL on ESSD cloud disks, private only.
#
# No database account is created here: the provider would need the password in state.
# The application account ("app") is created out-of-band and its credentials stored in
# the secret named by credentials_ref (see docs/providers/alibaba.md).

module "names" {
  source = "../naming"
  names = {
    instance = { parts = [var.prefix, "pg"], max_length = 64 }
  }
}

locals {
  tags = merge(var.tags, { "hermes-component" = "database" })

  # Instance classes (general-purpose, cloud disks), verified against the RDS for
  # PostgreSQL primary instance type list. Basic Edition has no verified 8-core/32 GB class.
  classes = {
    HighAvailability = {
      small  = "pg.n2.2c.2m" # 2 vCPU, 4 GB
      medium = "pg.n4.2c.2m" # 2 vCPU, 8 GB
      large  = "pg.n4.4c.2m" # 4 vCPU, 16 GB
      xlarge = "pg.n4.8c.2m" # 8 vCPU, 32 GB
    }
    Basic = {
      small  = "pg.n2.2c.1m" # 2 vCPU, 4 GB
      medium = "pg.n4.2c.1m" # 2 vCPU, 8 GB
      large  = "pg.n4.4c.1m" # 4 vCPU, 16 GB
    }
  }

  category      = var.high_availability ? "HighAvailability" : "Basic"
  instance_type = var.instance_type != "" ? var.instance_type : lookup(local.classes[local.category], var.size, "")

  # RDS keeps data backups for 7-730 days; the portable contract allows 1-35.
  backup_retention_days = max(7, var.backup_retention_days)

  # Log backup (needed for point-in-time recovery) cannot be configured on Basic Edition.
  log_backup = var.high_availability ? var.point_in_time_recovery : null
}

resource "alicloud_db_instance" "this" {
  engine                   = "PostgreSQL"
  engine_version           = "${var.engine_version}.0"
  instance_type            = local.instance_type
  instance_storage         = max(20, var.storage_gb)
  db_instance_storage_type = "cloud_essd"
  instance_charge_type     = "Postpaid"
  category                 = local.category
  instance_name            = module.names.names["instance"]

  vpc_id          = var.vpc_id
  zone_id         = var.zone_ids[0]
  zone_id_slave_a = var.high_availability ? var.zone_ids[1] : null
  vswitch_id      = var.high_availability ? join(",", slice(var.vswitch_ids, 0, 2)) : var.vswitch_ids[0]

  # Private access only: the allow-list is the VPC CIDR; no public connection is created.
  security_ips = [var.vpc_cidr]

  # TLS in transit: SSL on with the Alibaba-issued certificate, and non-TLS connections refused.
  ssl_action       = "Open"
  ca_type          = "aliyun"
  force_encryption = "1"

  # Encryption at rest of the cloud disks with the customer-managed key (when provided).
  encryption_key = var.kms_key_id

  storage_auto_scale  = var.max_storage_gb > var.storage_gb ? "Enable" : "Disable"
  storage_threshold   = var.max_storage_gb > var.storage_gb ? 20 : null
  storage_upper_bound = var.max_storage_gb > var.storage_gb ? var.max_storage_gb : null

  # Minor versions (security fixes) are applied automatically in the maintenance window.
  auto_upgrade_minor_version = "Auto"

  # SQL audit (SQL Explorer) with 180-day retention; billed by stored volume.
  sql_collector_status       = "Enabled"
  sql_collector_config_value = 180

  # Connection and duration logging (CIS-aligned; statement text is not logged).
  parameters {
    name  = "log_connections"
    value = "on"
  }
  parameters {
    name  = "log_disconnections"
    value = "on"
  }
  parameters {
    name  = "log_duration"
    value = "on"
  }

  deletion_protection = var.deletion_protection
  tags                = local.tags

  lifecycle {
    precondition {
      condition     = local.instance_type != ""
      error_message = "No verified RDS for PostgreSQL Basic Edition class for size ${var.size}: enable database.high_availability or set extensions.alibaba.db_instance_type."
    }

    precondition {
      condition     = !var.high_availability || length(var.zone_ids) >= 2
      error_message = "High availability needs at least two zones."
    }
  }
}

resource "alicloud_db_backup_policy" "this" {
  instance_id             = alicloud_db_instance.this.id
  preferred_backup_period = ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"]
  preferred_backup_time   = "02:00Z-03:00Z"
  backup_retention_period = local.backup_retention_days

  enable_backup_log           = local.log_backup
  log_backup_retention_period = local.log_backup == true ? local.backup_retention_days : null

  # Protected environments keep the latest archived backup after the instance is released
  # (the closest RDS equivalent of a final snapshot). "Lastest" is the provider's spelling.
  released_keep_policy = var.protected ? "Lastest" : "None"
}

resource "alicloud_db_database" "app" {
  instance_id    = alicloud_db_instance.this.id
  data_base_name = var.database_name
  character_set  = "UTF8,C,en_US.utf8"
  description    = "Hermes application database"
}
