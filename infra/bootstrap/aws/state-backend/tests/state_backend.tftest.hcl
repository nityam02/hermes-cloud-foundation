mock_provider "aws" {
  mock_data "aws_partition" {
    defaults = { partition = "aws", dns_suffix = "amazonaws.com" }
  }
}

variables {
  project     = "shop"
  region      = "eu-north-1"
  bucket_name = "example-hermes-state"
  labels      = { cost-center = "platform" }
}

run "secure_state_bucket" {
  command = plan

  assert {
    condition     = output.bucket == "example-hermes-state" && output.backend_config.use_lockfile && output.backend_config.encrypt && output.backend_config.region == "eu-north-1"
    error_message = "backend_config must enable encryption and S3 native locking."
  }

  assert {
    condition     = !contains(keys(output.backend_config), "kms_key_id") && !contains(keys(output.backend_config), "dynamodb_table")
    error_message = "No kms_key_id without a key; no DynamoDB lock table."
  }

  assert {
    condition     = one(aws_s3_bucket_versioning.state.versioning_configuration).status == "Enabled"
    error_message = "State must be versioned."
  }

  assert {
    condition     = aws_s3_bucket_public_access_block.state.block_public_acls && aws_s3_bucket_public_access_block.state.block_public_policy && aws_s3_bucket_public_access_block.state.ignore_public_acls && aws_s3_bucket_public_access_block.state.restrict_public_buckets
    error_message = "The state bucket must block all public access."
  }

  assert {
    condition     = one(one(aws_s3_bucket_server_side_encryption_configuration.state.rule).apply_server_side_encryption_by_default).sse_algorithm == "aws:kms"
    error_message = "State is encrypted with SSE-KMS."
  }

  assert {
    condition     = length([for s in jsondecode(aws_s3_bucket_policy.state.policy).Statement : s if s.Effect == "Deny" && try(s.Condition.Bool["aws:SecureTransport"], "") == "false"]) == 1
    error_message = "The state bucket must deny non-TLS access."
  }

  assert {
    condition     = aws_s3_bucket.state.tags["hermes-managed-by"] == "hermes" && aws_s3_bucket.state.tags["cost-center"] == "platform"
    error_message = "The state bucket carries Hermes tags and labels."
  }
}

run "customer_managed_key" {
  command = plan

  variables {
    kms_key = "arn:aws:kms:eu-north-1:123456789012:key/00000000-0000-4000-8000-000000000000"
  }

  assert {
    condition     = output.backend_config.kms_key_id == var.kms_key && one(one(aws_s3_bucket_server_side_encryption_configuration.state.rule).apply_server_side_encryption_by_default).kms_master_key_id == var.kms_key
    error_message = "With kms_key, both SSE and backend_config use it."
  }
}

run "rejects_invalid_bucket_name" {
  command = plan

  variables {
    bucket_name = "Not_A_Bucket"
  }

  expect_failures = [var.bucket_name]
}
