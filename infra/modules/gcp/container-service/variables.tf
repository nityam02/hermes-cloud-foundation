variable "name" {
  description = "Native Cloud Run service name (<= 49 characters)."
  type        = string
}

variable "region" {
  description = "Cloud Run region."
  type        = string
}

variable "service_account_email" {
  description = "Dedicated runtime service account."
  type        = string
}

variable "image" {
  description = "Container image (tag or digest)."
  type        = string
}

variable "port" {
  description = "Container port receiving requests."
  type        = number
  default     = 8080
}

variable "cpu" {
  description = "Requested vCPU (rounded up to a Cloud Run value; see locals)."
  type        = number
  default     = 1
}

variable "memory_mb" {
  description = "Requested memory in MiB (raised to the Cloud Run minimum for the CPU)."
  type        = number
  default     = 512
}

variable "min_instances" {
  description = "Minimum instances (0 = scale to zero)."
  type        = number
  default     = 0
}

variable "max_instances" {
  description = "Maximum instances."
  type        = number
  default     = 3
}

variable "public" {
  description = "Public HTTPS ingress (INGRESS_TRAFFIC_ALL + allUsers invoker). Otherwise internal-only, no public invoker."
  type        = bool
  default     = false
}

variable "health_check_path" {
  description = "HTTP path for the startup and liveness probes."
  type        = string
  default     = "/healthz"
}

variable "command" {
  description = "Entrypoint override ([] keeps the image entrypoint)."
  type        = list(string)
  default     = []
}

variable "env" {
  description = "Plain environment variables (binding contract + spec env)."
  type        = map(string)
  default     = {}
}

variable "secret_env" {
  description = "Env var name -> Secret Manager secret id (same project); injected from version \"latest\"."
  type        = map(string)
  default     = {}
}

variable "network" {
  description = "VPC network name for Direct VPC egress."
  type        = string
}

variable "subnetwork" {
  description = "Subnet name for Direct VPC egress."
  type        = string
}

variable "vpc_egress" {
  description = "PRIVATE_RANGES_ONLY (internet egress via Google) or ALL_TRAFFIC (all egress through the VPC / Cloud NAT)."
  type        = string
  default     = "PRIVATE_RANGES_ONLY"

  validation {
    condition     = contains(["PRIVATE_RANGES_ONLY", "ALL_TRAFFIC"], var.vpc_egress)
    error_message = "vpc_egress must be PRIVATE_RANGES_ONLY or ALL_TRAFFIC."
  }
}

variable "deletion_protection" {
  description = "Protect the service from deletion."
  type        = bool
  default     = false
}

variable "otel_collector" {
  description = "OpenTelemetry Collector sidecar (OTLP on localhost:4317/4318 -> Cloud Trace / Cloud Monitoring)."
  type = object({
    enabled = bool
    image   = string
    metrics = bool
  })
  default = {
    enabled = false
    image   = ""
    metrics = false
  }
}

variable "labels" {
  description = "Service labels."
  type        = map(string)
  default     = {}
}
