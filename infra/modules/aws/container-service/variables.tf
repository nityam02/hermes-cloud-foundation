variable "cluster_name" {
  description = "ECS cluster name."
  type        = string
}

variable "container_insights" {
  description = "Enable ECS Container Insights."
  type        = bool
  default     = false
}

variable "vpc_id" {
  description = "VPC id."
  type        = string
}

variable "private_subnet_ids" {
  description = "Subnets for tasks (no public IPs)."
  type        = list(string)
}

variable "public_subnet_ids" {
  description = "Subnets for the shared ALB."
  type        = list(string)
}

variable "protected" {
  description = "Protected environment: ALB deletion protection; HTTPS required for public services."
  type        = bool
  default     = false
}

variable "alb" {
  description = "Shared public ALB settings (created only when a service is public)."
  type = object({
    name                = string
    security_group_name = string
    certificate_arn     = optional(string, "")
    ingress_cidrs       = optional(list(string), ["0.0.0.0/0"])
  })
}

variable "task_egress_cidrs" {
  description = "IPv4 CIDRs tasks may reach on TCP 443 (AWS APIs, registries, external APIs). Narrow it when VPC endpoints or an egress proxy are used."
  type        = list(string)
  default     = ["0.0.0.0/0"]

  validation {
    condition     = length(var.task_egress_cidrs) > 0 && alltrue([for c in var.task_egress_cidrs : can(cidrnetmask(c))])
    error_message = "task_egress_cidrs (extensions.aws.task_egress_cidrs) must be a non-empty list of IPv4 CIDRs."
  }
}

variable "public_service_order" {
  description = "Public service names in spec order; the first one is the ALB default route, the others are served under /<name>/."
  type        = list(string)
  default     = []
}

variable "services" {
  description = "Spec service name -> resolved service definition (environment and secret references are computed by the composition)."
  type = map(object({
    image             = string
    port              = number
    cpu               = number
    memory_mb         = number
    min_instances     = number
    max_instances     = number
    public            = bool
    health_check_path = string
    command           = list(string)
    environment       = map(string)
    secrets           = map(string) # env var name -> Secrets Manager secret ARN
    log_group_name    = string
    log_group_arn     = string
    names = object({
      service        = string
      family         = string
      task_role      = string
      execution_role = string
      security_group = string
      target_group   = string
    })
    access = object({
      bucket_read_arns       = list(string)
      bucket_read_write_arns = list(string)
      topic_arns             = list(string)
      queue_arns             = list(string)
      queue_names            = list(string)
      database               = bool
    })
  }))
  default = {}

  validation {
    condition     = alltrue([for s in values(var.services) : s.min_instances >= 0 && s.max_instances >= max(1, s.min_instances) && s.max_instances <= 100])
    error_message = "Each service needs 0 <= min_instances <= max_instances <= 100 and max_instances >= 1."
  }
}

variable "database" {
  description = "Database wiring for services with access.database (null when there is no database)."
  type = object({
    security_group_id = string
    iam_connect_arn   = string
  })
  default = null
}

variable "kms_enabled" {
  description = "True when buckets/topics/queues/secrets use the environment CMK (a known boolean, so policies are shaped at plan time)."
  type        = bool
  default     = false
}

variable "kms_key_arn" {
  description = "Environment CMK ARN (required when kms_enabled); grants kms:Decrypt/GenerateDataKey via the granted services only."
  type        = string
  default     = null
}

variable "tracing" {
  description = "Run the ADOT collector sidecar (OTLP on localhost:4317) exporting traces to X-Ray."
  type        = bool
  default     = false
}

variable "metrics" {
  description = "Allow cloudwatch:PutMetricData; with tracing, the sidecar also exports OTLP metrics (EMF) to the service log group."
  type        = bool
  default     = false
}

variable "metrics_namespace" {
  description = "CloudWatch namespace for OTLP metrics exported by the sidecar."
  type        = string
  default     = "Hermes"
}

variable "otel_collector_image" {
  description = "AWS Distro for OpenTelemetry collector image (pinned tag)."
  type        = string
  default     = "public.ecr.aws/aws-observability/aws-otel-collector:v0.51.0"
}

variable "cpu_architecture" {
  description = "Fargate CPU architecture: X86_64 or ARM64 (must match the images)."
  type        = string
  default     = "X86_64"

  validation {
    condition     = contains(["X86_64", "ARM64"], var.cpu_architecture)
    error_message = "cpu_architecture must be X86_64 or ARM64."
  }
}

variable "readonly_root_filesystem" {
  description = "Read-only root filesystem for application containers (a writable /tmp volume is mounted)."
  type        = bool
  default     = true
}

variable "container_health_check" {
  description = "Add an ECS container health check (needs sh plus wget or curl in the image)."
  type        = bool
  default     = false
}

variable "alarms_enabled" {
  description = "Create ECS CPU and ALB 5xx alarms."
  type        = bool
  default     = false
}

variable "alarm_topic_arn" {
  description = "SNS topic for alarm notifications."
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
  description = "Tags."
  type        = map(string)
  default     = {}
}
