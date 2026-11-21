# -----------------------------------------------------------------------------
# Hermes provider composition contract — v1alpha1
#
# CANONICAL FILE. Every provider composition module
# (infra/providers/<provider>/platform/variables.tf) MUST contain these variable
# declarations verbatim. tests/contracts enforces equality.
#
# The hermes CLI renders the *normalized* spec (every field populated) into these
# variables. The optional() defaults below exist so the modules can also be called
# directly from plain OpenTofu/Terraform (consumption from Terragrunt or Atmos is
# expected to work but is not yet tested).
# -----------------------------------------------------------------------------

variable "project" {
  description = "Project name (3-30 chars, lowercase letters, digits, hyphens)."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,28}[a-z0-9]$", var.project))
    error_message = "The project must match ^[a-z][a-z0-9-]{1,28}[a-z0-9]$."
  }
}

variable "environment" {
  description = "Environment name (2-12 chars, lowercase letters and digits)."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9]{1,11}$", var.environment))
    error_message = "The environment must match ^[a-z][a-z0-9]{1,11}$."
  }
}

variable "region" {
  description = "Provider region for all regional resources."
  type        = string
}

variable "protected" {
  description = "True when the environment is protected (production-grade defaults and guardrails)."
  type        = bool
  default     = false
}

variable "labels" {
  description = "Additional labels/tags applied to every resource."
  type        = map(string)
  default     = {}
}

variable "network" {
  description = "Private network layout."
  type = object({
    cidr  = optional(string, "10.20.0.0/16")
    zones = optional(number, 2)
    nat   = optional(bool, true)
  })
  default = {}

  validation {
    condition     = contains([2, 3], var.network.zones)
    error_message = "The network.zones value must be 2 or 3."
  }

  validation {
    condition     = can(cidrhost(var.network.cidr, 0)) && tonumber(split("/", var.network.cidr)[1]) <= 20
    error_message = "The network.cidr value must be a valid IPv4 CIDR with a prefix length of /20 or larger."
  }
}

variable "security" {
  description = "Cross-cutting security settings."
  type = object({
    customer_managed_keys = optional(bool, false)
  })
  default = {}
}

variable "compute" {
  description = "Containerized services."
  type = object({
    type = optional(string, "none") # containerized | none  (kubernetes reserved)
    services = optional(list(object({
      name              = string
      image             = string
      port              = optional(number, 8080)
      cpu               = optional(number, 0.5)
      memory_mb         = optional(number, 1024)
      min_instances     = optional(number, 1)
      max_instances     = optional(number, 3)
      public            = optional(bool, false)
      health_check_path = optional(string, "/healthz")
      command           = optional(list(string), [])
      env               = optional(map(string), {})
      secret_env        = optional(map(string), {})
      access = optional(object({
        buckets  = optional(map(string), {}) # bucket name -> "read" | "read-write"
        publish  = optional(list(string), [])
        consume  = optional(list(string), [])
        database = optional(bool, false)
      }), {})
    })), [])
  })
  default = {}

  validation {
    condition     = contains(["containerized", "none"], var.compute.type)
    error_message = "The compute.type value must be containerized or none (kubernetes is reserved and not implemented in v1alpha1)."
  }

  validation {
    condition = alltrue([
      for s in var.compute.services : alltrue([for mode in values(s.access.buckets) : contains(["read", "read-write"], mode)])
    ])
    error_message = "Each service access.buckets value must be read or read-write."
  }
}

variable "database" {
  description = "Managed relational database."
  type = object({
    enabled                = optional(bool, false)
    engine                 = optional(string, "postgresql")
    version                = optional(string, "16")
    size                   = optional(string, "small") # small | medium | large | xlarge
    storage_gb             = optional(number, 20)
    max_storage_gb         = optional(number, 100)
    high_availability      = optional(bool, false)
    backup_retention_days  = optional(number, 7)
    point_in_time_recovery = optional(bool, true)
    deletion_protection    = optional(bool, true)
    extensions             = optional(list(string), [])
  })
  default = {}

  validation {
    condition     = var.database.engine == "postgresql"
    error_message = "The database.engine value must be postgresql (mysql is reserved and not implemented in v1alpha1)."
  }

  validation {
    condition     = contains(["small", "medium", "large", "xlarge"], var.database.size)
    error_message = "The database.size value must be small, medium, large or xlarge."
  }
}

variable "storage" {
  description = "Object storage buckets."
  type = object({
    enabled = optional(bool, false)
    buckets = optional(list(object({
      name           = string
      versioning     = optional(bool, true)
      retention_days = optional(number, 0)
    })), [])
  })
  default = {}
}

variable "messaging" {
  description = "Topics with subscriptions (publish/subscribe with a durable queue per subscription)."
  type = object({
    enabled = optional(bool, false)
    topics = optional(list(object({
      name            = string
      retention_hours = optional(number, 168)
      subscriptions = optional(list(object({
        name                  = string
        ack_deadline_seconds  = optional(number, 30)
        max_delivery_attempts = optional(number, 5)
      })), [])
    })), [])
  })
  default = {}

  # Portable bounds: Pub/Sub subscriptions and Alibaba MNS queues retain messages for at most
  # 7 days; Pub/Sub requires 5-100 delivery attempts before dead-lettering.
  validation {
    condition     = alltrue([for t in var.messaging.topics : t.retention_hours >= 1 && t.retention_hours <= 168])
    error_message = "Each messaging topic retention_hours value must be between 1 and 168 (the portable maximum is 7 days)."
  }

  validation {
    condition = alltrue(flatten([for t in var.messaging.topics : [
      for s in t.subscriptions : (s.max_delivery_attempts == 0 || (s.max_delivery_attempts >= 5 && s.max_delivery_attempts <= 100)) && s.ack_deadline_seconds >= 10 && s.ack_deadline_seconds <= 600
    ]]))
    error_message = "Each subscription max_delivery_attempts value must be 0 (no dead-letter) or 5-100, and ack_deadline_seconds must be 10-600."
  }
}

variable "secrets" {
  description = "Secret containers. Values are never managed by IaC; they are set out-of-band."
  type = object({
    enabled = optional(bool, false)
    names   = optional(list(string), [])
  })
  default = {}
}

variable "observability" {
  description = "Logging, metrics, tracing and alerting."
  type = object({
    enabled            = optional(bool, true)
    log_retention_days = optional(number, 30)
    metrics            = optional(bool, true)
    tracing            = optional(bool, true)
    alert_email        = optional(string, "")
  })
  default = {}
}

variable "extensions" {
  description = "Provider-specific settings (only the keys for this provider are passed). Each composition documents the keys it reads."
  type        = any
  default     = {}
}
