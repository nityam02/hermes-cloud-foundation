# HCF-POL-007 (warn): deleting or replacing stateful resources loses data or breaks
# dependants. The hard gate is in the CLI (`hermes apply --allow-destructive`); this rule makes
# the impact explicit in the policy report.
package main

stateful_types := {
	# databases
	"aws_db_instance", "aws_rds_cluster", "aws_rds_cluster_instance", "aws_dynamodb_table", "aws_elasticache_replication_group",
	"google_sql_database_instance", "google_sql_database", "google_alloydb_cluster", "google_firestore_database",
	"alicloud_db_instance", "alicloud_db_database", "alicloud_polardb_cluster", "alicloud_ots_instance", "alicloud_ots_table",
	# object storage and volumes
	"aws_s3_bucket", "aws_efs_file_system", "aws_ebs_volume",
	"google_storage_bucket", "google_compute_disk",
	"alicloud_oss_bucket", "alicloud_disk", "alicloud_ecs_disk",
	# keys (deleting a key makes everything it encrypted unreadable)
	"aws_kms_key", "google_kms_crypto_key", "google_kms_key_ring", "alicloud_kms_key", "alicloud_kms_instance",
	# secrets (values set out-of-band must be re-created by hand)
	"aws_secretsmanager_secret", "google_secret_manager_secret", "alicloud_kms_secret",
	# durable queues and streams (in-flight messages are lost)
	"aws_sqs_queue", "aws_kinesis_stream", "google_pubsub_subscription", "google_pubsub_topic",
	"alicloud_message_service_queue", "alicloud_mns_queue", "alicloud_log_store",
}

change_kind(rc) := "replace" if {
	"create" in rc.change.actions
} else := "delete"

warn contains message("HCF-POL-007", rc, sprintf("plan will %s a stateful resource (%s); confirm backups and use --allow-destructive only deliberately", [change_kind(rc), rc.type])) if {
	some rc in deleted_resources
	rc.type in stateful_types
}
