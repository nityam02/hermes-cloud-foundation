# Network module tests (mock provider only; no cloud API is called).

mock_provider "alicloud" {}

variables {
  prefix   = "shop-dev"
  cidr     = "10.20.0.0/16"
  zone_ids = ["eu-central-1a", "eu-central-1b", "eu-central-1c"]
}

run "one_vswitch_per_zone_with_cross_zone_nat" {
  command = apply

  assert {
    condition     = length(alicloud_vswitch.this) == 3 && alicloud_vswitch.this[1].zone_id == "eu-central-1b" && alicloud_vswitch.this[1].cidr_block == "10.20.16.0/20"
    error_message = "Each zone must get its own non-overlapping vSwitch."
  }

  assert {
    condition     = alicloud_nat_gateway.this[0].nat_type == "Enhanced" && alicloud_nat_gateway.this[0].availability_mode == "CrossAZ" && length(alicloud_snat_entry.this) == 3
    error_message = "NAT must be an enhanced, cross-zone gateway with an SNAT entry per vSwitch."
  }

  assert {
    condition     = alicloud_nat_gateway.this[0].deletion_protection == false
    error_message = "Non-protected environments must not block deletion."
  }
}

run "protected_enables_deletion_protection" {
  command = apply

  variables {
    protected = true
  }

  assert {
    condition     = alicloud_nat_gateway.this[0].deletion_protection == true && alicloud_eip_address.nat[0].deletion_protection == true
    error_message = "Protected environments must protect the NAT gateway and its EIP from deletion."
  }
}

run "no_nat" {
  command = apply

  variables {
    nat = false
  }

  assert {
    condition     = length(alicloud_nat_gateway.this) == 0 && length(alicloud_eip_address.nat) == 0 && length(alicloud_snat_entry.this) == 0 && output.nat_gateway_id == null
    error_message = "nat = false must create no NAT gateway, EIP or SNAT entries."
  }
}
