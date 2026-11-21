# Mock-provider tests: address plan, NAT, private services access, firewall. No cloud calls.

mock_provider "google" {}

variables {
  names = {
    network               = "demo-dev-vpc"
    subnet                = "demo-dev-subnet"
    router                = "demo-dev-router"
    nat                   = "demo-dev-nat"
    psa                   = "demo-dev-psa"
    firewall_deny_ingress = "demo-dev-deny-all-ingress"
  }
  region                  = "europe-north1"
  cidr                    = "10.20.0.0/16"
  nat                     = true
  private_services_access = true
  flow_logs               = true
}

run "full_network" {
  command = apply

  assert {
    condition     = google_compute_network.this.auto_create_subnetworks == false && google_compute_subnetwork.this.ip_cidr_range == "10.20.0.0/17" && google_compute_subnetwork.this.private_ip_google_access == true
    error_message = "Custom-mode VPC with the first half of the CIDR as subnet and Private Google Access."
  }

  assert {
    condition     = google_compute_global_address.psa[0].address == "10.20.128.0" && google_compute_global_address.psa[0].prefix_length == 18 && google_compute_global_address.psa[0].purpose == "VPC_PEERING"
    error_message = "Private services access must use the third quarter of the CIDR."
  }

  assert {
    condition     = length(google_compute_router_nat.this) == 1 && length(google_compute_subnetwork.this.log_config) == 1
    error_message = "NAT and flow logs must be created when requested."
  }

  assert {
    condition     = google_compute_firewall.deny_all_ingress.direction == "INGRESS" && contains(google_compute_firewall.deny_all_ingress.source_ranges, "0.0.0.0/0") && length(google_compute_firewall.deny_all_ingress.allow) == 0
    error_message = "Only an explicit deny-all ingress rule may exist."
  }
}

run "minimal_network" {
  command = apply

  variables {
    cidr                    = "10.30.0.0/20"
    nat                     = false
    private_services_access = false
    flow_logs               = false
  }

  assert {
    condition     = length(google_compute_router_nat.this) == 0 && length(google_compute_global_address.psa) == 0 && length(google_compute_subnetwork.this.log_config) == 0
    error_message = "No NAT, private services access or flow logs when not requested."
  }

  assert {
    condition     = output.subnet_cidr == "10.30.0.0/21" && output.private_services_connection == ""
    error_message = "A /20 environment gets a /21 subnet."
  }
}
