output "vpc_id" {
  description = "VPC ID."
  value       = alicloud_vpc.this.id
}

output "vpc_name" {
  description = "VPC name."
  value       = alicloud_vpc.this.vpc_name
}

output "cidr" {
  description = "VPC CIDR."
  value       = var.cidr
}

output "vswitch_ids" {
  description = "vSwitch IDs, in zone order."
  value       = alicloud_vswitch.this[*].id
}

output "zone_ids" {
  description = "Zone IDs, in vSwitch order."
  value       = var.zone_ids
}

output "nat_gateway_id" {
  description = "NAT gateway ID, or null when nat = false."
  value       = one(alicloud_nat_gateway.this[*].id)
}
