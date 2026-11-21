output "vpc_id" {
  description = "VPC id."
  value       = aws_vpc.this.id
}

output "name" {
  description = "VPC Name tag."
  value       = var.name
}

output "cidr" {
  description = "VPC CIDR."
  value       = aws_vpc.this.cidr_block
}

output "availability_zones" {
  description = "Zones used, in subnet order."
  value       = local.azs
}

output "public_subnet_ids" {
  description = "Public subnet ids (ALB, NAT)."
  value       = aws_subnet.public[*].id
}

output "private_subnet_ids" {
  description = "Private subnet ids (tasks, database)."
  value       = aws_subnet.private[*].id
}

output "private_route_table_ids" {
  description = "Private route table ids."
  value       = aws_route_table.private[*].id
}

output "nat_gateway_count" {
  description = "Number of NAT gateways."
  value       = local.nat_count
}

output "flow_log_group_name" {
  description = "Flow-log group name, or \"\" when disabled."
  value       = var.flow_logs_enabled ? aws_cloudwatch_log_group.flow_logs[0].name : ""
}
