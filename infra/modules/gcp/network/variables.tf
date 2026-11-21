variable "names" {
  description = "Native names: network, subnet, router, nat, psa, firewall_deny_ingress."
  type = object({
    network               = string
    subnet                = string
    router                = string
    nat                   = string
    psa                   = string
    firewall_deny_ingress = string
  })
}

variable "region" {
  description = "Region of the subnet, Cloud Router and Cloud NAT."
  type        = string
}

variable "cidr" {
  description = "Environment address space. First half: subnet; third quarter: private services access; fourth quarter: reserved."
  type        = string
}

variable "nat" {
  description = "Create a Cloud Router + Cloud NAT for private egress."
  type        = bool
}

variable "private_services_access" {
  description = "Reserve a private services access range and peer it with servicenetworking (Cloud SQL private IP)."
  type        = bool
}

variable "flow_logs" {
  description = "Enable VPC flow logs on the subnet, Cloud NAT error logs and firewall logs."
  type        = bool
}
