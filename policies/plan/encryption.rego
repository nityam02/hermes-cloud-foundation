# HCF-POL-002: data stores must be encrypted at rest and databases must require TLS,
# wherever the plan makes the setting visible.
#
# Not covered because the service always encrypts at rest: S3 (SSE-S3 default since 2023),
# DynamoDB, CloudWatch Logs, Secrets Manager, GCS, Cloud SQL, Pub/Sub, KMS secrets.
# Customer-managed keys are not enforced here: a key created in the same plan is unknown until
# apply, so "no CMK" and "CMK not yet created" look the same (documented limitation).
package main

# ---------------------------------------------------------------- AWS

deny contains message("HCF-POL-002", rc, "storage_encrypted = false") if {
	some rc in planned_resources
	rc.type in {"aws_db_instance", "aws_rds_cluster", "aws_docdb_cluster", "aws_neptune_cluster"}
	attr_false(rc, "storage_encrypted")
	not excepted("HCF-POL-002", rc.address)
}

deny contains message("HCF-POL-002", rc, "encrypted = false") if {
	some rc in planned_resources
	rc.type in {"aws_ebs_volume", "aws_efs_file_system", "alicloud_disk", "alicloud_ecs_disk"}
	attr_false(rc, "encrypted")
	not excepted("HCF-POL-002", rc.address)
}

deny contains message("HCF-POL-002", rc, "SQS queue has neither SQS-managed SSE nor a KMS key") if {
	some rc in planned_resources
	rc.type == "aws_sqs_queue"
	attr_false(rc, "sqs_managed_sse_enabled")
	empty_or_null(rc.change.after.kms_master_key_id)
	not excepted("HCF-POL-002", rc.address)
}

deny contains message("HCF-POL-002", rc, "SNS topic has no kms_master_key_id (SNS does not encrypt by default; use a CMK or alias/aws/sns)") if {
	some rc in planned_resources
	rc.type == "aws_sns_topic"
	"kms_master_key_id" in object.keys(rc.change.after)
	empty_or_null(rc.change.after.kms_master_key_id)
	not excepted("HCF-POL-002", rc.address)
}

deny contains message("HCF-POL-002", rc, sprintf("%s = false", [attr])) if {
	some rc in planned_resources
	rc.type == "aws_elasticache_replication_group"
	some attr in ["at_rest_encryption_enabled", "transit_encryption_enabled"]
	attr_false(rc, attr)
	not excepted("HCF-POL-002", rc.address)
}

deny contains message("HCF-POL-002", rc, "Kinesis stream encryption_type = NONE") if {
	some rc in planned_resources
	rc.type == "aws_kinesis_stream"
	rc.change.after.encryption_type == "NONE"
	not excepted("HCF-POL-002", rc.address)
}

deny contains message("HCF-POL-002", rc, "parameter group sets rds.force_ssl = 0; databases must require TLS") if {
	some rc in planned_resources
	rc.type in {"aws_db_parameter_group", "aws_rds_cluster_parameter_group"}
	some p in rc.change.after.parameter
	p.name == "rds.force_ssl"
	p.value == "0"
	not excepted("HCF-POL-002", rc.address)
}

# ---------------------------------------------------------------- Google Cloud

deny contains message("HCF-POL-002", rc, "Cloud SQL ssl_mode allows unencrypted connections; use ENCRYPTED_ONLY or TRUSTED_CLIENT_CERTIFICATE_REQUIRED") if {
	some rc in planned_resources
	rc.type == "google_sql_database_instance"
	some s in rc.change.after.settings
	some ip in s.ip_configuration
	ip.ssl_mode == "ALLOW_UNENCRYPTED_AND_ENCRYPTED"
	not excepted("HCF-POL-002", rc.address)
}

# ---------------------------------------------------------------- Alibaba Cloud

deny contains message("HCF-POL-002", rc, "ssl_action = Close; ApsaraDB RDS must keep SSL enabled") if {
	some rc in planned_resources
	rc.type == "alicloud_db_instance"
	rc.change.after.ssl_action == "Close"
	not excepted("HCF-POL-002", rc.address)
}

warn contains message("HCF-POL-002", rc, "ApsaraDB RDS SSL is not explicitly enabled (set ssl_action = \"Open\")") if {
	some rc in planned_resources
	rc.type == "alicloud_db_instance"
	not rc.change.after.ssl_action
	not after_unknown(rc, "ssl_action")
}

warn contains message("HCF-POL-002", rc, "no encryption_key and TDE not enabled: disk encryption with a KMS key is not configured") if {
	some rc in planned_resources
	rc.type == "alicloud_db_instance"
	empty_or_null(object.get(rc.change.after, "encryption_key", null))
	not after_unknown(rc, "encryption_key")
	object.get(rc.change.after, "tde_status", "") != "Enabled"
}

# An OSS bucket is unencrypted when it has no inline server_side_encryption_rule and its module
# has no alicloud_oss_bucket_server_side_encryption resource.
oss_sse_resource_modules := {module_of(rc) |
	some rc in planned_resources
	rc.type == "alicloud_oss_bucket_server_side_encryption"
}

deny contains message("HCF-POL-002", rc, "OSS bucket has no server-side encryption (server_side_encryption_rule or alicloud_oss_bucket_server_side_encryption)") if {
	some rc in planned_resources
	rc.type == "alicloud_oss_bucket"
	count(object.get(rc.change.after, "server_side_encryption_rule", [])) == 0
	not after_unknown(rc, "server_side_encryption_rule")
	not module_of(rc) in oss_sse_resource_modules
	not excepted("HCF-POL-002", rc.address)
}

deny contains message("HCF-POL-002", rc, "OSS server-side encryption algorithm must be AES256, KMS or SM4") if {
	some rc in planned_resources
	rc.type == "alicloud_oss_bucket"
	some rule in rc.change.after.server_side_encryption_rule
	not object.get(rule, "sse_algorithm", "") in {"AES256", "KMS", "SM4"}
	not excepted("HCF-POL-002", rc.address)
}
