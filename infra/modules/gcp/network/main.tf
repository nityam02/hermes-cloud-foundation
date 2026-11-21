# -----------------------------------------------------------------------------
# Custom-mode VPC with one regional subnet. GCP subnets are regional (they span
# every zone of the region), so there is no per-zone subnet layout.
#
# Address plan inside var.cidr:
#   first half      -> subnet (Cloud Run Direct VPC egress uses these addresses)
#   third quarter   -> private services access range (Cloud SQL private IP)
#   fourth quarter  -> reserved for future use (e.g. proxy-only subnet)
#
# Firewall: VPC networks have an implied "deny all ingress" and "allow all egress".
# An explicit low-priority deny-all ingress rule makes the intent visible and
# loggable. No allow-ingress rules are created: nothing in the platform receives
# traffic from inside the VPC (Cloud Run ingress goes through Google's front end;
# Cloud SQL lives in the peered producer network).
# -----------------------------------------------------------------------------

locals {
  subnet_cidr = cidrsubnet(var.cidr, 1, 0)
  psa_cidr    = cidrsubnet(var.cidr, 2, 2)
}

resource "google_compute_network" "this" {
  name                            = var.names.network
  description                     = "Hermes environment network."
  auto_create_subnetworks         = false
  routing_mode                    = "REGIONAL"
  delete_default_routes_on_create = false
}

# Flow logs follow observability.enabled (an explicit opt-out in the spec).
#trivy:ignore:GCP-0029
#trivy:ignore:GCP-0076
resource "google_compute_subnetwork" "this" {
  name                     = var.names.subnet
  description              = "Hermes environment subnet."
  region                   = var.region
  network                  = google_compute_network.this.id
  ip_cidr_range            = local.subnet_cidr
  private_ip_google_access = true
  stack_type               = "IPV4_ONLY"

  dynamic "log_config" {
    for_each = var.flow_logs ? [1] : []
    content {
      aggregation_interval = "INTERVAL_5_MIN"
      flow_sampling        = 0.5
      metadata             = "INCLUDE_ALL_METADATA"
    }
  }
}

# False positive: this rule DENIES all ports; it allows nothing.
#trivy:ignore:GCP-0074
resource "google_compute_firewall" "deny_all_ingress" {
  name          = var.names.firewall_deny_ingress
  description   = "Explicit deny-all ingress (documents the implied rule; allow rules must use a higher priority)."
  network       = google_compute_network.this.id
  direction     = "INGRESS"
  priority      = 65534
  source_ranges = ["0.0.0.0/0"]

  deny {
    protocol = "all"
  }

  dynamic "log_config" {
    for_each = var.flow_logs ? [1] : []
    content {
      metadata = "EXCLUDE_ALL_METADATA"
    }
  }
}

resource "google_compute_router" "this" {
  count = var.nat ? 1 : 0

  name    = var.names.router
  region  = var.region
  network = google_compute_network.this.id
}

# Cloud NAT is a regional, Google-managed (software-defined) service: there is no
# per-zone gateway to make highly available.
resource "google_compute_router_nat" "this" {
  count = var.nat ? 1 : 0

  name                               = var.names.nat
  router                             = google_compute_router.this[0].name
  region                             = var.region
  nat_ip_allocate_option             = "AUTO_ONLY"
  source_subnetwork_ip_ranges_to_nat = "LIST_OF_SUBNETWORKS"

  subnetwork {
    name                    = google_compute_subnetwork.this.id
    source_ip_ranges_to_nat = ["ALL_IP_RANGES"]
  }

  log_config {
    enable = var.flow_logs
    filter = "ERRORS_ONLY"
  }
}

# Private services access: Cloud SQL gets a private IP from this range; no public IP.
resource "google_compute_global_address" "psa" {
  count = var.private_services_access ? 1 : 0

  name          = var.names.psa
  description   = "Private services access range (Cloud SQL)."
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  address       = cidrhost(local.psa_cidr, 0)
  prefix_length = tonumber(split("/", local.psa_cidr)[1])
  network       = google_compute_network.this.id
}

resource "google_service_networking_connection" "psa" {
  count = var.private_services_access ? 1 : 0

  network                 = google_compute_network.this.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.psa[0].name]
  # Deleting the peering fails while producer resources still exist; the peering is
  # removed together with the network on destroy.
  deletion_policy = "ABANDON"
}
