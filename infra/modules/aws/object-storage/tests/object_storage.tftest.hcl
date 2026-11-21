mock_provider "aws" {}

variables {
  buckets = {
    artifacts = { base_name = "shop-dev-artifacts", versioning = true, retention_days = 0 }
    images    = { base_name = "shop-dev-images", versioning = false, retention_days = 30 }
  }
}

run "secure_defaults_without_cmk" {
  command = apply

  assert {
    condition     = can(regex("^shop-dev-artifacts-[0-9a-f]{8}$", output.buckets["artifacts"].name)) && output.buckets["artifacts"].arn == "arn:aws:s3:::${output.buckets["artifacts"].name}"
    error_message = "Bucket names must be <base>-<8 hex> and ARNs derived from them."
  }

  assert {
    condition = alltrue([for b in aws_s3_bucket_public_access_block.this :
    b.block_public_acls && b.block_public_policy && b.ignore_public_acls && b.restrict_public_buckets])
    error_message = "Every public-access-block flag must be true."
  }

  assert {
    condition     = alltrue([for o in aws_s3_bucket_ownership_controls.this : one(o.rule).object_ownership == "BucketOwnerEnforced"])
    error_message = "ACLs must be disabled (BucketOwnerEnforced)."
  }

  assert {
    condition     = alltrue([for e in aws_s3_bucket_server_side_encryption_configuration.this : one(one(e.rule).apply_server_side_encryption_by_default).sse_algorithm == "AES256"])
    error_message = "Without a CMK buckets use SSE-S3."
  }

  assert {
    condition     = alltrue([for b in aws_s3_bucket.this : !b.force_destroy])
    error_message = "force_destroy must be false."
  }

  assert {
    condition     = one(aws_s3_bucket_versioning.this["artifacts"].versioning_configuration).status == "Enabled" && one(aws_s3_bucket_versioning.this["images"].versioning_configuration).status == "Suspended"
    error_message = "Versioning must follow the spec."
  }

  assert {
    condition     = length([for r in aws_s3_bucket_lifecycle_configuration.this["images"].rule : r if r.id == "expire-current-objects" && one(r.expiration).days == 30]) == 1
    error_message = "retention_days > 0 must expire current objects after that many days."
  }

  assert {
    condition     = length([for r in aws_s3_bucket_lifecycle_configuration.this["artifacts"].rule : r if r.id == "expire-current-objects"]) == 0
    error_message = "retention_days = 0 must keep current objects forever."
  }

  assert {
    condition     = length([for r in aws_s3_bucket_lifecycle_configuration.this["artifacts"].rule : r if r.id == "expire-noncurrent-versions" && one(r.noncurrent_version_expiration).noncurrent_days == 30]) == 1
    error_message = "Noncurrent versions must expire."
  }

  assert {
    condition = alltrue([for k, lc in aws_s3_bucket_lifecycle_configuration.this : length([
      for r in lc.rule : r if r.status == "Enabled" && length(r.abort_incomplete_multipart_upload) == 1 && one(r.abort_incomplete_multipart_upload).days_after_initiation == 7
    ]) == 1])
    error_message = "Every bucket must abort incomplete multipart uploads after 7 days (CKV_AWS_300)."
  }

  assert {
    condition = alltrue([for k, p in aws_s3_bucket_policy.tls_only : alltrue([
      for s in jsondecode(p.policy).Statement : s.Effect == "Deny" && s.Principal == "*"
      ]) && length([
      for s in jsondecode(p.policy).Statement : s if try(s.Condition.Bool["aws:SecureTransport"], "") == "false"
    ]) == 1])
    error_message = "Bucket policies must only deny, and must deny non-TLS access."
  }
}

run "sse_kms_with_cmk" {
  command = apply

  variables {
    kms_enabled = true
    kms_key_arn = "arn:aws:kms:eu-north-1:123456789012:key/00000000-0000-4000-8000-000000000000"
  }

  assert {
    condition = alltrue([for e in aws_s3_bucket_server_side_encryption_configuration.this :
      one(one(e.rule).apply_server_side_encryption_by_default).sse_algorithm == "aws:kms" &&
      one(one(e.rule).apply_server_side_encryption_by_default).kms_master_key_id == var.kms_key_arn &&
    one(e.rule).bucket_key_enabled])
    error_message = "With a CMK buckets use SSE-KMS with that key and S3 Bucket Keys."
  }
}

run "rejects_long_base_names" {
  command = plan

  variables {
    buckets = { x = { base_name = "a-name-that-is-definitely-longer-than-fifty-four-characters", versioning = true, retention_days = 0 } }
  }

  expect_failures = [var.buckets]
}
