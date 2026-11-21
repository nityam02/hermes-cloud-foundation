variable "prefix" {
  description = "Native name prefix: <project>-<environment>."
  type        = string
}

variable "cidr" {
  description = "VPC IPv4 CIDR (/20 or larger)."
  type        = string
}

variable "zone_ids" {
  description = "Zone IDs, one vSwitch per zone (2 or 3)."
  type        = list(string)

  validation {
    condition     = contains([2, 3], length(var.zone_ids))
    error_message = "Provide 2 or 3 zone IDs."
  }
}

variable "nat" {
  description = "Create an enhanced NAT gateway with an EIP and SNAT entries for private egress."
  type        = bool
  default     = true
}

variable "nat_bandwidth_mbps" {
  description = "Peak bandwidth of the NAT EIP (Mbit/s, pay-by-traffic)."
  type        = number
  default     = 100
}

variable "protected" {
  description = "Protected environment: enables deletion protection on the NAT gateway and EIP."
  type        = bool
  default     = false
}

variable "tags" {
  description = "Tags applied to every resource (hermes-component is set by the module)."
  type        = map(string)
  default     = {}
}
