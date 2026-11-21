# Remote state bucket for one project (state-backend bootstrap contract, infra/contract/outputs.md).
# Run once per project/account with plain OpenTofu/Terraform before the first `hermes apply`.
# Locking uses S3 native lock files (use_lockfile; OpenTofu >= 1.10, Terraform >= 1.11):
# no DynamoDB table is needed.

provider "aws" {
  region = var.region
}

variable "project" {
  description = "Project name (used for tags)."
  type        = string
}

variable "region" {
  description = "Region of the state bucket."
  type        = string
}

variable "bucket_name" {
  description = "Globally unique state bucket name."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$", var.bucket_name)) && !strcontains(var.bucket_name, "--")
    error_message = "bucket_name must be 3-63 characters of lowercase letters, digits and single hyphens."
  }
}

variable "labels" {
  description = "Additional tags."
  type        = map(string)
  default     = {}
}

variable "kms_key" {
  description = "Optional KMS key ARN for SSE-KMS; \"\" uses the AWS-managed aws/s3 key."
  type        = string
  default     = ""
}

variable "noncurrent_version_days" {
  description = "Days to keep noncurrent state versions (recovery window for bad applies)."
  type        = number
  default     = 90
}

data "aws_partition" "current" {}

locals {
  tags = merge(var.labels, {
    "hermes-project"    = var.project
    "hermes-managed-by" = "hermes"
    "hermes-component"  = "state"
  })
  bucket_arn = "arn:${data.aws_partition.current.partition}:s3:::${var.bucket_name}"
}

# Server access logging needs a separate log bucket; enable it out-of-band if required.
#trivy:ignore:AWS-0089
resource "aws_s3_bucket" "state" {
  #checkov:skip=CKV_AWS_18:Access logging for the state bucket needs a separate log bucket; enable it out-of-band if required.
  #checkov:skip=CKV_AWS_144:Cross-region replication of state is optional and out of scope for the bootstrap.
  #checkov:skip=CKV2_AWS_62:Event notifications are not needed for state.
  bucket        = var.bucket_name
  force_destroy = false
  tags          = local.tags

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_s3_bucket_public_access_block" "state" {
  bucket                  = aws_s3_bucket.state.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "state" {
  bucket = aws_s3_bucket.state.id
  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

resource "aws_s3_bucket_versioning" "state" {
  bucket = aws_s3_bucket.state.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "state" {
  bucket = aws_s3_bucket.state.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"
      kms_master_key_id = var.kms_key == "" ? null : var.kms_key
    }
    bucket_key_enabled = true
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "state" {
  bucket = aws_s3_bucket.state.id

  rule {
    id     = "expire-noncurrent-state-versions"
    status = "Enabled"
    filter {}
    noncurrent_version_expiration {
      noncurrent_days = var.noncurrent_version_days
    }
  }

  rule {
    id     = "abort-incomplete-multipart-uploads"
    status = "Enabled"
    filter {}
    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }

  depends_on = [aws_s3_bucket_versioning.state]
}

resource "aws_s3_bucket_policy" "state" {
  bucket = aws_s3_bucket.state.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "DenyInsecureTransport"
        Effect    = "Deny"
        Principal = "*"
        Action    = "s3:*"
        Resource  = [local.bucket_arn, "${local.bucket_arn}/*"]
        Condition = { Bool = { "aws:SecureTransport" = "false" } }
      },
      {
        Sid       = "DenyOutdatedTls"
        Effect    = "Deny"
        Principal = "*"
        Action    = "s3:*"
        Resource  = [local.bucket_arn, "${local.bucket_arn}/*"]
        Condition = { NumericLessThan = { "s3:TlsVersion" = "1.2" } }
      },
    ]
  })

  depends_on = [aws_s3_bucket_public_access_block.state]
}

output "bucket" {
  description = "State bucket name."
  value       = aws_s3_bucket.state.bucket
}

output "backend_config" {
  description = "Settings for the s3 backend that hermes writes into rendered roots (the key is set per environment by hermes)."
  value = merge(
    {
      bucket       = aws_s3_bucket.state.bucket
      region       = var.region
      encrypt      = true
      use_lockfile = true
    },
    var.kms_key == "" ? {} : { kms_key_id = var.kms_key },
  )
}
