# Network capability: VPC, one vSwitch per zone, optional enhanced NAT gateway with an
# EIP and one SNAT entry per vSwitch. Security groups live with the workloads that own
# them (container-service), so this module grants no ingress at all.

module "names" {
  source = "../naming"
  names = {
    vpc = { parts = [var.prefix, "vpc"], max_length = 128 }
    nat = { parts = [var.prefix, "nat"], max_length = 128 }
    eip = { parts = [var.prefix, "nat-eip"], max_length = 128 }
  }
}

locals {
  tags = merge(var.tags, { "hermes-component" = "network" })

  # One /20 (for a /16 VPC) per zone; index i -> zone i.
  vswitch_cidrs = [for i, _ in var.zone_ids : cidrsubnet(var.cidr, 4, i)]
}

resource "alicloud_vpc" "this" {
  vpc_name    = module.names.names["vpc"]
  cidr_block  = var.cidr
  description = "Hermes VPC for ${var.prefix}"
  tags        = local.tags
}

resource "alicloud_vswitch" "this" {
  count = length(var.zone_ids)

  vswitch_name = "${module.names.names["vpc"]}-${var.zone_ids[count.index]}"
  vpc_id       = alicloud_vpc.this.id
  cidr_block   = local.vswitch_cidrs[count.index]
  zone_id      = var.zone_ids[count.index]
  description  = "Hermes private vSwitch (zone ${var.zone_ids[count.index]})"
  tags         = local.tags
}

# Enhanced NAT gateways are deployed across zones by default (availability_mode CrossAZ),
# and creating one adds the 0.0.0.0/0 route to the VPC system route table automatically.
resource "alicloud_nat_gateway" "this" {
  count = var.nat ? 1 : 0

  vpc_id               = alicloud_vpc.this.id
  nat_gateway_name     = module.names.names["nat"]
  description          = "Hermes egress NAT for ${var.prefix}"
  nat_type             = "Enhanced"
  network_type         = "internet"
  payment_type         = "PayAsYouGo"
  internet_charge_type = "PayByLcu"
  vswitch_id           = alicloud_vswitch.this[0].id
  availability_mode    = "CrossAZ"
  deletion_protection  = var.protected
  tags                 = local.tags
}

resource "alicloud_eip_address" "nat" {
  count = var.nat ? 1 : 0

  address_name         = module.names.names["eip"]
  description          = "Hermes NAT egress address for ${var.prefix}"
  isp                  = "BGP"
  internet_charge_type = "PayByTraffic"
  payment_type         = "PayAsYouGo"
  bandwidth            = var.nat_bandwidth_mbps
  deletion_protection  = var.protected
  tags                 = local.tags
}

resource "alicloud_eip_association" "nat" {
  count = var.nat ? 1 : 0

  allocation_id = alicloud_eip_address.nat[0].id
  instance_id   = alicloud_nat_gateway.this[0].id
  instance_type = "Nat"
}

resource "alicloud_snat_entry" "this" {
  count = var.nat ? length(var.zone_ids) : 0

  snat_table_id     = alicloud_nat_gateway.this[0].snat_table_ids
  source_vswitch_id = alicloud_vswitch.this[count.index].id
  snat_ip           = alicloud_eip_address.nat[0].ip_address
  snat_entry_name   = "${module.names.names["nat"]}-${count.index}"

  depends_on = [alicloud_eip_association.nat]
}
