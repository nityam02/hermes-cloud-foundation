package main

aws_b := "module.platform.module.bad."

expected_aws_denies := {sprintf("%s: %s%s", [r, aws_b, a]) | some pair in [
	["HCF-POL-001", "aws_s3_bucket_public_access_block.partial"],
	["HCF-POL-001", "aws_s3_bucket_acl.public"],
	["HCF-POL-001", "aws_s3_bucket_acl.grant"],
	["HCF-POL-001", "aws_s3_bucket_policy.public"],
	["HCF-POL-002", "aws_db_instance.bad"],
	["HCF-POL-002", "aws_db_parameter_group.no_tls"],
	["HCF-POL-002", "aws_sqs_queue.plain"],
	["HCF-POL-002", "aws_sns_topic.plain"],
	["HCF-POL-003", "aws_db_instance.bad"],
	["HCF-POL-004", "aws_security_group.ssh"],
	["HCF-POL-004", "aws_vpc_security_group_ingress_rule.web_not_compute"],
	["HCF-POL-004", "aws_vpc_security_group_ingress_rule.all_traffic"],
	["HCF-POL-004", "aws_security_group_rule.db"],
	["HCF-POL-005", "aws_iam_role_policy.star"],
	["HCF-POL-005", "aws_iam_role_policy.not_action"],
	["HCF-POL-005", "aws_iam_policy.data_star"],
	["HCF-POL-005", "aws_iam_policy.pass_role"],
	["HCF-POL-006", "aws_db_instance.bad"],
	["HCF-POL-008", "aws_sns_topic.untagged"],
	["HCF-POL-008", "aws_sqs_queue.wrong_owner"],
	["HCF-POL-009", "aws_db_instance.bad"],
	["HCF-POL-009", "aws_secretsmanager_secret_version.value"],
	["HCF-POL-009", "aws_ssm_parameter.secure"],
	["HCF-POL-009", "random_password.db"],
	["HCF-POL-009", "aws_iam_access_key.ci"],
	["HCF-POL-010", "aws_iam_role.public_trust"],
	["HCF-POL-010", "aws_iam_role.wildcard_sub"],
	["HCF-POL-010", "aws_iam_role.no_sub"],
	["HCF-POL-011", "aws_db_instance.bad"],
]; r := pair[0]; a := pair[1]}

test_aws_compliant_plan_passes if {
	count(deny) == 0 with input as data.fixtures.aws_compliant
	count(warn) == 0 with input as data.fixtures.aws_compliant
}

test_aws_violations_exact if {
	got := finding_keys(deny) with input as data.fixtures.aws_violations
	got == expected_aws_denies
}

test_aws_resource_less_list_action_allowed if {
	msgs := deny with input as data.fixtures.aws_violations
	every m in msgs {
		not contains(m, "s3:ListAllMyBuckets")
	}
}

test_aws_data_plane_messages_name_each_action if {
	msgs := deny with input as data.fixtures.aws_violations
	some m in msgs
	contains(m, "\"secretsmanager:GetSecretValue\" on Resource")
}

test_aws_messages_never_contain_secret_values if {
	msgs := deny with input as data.fixtures.aws_violations
	every m in msgs {
		not contains(m, "demo-not-a-secret")
	}
}

# Protected-environment rules follow data.hermes.protected_environments.
test_aws_unprotected_environment_skips_protection_rules if {
	got := rules_of(deny) with input as data.fixtures.aws_violations
		with data.hermes.protected_environments as ["live"]
	not "HCF-POL-006" in got
	not "HCF-POL-011" in got
	"HCF-POL-002" in got
}

test_aws_cli_protected_flag_overrides_tags if {
	got := deny with input as data.fixtures.aws_violations
		with data.hermes.protected_environments as []
		with data.hermes.protected as true
	has_finding(got, "HCF-POL-006", concat("", [aws_b, "aws_db_instance.bad"]))
}

# The compliant fixture's rendered root passes protected = true; a database without deletion
# protection is denied even when tagged with a non-protected environment.
test_aws_module_call_protected_is_detected if {
	db := json.patch(data.fixtures.aws_compliant.resource_changes[4], [
		{"op": "replace", "path": "/change/after/deletion_protection", "value": false},
		{"op": "replace", "path": "/change/after/tags_all/hermes-environment", "value": "dev"},
	])
	db.type == "aws_db_instance"
	doc := json.patch(data.fixtures.aws_compliant, [{"op": "replace", "path": "/resource_changes/4", "value": db}])
	got := deny with input as doc
	has_finding(got, "HCF-POL-006", db.address)
}

test_aws_web_ingress_requires_compute_tag if {
	sg := json.patch(data.fixtures.aws_compliant.resource_changes[8], [{"op": "replace", "path": "/change/after/tags_all/hermes-component", "value": "network"}])
	sg.type == "aws_security_group"
	got := deny with input as {"format_version": "1.2", "planned_values": {}, "resource_changes": [sg]}
	has_finding(got, "HCF-POL-004", sg.address)
}

# ---------------------------------------------------------------- exceptions

# Exceptions must expire within 90 days of now, so the test dates are relative to now.
future := time.format([time.add_date(time.now_ns(), 0, 0, 30), "UTC", "2006-01-02"])

too_far := time.format([time.add_date(time.now_ns(), 0, 0, 120), "UTC", "2006-01-02"])

test_exception_suppresses_one_finding if {
	addr := concat("", [aws_b, "aws_secretsmanager_secret_version.value"])
	got := finding_keys(deny) with input as data.fixtures.aws_violations
		with data.hermes.exceptions as [{"rule": "HCF-POL-009", "address": addr, "reason": "bootstrap value, rotated after first apply", "expires": future}]
	got == expected_aws_denies - {sprintf("HCF-POL-009: %s", [addr])}
}

# F-13: wildcard exceptions are ignored (and reported); "*" used to switch a rule off entirely.
test_wildcard_exceptions_are_ignored if {
	every pattern in ["*", concat("", [aws_b, "aws_iam_policy.*"])] {
		exc := [{"rule": "HCF-POL-005", "address": pattern, "reason": "reviewed", "expires": future}]
		got := finding_keys(deny) with input as data.fixtures.aws_violations with data.hermes.exceptions as exc
		got == expected_aws_denies
		"HCF-POL-000" in rules_of(warn) with input as data.fixtures.aws_violations with data.hermes.exceptions as exc
	}
}

test_exception_beyond_90_days_is_ignored if {
	addr := concat("", [aws_b, "aws_iam_access_key.ci"])
	exc := [{"rule": "HCF-POL-009", "address": addr, "reason": "long", "expires": too_far}]
	has_finding(deny, "HCF-POL-009", addr) with input as data.fixtures.aws_violations with data.hermes.exceptions as exc
	"HCF-POL-000" in rules_of(warn) with input as data.fixtures.aws_violations with data.hermes.exceptions as exc
}

test_expired_exception_is_ignored_and_reported if {
	addr := concat("", [aws_b, "aws_iam_access_key.ci"])
	exc := [{"rule": "HCF-POL-009", "address": addr, "reason": "temporary", "expires": "2020-01-01"}]
	has_finding(deny, "HCF-POL-009", addr) with input as data.fixtures.aws_violations with data.hermes.exceptions as exc
	"HCF-POL-000" in rules_of(warn) with input as data.fixtures.aws_violations with data.hermes.exceptions as exc
}

test_exception_without_reason_is_ignored if {
	addr := concat("", [aws_b, "aws_iam_access_key.ci"])
	has_finding(deny, "HCF-POL-009", addr) with input as data.fixtures.aws_violations
		with data.hermes.exceptions as [{"rule": "HCF-POL-009", "address": addr, "reason": " ", "expires": future}]
}

test_exception_for_other_rule_does_not_apply if {
	addr := concat("", [aws_b, "aws_db_instance.bad"])
	got := deny with input as data.fixtures.aws_violations
		with data.hermes.exceptions as [{"rule": "HCF-POL-003", "address": addr, "reason": "reviewed", "expires": future}]
	not has_finding(got, "HCF-POL-003", addr)
	has_finding(got, "HCF-POL-002", addr)
}
