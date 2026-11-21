package main

test_state_document_is_rejected if {
	"HCF-POL-000" in rules_of(deny) with input as data.fixtures.not_a_plan
}

test_arbitrary_json_is_rejected if {
	"HCF-POL-000" in rules_of(deny) with input as {"resources": []}
}

test_errored_plan_is_rejected if {
	"HCF-POL-000" in rules_of(deny) with input as {"format_version": "1.2", "planned_values": {}, "errored": true}
}

test_empty_plan_passes if {
	count(deny) == 0 with input as {"format_version": "1.2", "planned_values": {}}
}

test_destructive_changes_warn_without_denying if {
	count(deny) == 0 with input as data.fixtures.destructive
	got := {k | some k in finding_keys(warn)} with input as data.fixtures.destructive
	got == {
		"HCF-POL-007: module.platform.module.database.aws_db_instance.this",
		"HCF-POL-007: module.platform.module.storage.google_storage_bucket.this[\"artifacts\"]",
		"HCF-POL-007: module.platform.module.secrets.alicloud_kms_secret.this[\"db-password\"]",
	}
}

test_destructive_distinguishes_replace_and_delete if {
	msgs := warn with input as data.fixtures.destructive
	some d in msgs
	contains(d, "aws_db_instance.this: plan will delete")
	some r in msgs
	contains(r, "google_storage_bucket.this[\"artifacts\"]: plan will replace")
}

# Values unknown until apply are never treated as compliant silently and never cause a deny.
test_unknown_values_warn_and_do_not_deny if {
	count(deny) == 0 with input as data.fixtures.unknowns
	got := rules_of(warn) with input as data.fixtures.unknowns
	"HCF-POL-005" in got
	"HCF-POL-006" in got
	"HCF-POL-001" in got
}

test_every_rule_has_failing_fixture if {
	aws := rules_of(deny) with input as data.fixtures.aws_violations
	gcp := rules_of(deny) with input as data.fixtures.gcp_violations
	ali := rules_of(deny) with input as data.fixtures.alibaba_violations
	other := rules_of(deny) with input as data.fixtures.not_a_plan
	(((aws | gcp) | ali) | other) == all_deny_rules
}

test_every_provider_has_passing_fixture if {
	count(deny) == 0 with input as data.fixtures.aws_compliant
	count(deny) == 0 with input as data.fixtures.gcp_compliant
	count(deny) == 0 with input as data.fixtures.alibaba_compliant
}
