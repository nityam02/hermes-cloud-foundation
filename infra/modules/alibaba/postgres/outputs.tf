output "instance_id" {
  description = "RDS instance ID."
  value       = alicloud_db_instance.this.id
}

output "endpoint" {
  description = "Private connection string (host name)."
  value       = alicloud_db_instance.this.connection_string
}

output "port" {
  description = "PostgreSQL port."
  value       = 5432
}

output "database_name" {
  description = "Application database name."
  value       = alicloud_db_database.app.data_base_name
}
