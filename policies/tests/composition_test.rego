package main

# Regression tests on plans of the REAL provider compositions (F-14): the fixtures
# fixtures/<provider>_default/plan.json are derived by scripts/gen-policy-fixtures.sh from a
# mock-provider plan of the golden render of examples/multi-cloud (default configuration,
# non-protected environment, no customer-managed keys). They are not hand-written: regenerate
# them after a module change (`make policy-fixtures`); `make policy-fixtures-check` fails when
# they are stale. A default configuration must pass every deny rule.

default_fixtures := {
	"aws": data.fixtures.aws_default,
	"gcp": data.fixtures.gcp_default,
	"alibaba": data.fixtures.alibaba_default,
}

# The most common resources each fixture must contain, so a fixture cannot pass by being empty.
required_types := {
	"aws": {"aws_s3_bucket", "aws_sqs_queue", "aws_sns_topic", "aws_db_instance", "aws_kms_key", "aws_ecs_service"},
	"gcp": {"google_storage_bucket", "google_pubsub_topic", "google_pubsub_subscription", "google_sql_database_instance", "google_cloud_run_v2_service"},
	"alibaba": {"alicloud_oss_bucket", "alicloud_message_service_topic", "alicloud_message_service_queue", "alicloud_db_instance", "alicloud_sae_application"},
}

fixture_types(fx) := {rc.type | some rc in fx.resource_changes}

test_aws_default_composition_plan_passes if {
	count(deny) == 0 with input as data.fixtures.aws_default
}

test_gcp_default_composition_plan_passes if {
	count(deny) == 0 with input as data.fixtures.gcp_default
}

test_alibaba_default_composition_plan_passes if {
	count(deny) == 0 with input as data.fixtures.alibaba_default
}

test_default_fixtures_contain_common_resources if {
	every p, types in required_types {
		missing := types - fixture_types(default_fixtures[p])
		count(missing) == 0
	}
}

alert_topic := "module.platform.module.observability.aws_sns_topic.alerts[0]"

test_aws_alert_topic_is_encrypted_in_default_plan if {
	some rc in data.fixtures.aws_default.resource_changes
	rc.address == alert_topic
	is_string(rc.change.after.kms_master_key_id)
	rc.change.after.kms_master_key_id != ""
}

# The pre-fix shape (alert topic without a key) is what HCF-POL-002 must catch.
test_aws_alert_topic_without_key_is_denied if {
	fx := data.fixtures.aws_default
	some i
	fx.resource_changes[i].address == alert_topic
	unencrypted := json.patch(fx, [{"op": "replace", "path": sprintf("/resource_changes/%d/change/after/kms_master_key_id", [i]), "value": null}])
	sprintf("HCF-POL-002: %s: SNS topic has no kms_master_key_id (SNS does not encrypt by default; use a CMK or alias/aws/sns)", [alert_topic]) in deny with input as unencrypted
}
