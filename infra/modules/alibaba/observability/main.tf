# Observability capability: a Simple Log Service (SLS) project and logstore that SAE ships
# container stdout to, plus CloudMonitor alerting to an e-mail contact.
#
# Alerts: only RDS CPU and disk usage alarms are created (namespace acs_rds_dashboard).
# SAE and message-queue alarms are not implemented.

module "names" {
  source = "../naming"
  names = {
    # SLS project names are globally unique: 56 + "-" + 6 hex = 63.
    project = { parts = [var.prefix, "logs"], max_length = 56 }
    contact = { parts = [var.prefix, "oncall"], max_length = 40 }
    alarm   = { parts = [var.prefix, "db"], max_length = 120 }
  }
}

resource "random_id" "suffix" {
  byte_length = 3
}

locals {
  tags     = merge(var.tags, { "hermes-component" = "observability" })
  alerting = var.alert_email != ""
}

resource "alicloud_log_project" "this" {
  project_name = "${module.names.names["project"]}-${random_id.suffix.hex}"
  description  = "Hermes logs for ${var.prefix}"
  tags         = local.tags
}

resource "alicloud_log_store" "services" {
  project_name          = alicloud_log_project.this.project_name
  logstore_name         = "services"
  retention_period      = var.log_retention_days
  shard_count           = 2
  auto_split            = true
  max_split_shard_count = 16
  append_meta           = true

  # Encryption at rest with the SLS service key. BYOK (user_cmk_info) needs an extra RAM
  # role for SLS and is not implemented.
  encrypt_conf {
    enable       = true
    encrypt_type = "default"
  }
}

resource "alicloud_cms_alarm_contact" "this" {
  count = local.alerting ? 1 : 0

  alarm_contact_name = module.names.names["contact"]
  describe           = "Hermes on-call contact for ${var.prefix}"
  channels_mail      = var.alert_email
  lang               = "en"
}

resource "alicloud_cms_alarm_contact_group" "this" {
  count = local.alerting ? 1 : 0

  alarm_contact_group_name = module.names.names["contact"]
  describe                 = "Hermes on-call group for ${var.prefix}"
  contacts                 = [alicloud_cms_alarm_contact.this[0].alarm_contact_name]
}

locals {
  db_alarms = local.alerting && var.database_enabled ? {
    cpu  = { metric = "CpuUsage", threshold = "80" }
    disk = { metric = "DiskUsage", threshold = "80" }
  } : {}
}

resource "alicloud_cms_alarm" "database" {
  for_each = local.db_alarms

  name              = "${module.names.names["alarm"]}-${each.key}"
  project           = "acs_rds_dashboard"
  metric            = each.value.metric
  metric_dimensions = jsonencode([{ instanceId = var.database_instance_id }])
  period            = 300
  contact_groups    = [alicloud_cms_alarm_contact_group.this[0].alarm_contact_group_name]
  enabled           = true
  tags              = local.tags

  escalations_critical {
    statistics          = "Average"
    comparison_operator = ">="
    threshold           = each.value.threshold
    times               = 3
  }
}
