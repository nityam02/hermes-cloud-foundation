# State-backend bootstrap tests (mock provider only; no cloud API is called).
# Plan-only: the resources use prevent_destroy, which the test's cleanup destroy would hit.

mock_provider "alicloud" {}

variables {
  project     = "sample-logistics-platform"
  region      = "eu-central-1"
  bucket_name = "example-hermes-state"
}

run "secure_bucket_and_lock_table" {
  command = plan

  assert {
    condition     = one(alicloud_oss_bucket.state.versioning).status == "Enabled" && one(alicloud_oss_bucket.state.server_side_encryption_rule).sse_algorithm == "AES256"
    error_message = "The state bucket must be versioned and encrypted."
  }

  assert {
    condition     = alicloud_oss_bucket_acl.state.acl == "private" && alicloud_oss_bucket_public_access_block.state.block_public_access == true && alicloud_oss_bucket.state.force_destroy == false
    error_message = "The state bucket must be private with public access blocked."
  }

  assert {
    condition     = jsondecode(alicloud_oss_bucket_policy.state.policy).Statement[0].Condition.Bool["acs:SecureTransport"] == ["false"]
    error_message = "The state bucket must deny requests without TLS."
  }

  assert {
    condition     = one(alicloud_ots_table.lock.primary_key).name == "LockID" && one(alicloud_ots_table.lock.primary_key).type == "String"
    error_message = "The oss backend lock table needs a String primary key named LockID."
  }

  assert {
    condition     = length(alicloud_ots_instance.lock.name) <= 16 && length(alicloud_ots_instance.lock.name) >= 3
    error_message = "Tablestore instance names must be 3-16 characters, even for long project names."
  }

  assert {
    condition = (
      output.backend_config.bucket == "example-hermes-state" &&
      output.backend_config.region == "eu-central-1" &&
      output.backend_config.encrypt == true &&
      output.backend_config.tablestore_table == "hermes_state_lock" &&
      output.backend_config.tablestore_endpoint == "https://${alicloud_ots_instance.lock.name}.eu-central-1.ots.aliyuncs.com"
    )
    error_message = "backend_config must carry the oss backend settings."
  }

  assert {
    condition     = output.bucket == "example-hermes-state"
    error_message = "The bucket output must be the state bucket name."
  }
}

run "customer_managed_key" {
  command = plan

  variables {
    kms_key = "key-mock-123"
  }

  assert {
    condition     = one(alicloud_oss_bucket.state.server_side_encryption_rule).sse_algorithm == "KMS" && one(alicloud_oss_bucket.state.server_side_encryption_rule).kms_master_key_id == "key-mock-123"
    error_message = "With kms_key the state bucket must use SSE-KMS with that key."
  }
}
