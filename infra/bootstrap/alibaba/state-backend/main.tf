# Remote state for one Hermes project on Alibaba Cloud: an OSS bucket (versioned,
# encrypted, private, TLS-only, deletion-protected) and a Tablestore (OTS) table that the
# OpenTofu/Terraform "oss" backend uses for state locking.
#
# Apply this once per project with local state, then migrate its own state into the
# bucket if desired. prevent_destroy guards the bucket, the Tablestore instance and table.

terraform {
  required_version = ">= 1.10"

  required_providers {
    alicloud = {
      source  = "aliyun/alicloud"
      version = "~> 2.2"
    }
  }
}

variable "project" {
  description = "Hermes project name."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,28}[a-z0-9]$", var.project))
    error_message = "The project must match ^[a-z][a-z0-9-]{1,28}[a-z0-9]$."
  }
}

variable "region" {
  description = "Region of the bucket and the Tablestore instance (must match the provider region)."
  type        = string
}

variable "bucket_name" {
  description = "Globally unique OSS bucket name for state."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$", var.bucket_name))
    error_message = "The bucket_name must be 3-63 lowercase letters, digits or hyphens, starting and ending with a letter or digit."
  }
}

variable "labels" {
  description = "Additional tags."
  type        = map(string)
  default     = {}
}

variable "kms_key" {
  description = "KMS key ID for SSE-KMS on the state bucket (\"\" = OSS-managed AES256)."
  type        = string
  default     = ""
}

variable "prefix" {
  description = "Object prefix for state files (the renderer appends <project>/<environment>/<provider>)."
  type        = string
  default     = "hermes"
}

variable "redundancy_type" {
  description = "OSS redundancy: LRS or ZRS (ZRS is not offered in every region)."
  type        = string
  default     = "LRS"

  validation {
    condition     = contains(["LRS", "ZRS"], var.redundancy_type)
    error_message = "The redundancy_type value must be LRS or ZRS."
  }
}

variable "lock_table_internet_access" {
  description = "Allow Tablestore access over the internet (needed when CI runs outside the VPC; requests still need RAM credentials)."
  type        = bool
  default     = true
}

module "names" {
  source = "../../../modules/alibaba/naming"
  names = {
    # Tablestore instance names are 3-16 characters.
    ots = { parts = [var.project, "tf"], max_length = 16 }
  }
}

locals {
  tags = merge(var.labels, {
    "hermes-project"     = var.project
    "hermes-environment" = "shared"
    "hermes-managed-by"  = "hermes"
    "hermes-component"   = "storage"
  })

  lock_table = "hermes_state_lock"

  tls_only_policy = jsonencode({
    Version = "1"
    Statement = [{
      Sid       = "DenyInsecureTransport"
      Effect    = "Deny"
      Principal = ["*"]
      Action    = ["oss:*"]
      Resource  = ["acs:oss:*:*:${var.bucket_name}", "acs:oss:*:*:${var.bucket_name}/*"]
      Condition = { Bool = { "acs:SecureTransport" = ["false"] } }
    }]
  })
}

resource "alicloud_oss_bucket" "state" {
  #checkov:skip=CKV_ALI_11:Transfer acceleration is a paid feature for cross-region uploads over the internet; Hermes clients reach OSS in-region, so it is not needed.
  #checkov:skip=CKV_ALI_12:OSS access logging needs a dedicated log bucket per environment; designed, not implemented (documented in docs/providers/alibaba.md). Account-level ActionTrail (not managed by Hermes) records management API calls.
  bucket          = var.bucket_name
  storage_class   = "Standard"
  redundancy_type = var.redundancy_type
  force_destroy   = false
  tags            = local.tags

  versioning {
    status = "Enabled"
  }

  server_side_encryption_rule {
    sse_algorithm     = var.kms_key == "" ? "AES256" : "KMS"
    kms_master_key_id = var.kms_key == "" ? null : var.kms_key
  }

  # Keep old state versions for 90 days; clean up abandoned multipart uploads.
  lifecycle_rule {
    id      = "hermes-state-history"
    enabled = true
    noncurrent_version_expiration {
      days = 90
    }
    abort_multipart_upload {
      days = 7
    }
  }

  lifecycle {
    prevent_destroy = true
  }
}

resource "alicloud_oss_bucket_acl" "state" {
  bucket = alicloud_oss_bucket.state.bucket
  acl    = "private"
}

resource "alicloud_oss_bucket_public_access_block" "state" {
  bucket              = alicloud_oss_bucket.state.bucket
  block_public_access = true
}

resource "alicloud_oss_bucket_policy" "state" {
  bucket = alicloud_oss_bucket.state.bucket
  policy = local.tls_only_policy

  depends_on = [alicloud_oss_bucket_public_access_block.state]
}

resource "alicloud_ots_instance" "lock" {
  name               = module.names.names["ots"]
  description        = "Hermes state lock for ${var.project}"
  instance_type      = "Capacity"
  network_type_acl   = var.lock_table_internet_access ? ["VPC", "INTERNET"] : ["VPC"]
  network_source_acl = ["TRUST_PROXY"]
  tags               = local.tags

  lifecycle {
    prevent_destroy = true
  }
}

resource "alicloud_ots_table" "lock" {
  instance_name = alicloud_ots_instance.lock.name
  table_name    = local.lock_table
  time_to_live  = -1
  max_version   = 1
  enable_sse    = true
  sse_key_type  = "SSE_KMS_SERVICE"

  # The oss backend expects a single string primary key named LockID.
  primary_key {
    name = "LockID"
    type = "String"
  }

  lifecycle {
    prevent_destroy = true
  }
}

output "bucket" {
  description = "State bucket name."
  value       = alicloud_oss_bucket.state.bucket
}

output "backend_config" {
  description = "Settings for the oss backend block that hermes writes into rendered roots."
  value = {
    bucket              = alicloud_oss_bucket.state.bucket
    prefix              = var.prefix
    region              = var.region
    encrypt             = true
    tablestore_endpoint = "https://${alicloud_ots_instance.lock.name}.${var.region}.ots.aliyuncs.com"
    tablestore_table    = alicloud_ots_table.lock.table_name
  }
}
