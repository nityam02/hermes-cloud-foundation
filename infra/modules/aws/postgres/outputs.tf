output "address" {
  description = "Endpoint host name."
  value       = aws_db_instance.this.address
}

output "port" {
  description = "Port."
  value       = local.port
}

output "db_name" {
  description = "Application database name."
  value       = local.db_name
}

output "app_user" {
  description = "Application database user (IAM authentication; created out-of-band, see docs)."
  value       = local.app_user
}

output "auth" {
  description = "Application authentication mode."
  value       = "iam"
}

output "master_secret_arn" {
  description = "ARN of the RDS-managed master-user secret (\"\" until RDS reports it)."
  value       = try(aws_db_instance.this.master_user_secret[0].secret_arn, "")
}

output "identifier" {
  description = "Instance identifier."
  value       = aws_db_instance.this.identifier
}

output "iam_connect_arn" {
  description = "rds-db:connect resource ARN for the application user."
  value       = "arn:${var.partition}:rds-db:${var.region}:${var.account_id}:dbuser:${aws_db_instance.this.resource_id}/${local.app_user}"
}

output "security_group_id" {
  description = "Database security group id."
  value       = aws_security_group.this.id
}

output "allowed_services" {
  description = "Spec service names allowed to connect (sorted)."
  value       = sort(keys(var.allowed_security_group_ids))
}

output "instance_class" {
  description = "Resolved instance class."
  value       = local.instance_class
}

output "extensions" {
  description = "Validated extensions to create after provisioning (CREATE EXTENSION; see docs/providers/aws.md)."
  value       = var.extensions
}
