variable "project" {
  description = "Spec project name."
  type        = string
}

variable "environment" {
  description = "Spec environment name."
  type        = string
}

variable "service_environment" {
  description = "Service name -> exact environment variables to inject (computed once by the composition)."
  type        = map(map(string))
}

variable "prefix" {
  description = "Native name prefix: <project>-<environment>."
  type        = string
}

variable "region" {
  description = "Region (used in RAM resource ARNs)."
  type        = string
}

variable "account_id" {
  description = "Alibaba Cloud account ID (used in RAM resource ARNs and the role trust policy)."
  type        = string
}

variable "services" {
  description = "Spec services (contract shape)."
  type = list(object({
    name              = string
    image             = string
    port              = number
    cpu               = number
    memory_mb         = number
    min_instances     = number
    max_instances     = number
    public            = bool
    health_check_path = string
    command           = list(string)
    env               = map(string)
    secret_env        = map(string)
    access = object({
      buckets  = map(string)
      publish  = list(string)
      consume  = list(string)
      database = bool
    })
  }))
  default = []

  validation {
    condition     = alltrue([for s in var.services : s.cpu <= 32 && s.memory_mb <= 131072])
    error_message = "Each service must fit an SAE instance type (at most 32 vCPU and 128 GB)."
  }
}

variable "vpc_id" {
  description = "VPC ID."
  type        = string
}

variable "vpc_cidr" {
  description = "VPC CIDR (allowed to reach service ports)."
  type        = string
}

variable "vswitch_ids" {
  description = "vSwitch IDs the application instances may use."
  type        = list(string)
}

variable "bucket_names" {
  description = "Spec bucket name -> native bucket name."
  type        = map(string)
  default     = {}
}

variable "bucket_kms_key_id" {
  description = "KMS key that encrypts the buckets (SSE-KMS), or null."
  type        = string
  default     = null
}

variable "topic_names" {
  description = "Spec topic name -> native topic name."
  type        = map(string)
  default     = {}
}

variable "queue_names" {
  description = "Spec subscription name -> native queue name."
  type        = map(string)
  default     = {}
}

variable "secret_names" {
  description = "Spec secret name -> native secret name."
  type        = map(string)
  default     = {}
}

variable "trusted_principals" {
  description = "RAM role/user ARNs allowed to assume the workload roles; replaces the default SAE service principal (sae.aliyuncs.com). Never the account root."
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for p in var.trusted_principals : can(regex("^acs:ram::[0-9]{10,20}:(role|user)/[A-Za-z0-9._@/-]+$", p))])
    error_message = "trusted_principals entries must be RAM role or user ARNs (acs:ram::<account>:role/<name> or :user/<name>); the account root is not allowed."
  }
}

variable "certificate_id" {
  description = "SSL certificate ID for HTTPS on public load balancers (\"\" = HTTP only)."
  type        = string
  default     = ""
}

variable "acr_instance_id" {
  description = "Container Registry Enterprise Edition instance ID when images are pulled from it."
  type        = string
  default     = ""
}

variable "log_project" {
  description = "SLS project for stdout shipping (\"\" disables shipping)."
  type        = string
  default     = ""
}

variable "logstore" {
  description = "SLS logstore for stdout shipping."
  type        = string
  default     = ""
}

variable "protected" {
  description = "Protected environment: public services must use HTTPS."
  type        = bool
  default     = false
}

variable "tags" {
  description = "Tags applied to taggable resources."
  type        = map(string)
  default     = {}
}
