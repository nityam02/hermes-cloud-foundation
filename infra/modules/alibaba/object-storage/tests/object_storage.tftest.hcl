# OSS module tests (mock alicloud provider; the real random provider runs locally).

mock_provider "alicloud" {}

variables {
  prefix = "sample-logistics-platform-production"
  buckets = [
    { name = "telemetry-archive-01", versioning = true, retention_days = 0 },
    { name = "images", versioning = false, retention_days = 30 },
  ]
}

run "secure_defaults_without_cmk" {
  command = apply

  assert {
    condition     = alltrue([for b in values(output.buckets) : length(b.name) >= 3 && length(b.name) <= 63 && can(regex("^[a-z0-9][a-z0-9-]*[a-z0-9]$", b.name))])
    error_message = "Bucket names must be valid OSS names of 3-63 characters, even for long project names."
  }

  assert {
    condition     = alltrue([for b in values(output.buckets) : b.url == "oss://${b.name}"])
    error_message = "Bucket URLs must use the oss:// scheme."
  }

  assert {
    condition     = alltrue([for k, b in alicloud_oss_bucket.this : one(b.server_side_encryption_rule).sse_algorithm == "AES256"])
    error_message = "Without a CMK, buckets must use OSS-managed AES256 encryption."
  }

  assert {
    condition     = alltrue([for a in values(alicloud_oss_bucket_acl.this) : a.acl == "private"]) && alltrue([for p in values(alicloud_oss_bucket_public_access_block.this) : p.block_public_access == true])
    error_message = "Buckets must be private with public access blocked."
  }

  assert {
    condition = alltrue([for k, p in output.tls_only_policies : (
      jsondecode(p).Statement[0].Effect == "Deny" &&
      jsondecode(p).Statement[0].Condition.Bool["acs:SecureTransport"] == ["false"] &&
      contains(jsondecode(p).Statement[0].Resource, "acs:oss:*:*:${output.buckets[k].name}/*")
    )])
    error_message = "Every bucket must deny requests that do not use TLS."
  }

  assert {
    condition     = one(alicloud_oss_bucket.this["telemetry-archive-01"].versioning).status == "Enabled" && length(alicloud_oss_bucket.this["images"].versioning) == 0
    error_message = "Versioning must follow the spec."
  }

  assert {
    condition     = length(alicloud_oss_bucket.this["images"].lifecycle_rule) == 2 && length(alicloud_oss_bucket.this["telemetry-archive-01"].lifecycle_rule) == 1
    error_message = "A retention rule must exist only when retention_days > 0 (plus the multipart-cleanup rule)."
  }

  assert {
    condition     = alltrue([for b in values(alicloud_oss_bucket.this) : b.redundancy_type == "LRS" && b.force_destroy == false])
    error_message = "Non-protected buckets use LRS and are never force-destroyed."
  }
}

run "cmk_and_protected" {
  command = apply

  variables {
    kms_key_id = "key-mock-123"
    protected  = true
  }

  assert {
    condition     = alltrue([for b in values(alicloud_oss_bucket.this) : one(b.server_side_encryption_rule).sse_algorithm == "KMS" && one(b.server_side_encryption_rule).kms_master_key_id == "key-mock-123"])
    error_message = "With a CMK, buckets must use SSE-KMS with that key."
  }

  assert {
    condition     = alltrue([for b in values(alicloud_oss_bucket.this) : b.redundancy_type == "ZRS"])
    error_message = "Protected buckets must be zone-redundant by default."
  }
}
