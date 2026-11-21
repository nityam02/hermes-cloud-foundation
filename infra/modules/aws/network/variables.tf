variable "name" {
  description = "Name prefix (\"<project>-<environment>\") used for Name tags."
  type        = string
}

variable "cidr" {
  description = "VPC IPv4 CIDR (/16 to /20)."
  type        = string
}

variable "availability_zones" {
  description = "Available zone names in the region (at least zone_count)."
  type        = list(string)
}

variable "zone_count" {
  description = "Number of availability zones to span (2 or 3)."
  type        = number
}

variable "nat" {
  description = "Create NAT gateway(s) for private egress."
  type        = bool
  default     = true
}

variable "nat_per_az" {
  description = "One NAT gateway per zone (HA) instead of a single shared one."
  type        = bool
  default     = false
}

variable "flow_logs_enabled" {
  description = "Send VPC flow logs to CloudWatch Logs."
  type        = bool
  default     = true
}

variable "flow_log_group_name" {
  description = "CloudWatch log group name for flow logs."
  type        = string
}

variable "flow_log_role_name" {
  description = "IAM role name used by the flow-log delivery service."
  type        = string
}

variable "log_retention_days" {
  description = "Flow-log retention (a CloudWatch-supported value)."
  type        = number
  default     = 30
}

variable "kms_key_arn" {
  description = "CMK for the flow-log group; null uses CloudWatch Logs default encryption."
  type        = string
  default     = null
}

variable "account_id" {
  description = "AWS account id."
  type        = string
}

variable "partition" {
  description = "AWS partition."
  type        = string
  default     = "aws"
}

variable "region" {
  description = "AWS region."
  type        = string
}

variable "tags" {
  description = "Tags for every resource."
  type        = map(string)
  default     = {}
}
