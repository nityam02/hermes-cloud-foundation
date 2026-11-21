output "log_project" {
  description = "SLS project name."
  value       = alicloud_log_project.this.project_name
}

output "logstore" {
  description = "SLS logstore that receives service stdout."
  value       = alicloud_log_store.services.logstore_name
}

output "alert_channel" {
  description = "CloudMonitor alarm contact group name, or \"\" when alerting is off."
  value       = local.alerting ? alicloud_cms_alarm_contact_group.this[0].alarm_contact_group_name : ""
}
