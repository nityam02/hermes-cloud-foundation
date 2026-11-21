# Private, encrypted, TLS-only S3 buckets. Globally unique names: "<base>-<8 hex>" where the
# suffix is a random_id kept stable per base name.

variable "buckets" {
  description = "Spec bucket name -> settings. base_name is at most 54 characters (63 minus the 9-character suffix)."
  type = map(object({
    base_name      = string
    versioning     = bool
    retention_days = number
  }))
  default = {}

  validation {
    condition     = alltrue([for b in values(var.buckets) : length(b.base_name) <= 54 && b.retention_days >= 0])
    error_message = "base_name must be at most 54 characters and retention_days must be >= 0."
  }
}

variable "kms_enabled" {
  description = "True when kms_key_arn is set (a known boolean, so encryption settings are known at plan time)."
  type        = bool
  default     = false
}

variable "kms_key_arn" {
  description = "CMK for SSE-KMS (with S3 Bucket Keys); null uses SSE-S3 (AES256)."
  type        = string
  default     = null
}

variable "noncurrent_version_days" {
  description = "Days to keep noncurrent object versions when retention_days is 0."
  type        = number
  default     = 30
}

variable "partition" {
  description = "AWS partition."
  type        = string
  default     = "aws"
}

variable "tags" {
  description = "Tags."
  type        = map(string)
  default     = {}
}

resource "random_id" "suffix" {
  for_each = var.buckets

  byte_length = 4
  keepers     = { base_name = each.value.base_name }
}

locals {
  bucket_names = { for k, b in var.buckets : k => "${b.base_name}-${random_id.suffix[k].hex}" }
}

resource "aws_s3_bucket" "this" {
  #checkov:skip=CKV_AWS_18:Server access logging needs a separate log bucket per environment; not part of the v1alpha1 storage capability (documented gap).
  #checkov:skip=CKV_AWS_144:Cross-region replication is not part of the v1alpha1 storage capability.
  #checkov:skip=CKV2_AWS_62:Event notifications are not part of the v1alpha1 storage capability.
  #checkov:skip=CKV_AWS_145:SSE-KMS with a CMK is used when security.customer_managed_keys is true; SSE-S3 otherwise.
  #checkov:skip=CKV_AWS_21:Versioning follows the spec (default true); a bucket may opt out explicitly.
  #checkov:skip=CKV_AWS_19:False positive: encryption at rest is configured in aws_s3_bucket_server_side_encryption_configuration.this (SSE-S3, or SSE-KMS with the CMK), asserted in tests/.
  for_each = var.buckets

  bucket        = local.bucket_names[each.key]
  force_destroy = false
  tags          = var.tags
}

resource "aws_s3_bucket_public_access_block" "this" {
  for_each = var.buckets

  bucket                  = aws_s3_bucket.this[each.key].id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "this" {
  for_each = var.buckets

  bucket = aws_s3_bucket.this[each.key].id
  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "this" {
  for_each = var.buckets

  bucket = aws_s3_bucket.this[each.key].id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = var.kms_enabled ? "aws:kms" : "AES256"
      kms_master_key_id = var.kms_key_arn
    }
    bucket_key_enabled = var.kms_enabled
  }
}

# "Suspended" (not "Disabled") so a bucket can be switched off versioning after it was enabled.
resource "aws_s3_bucket_versioning" "this" {
  for_each = var.buckets

  bucket = aws_s3_bucket.this[each.key].id
  versioning_configuration {
    status = each.value.versioning ? "Enabled" : "Suspended"
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "this" {
  #checkov:skip=CKV_AWS_300:False positive: the first rule aborts incomplete multipart uploads after 7 days on every bucket (filter {}; asserted in tests/). Checkov 3.4.5 drops static rules when a dynamic "rule" block iterates over each.value, verified with a minimal reproduction.
  for_each = var.buckets

  bucket = aws_s3_bucket.this[each.key].id

  rule {
    id     = "abort-incomplete-multipart-uploads"
    status = "Enabled"
    filter {}
    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }

  dynamic "rule" {
    for_each = each.value.retention_days > 0 ? [each.value.retention_days] : []
    content {
      id     = "expire-current-objects"
      status = "Enabled"
      filter {}
      expiration {
        days = rule.value
      }
    }
  }

  # Applies to versioned buckets (and to versions left over after versioning was suspended).
  rule {
    id     = "expire-noncurrent-versions"
    status = "Enabled"
    filter {}
    noncurrent_version_expiration {
      noncurrent_days = each.value.retention_days > 0 ? each.value.retention_days : var.noncurrent_version_days
    }
  }

  depends_on = [aws_s3_bucket_versioning.this]
}

resource "aws_s3_bucket_policy" "tls_only" {
  for_each = var.buckets

  bucket = aws_s3_bucket.this[each.key].id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "DenyInsecureTransport"
        Effect    = "Deny"
        Principal = "*"
        Action    = "s3:*"
        Resource  = ["arn:${var.partition}:s3:::${local.bucket_names[each.key]}", "arn:${var.partition}:s3:::${local.bucket_names[each.key]}/*"]
        Condition = { Bool = { "aws:SecureTransport" = "false" } }
      },
      {
        Sid       = "DenyOutdatedTls"
        Effect    = "Deny"
        Principal = "*"
        Action    = "s3:*"
        Resource  = ["arn:${var.partition}:s3:::${local.bucket_names[each.key]}", "arn:${var.partition}:s3:::${local.bucket_names[each.key]}/*"]
        Condition = { NumericLessThan = { "s3:TlsVersion" = "1.2" } }
      },
    ]
  })

  depends_on = [aws_s3_bucket_public_access_block.this]
}

output "buckets" {
  description = "Spec bucket name -> { name, arn } (ARN built from the name: known at plan time)."
  value = {
    for k, b in aws_s3_bucket.this : k => {
      name = b.bucket
      arn  = "arn:${var.partition}:s3:::${b.bucket}"
    }
  }
}
