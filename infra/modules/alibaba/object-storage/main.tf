# Storage capability: private OSS buckets with versioning, server-side encryption,
# retention lifecycle, public access block and a TLS-only bucket policy.

module "names" {
  source = "../naming"
  # 56 + "-" + 6 hex random suffix = 63 (OSS limit).
  names = { for b in var.buckets : b.name => { parts = [var.prefix, b.name], max_length = 56 } }
}

# Stable global-uniqueness suffix shared by the environment's buckets.
resource "random_id" "suffix" {
  count       = length(var.buckets) > 0 ? 1 : 0
  byte_length = 3
}

locals {
  tags    = merge(var.tags, { "hermes-component" = "storage" })
  buckets = { for b in var.buckets : b.name => b }
  native  = { for k, _ in local.buckets : k => "${module.names.names[k]}-${random_id.suffix[0].hex}" }

  # ZRS keeps three copies across zones; not every region offers it (override per region).
  redundancy_type = var.redundancy_type != "" ? var.redundancy_type : (var.protected ? "ZRS" : "LRS")
}

resource "alicloud_oss_bucket" "this" {
  #checkov:skip=CKV_ALI_11:Transfer acceleration is a paid feature for cross-region uploads over the internet; Hermes clients reach OSS in-region, so it is not needed.
  #checkov:skip=CKV_ALI_12:OSS access logging needs a dedicated log bucket per environment; designed, not implemented (documented in docs/providers/alibaba.md). Account-level ActionTrail (not managed by Hermes) records management API calls.
  for_each = local.buckets

  bucket          = local.native[each.key]
  storage_class   = "Standard"
  redundancy_type = local.redundancy_type
  force_destroy   = false
  tags            = local.tags

  server_side_encryption_rule {
    sse_algorithm     = var.kms_key_id == null ? "AES256" : "KMS"
    kms_master_key_id = var.kms_key_id
  }

  # Versioning cannot return to "disabled" once enabled; switching the spec off later
  # needs a manual change to "Suspended".
  dynamic "versioning" {
    for_each = each.value.versioning ? [1] : []
    content {
      status = "Enabled"
    }
  }

  lifecycle_rule {
    id      = "hermes-abort-incomplete-uploads"
    enabled = true
    abort_multipart_upload {
      days = 7
    }
  }

  dynamic "lifecycle_rule" {
    for_each = each.value.retention_days > 0 ? [each.value.retention_days] : []
    content {
      id      = "hermes-retention"
      enabled = true
      expiration {
        days = lifecycle_rule.value
      }
      dynamic "noncurrent_version_expiration" {
        for_each = each.value.versioning ? [lifecycle_rule.value] : []
        content {
          days = noncurrent_version_expiration.value
        }
      }
    }
  }
}

resource "alicloud_oss_bucket_acl" "this" {
  for_each = local.buckets

  bucket = alicloud_oss_bucket.this[each.key].bucket
  acl    = "private"
}

resource "alicloud_oss_bucket_public_access_block" "this" {
  for_each = local.buckets

  bucket              = alicloud_oss_bucket.this[each.key].bucket
  block_public_access = true
}

locals {
  tls_only_policies = {
    for k, _ in local.buckets : k => jsonencode({
      Version = "1"
      Statement = [{
        Sid       = "DenyInsecureTransport"
        Effect    = "Deny"
        Principal = ["*"]
        Action    = ["oss:*"]
        Resource  = ["acs:oss:*:*:${local.native[k]}", "acs:oss:*:*:${local.native[k]}/*"]
        Condition = { Bool = { "acs:SecureTransport" = ["false"] } }
      }]
    })
  }
}

resource "alicloud_oss_bucket_policy" "tls_only" {
  for_each = local.buckets

  bucket = alicloud_oss_bucket.this[each.key].bucket
  policy = local.tls_only_policies[each.key]

  depends_on = [alicloud_oss_bucket_public_access_block.this]
}
