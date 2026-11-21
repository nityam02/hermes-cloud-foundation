package main

ali_b := "module.platform.module.bad."

ali_s := "module.platform.module.bad_storage."

expected_alibaba_denies := {sprintf("%s: %s", [r, a]) | some pair in [
	["HCF-POL-001", concat("", [ali_s, "alicloud_oss_bucket.plain"])],
	["HCF-POL-001", concat("", [ali_b, "alicloud_oss_bucket_acl.public"])],
	["HCF-POL-001", concat("", [ali_b, "alicloud_oss_bucket_public_access_block.off"])],
	["HCF-POL-001", concat("", [ali_b, "alicloud_oss_bucket_policy.public"])],
	["HCF-POL-002", concat("", [ali_s, "alicloud_oss_bucket.plain"])],
	["HCF-POL-002", concat("", [ali_b, "alicloud_db_instance.bad"])],
	["HCF-POL-003", concat("", [ali_b, "alicloud_db_instance.bad"])],
	["HCF-POL-003", concat("", [ali_b, "alicloud_db_connection.public"])],
	["HCF-POL-004", concat("", [ali_b, "alicloud_security_group_rule.ssh"])],
	["HCF-POL-004", concat("", [ali_b, "alicloud_security_group_rule.web_unmarked"])],
	["HCF-POL-004", concat("", [ali_b, "alicloud_security_group_rule.all_v6"])],
	["HCF-POL-005", concat("", [ali_b, "alicloud_ram_policy.star"])],
	["HCF-POL-005", concat("", [ali_b, "alicloud_ram_policy.oss_star"])],
	["HCF-POL-005", concat("", [ali_b, "alicloud_ram_policy.legacy_document"])],
	["HCF-POL-006", concat("", [ali_b, "alicloud_db_instance.bad"])],
	["HCF-POL-008", concat("", [ali_b, "alicloud_db_instance.untagged"])],
	["HCF-POL-009", concat("", [ali_b, "alicloud_kms_secret.real_value"])],
	["HCF-POL-009", concat("", [ali_b, "alicloud_ram_access_key.ci"])],
	["HCF-POL-009", concat("", [ali_b, "alicloud_db_account.password"])],
	["HCF-POL-010", concat("", [ali_b, "alicloud_ram_role.no_sub"])],
	["HCF-POL-010", concat("", [ali_b, "alicloud_ram_role.public"])],
]; r := pair[0]; a := pair[1]}

test_alibaba_compliant_plan_passes if {
	count(deny) == 0 with input as data.fixtures.alibaba_compliant
	count(warn) == 0 with input as data.fixtures.alibaba_compliant
}

test_alibaba_violations_exact if {
	got := finding_keys(deny) with input as data.fixtures.alibaba_violations
	got == expected_alibaba_denies
}

# security_ips entries may be comma-joined lists.
test_alibaba_comma_joined_security_ips if {
	msgs := deny with input as data.fixtures.alibaba_violations
	some m in msgs
	startswith(m, concat("", ["HCF-POL-003: ", ali_b, "alicloud_db_instance.bad: ApsaraDB RDS security_ips"]))
}

# The placeholder the Alibaba secrets module writes is allowed; the list is configurable.
test_alibaba_placeholder_is_configurable if {
	addr := "module.platform.module.secrets.alicloud_kms_secret.this[\"db-password\"]"
	got := deny with input as data.fixtures.alibaba_compliant with data.hermes.secret_placeholders as ["other-placeholder"]
	has_finding(got, "HCF-POL-009", addr)
}

# A separate alicloud_oss_bucket_server_side_encryption resource in the module satisfies SSE.
test_alibaba_sse_resource_satisfies_encryption if {
	doc := json.patch(data.fixtures.alibaba_violations, [{"op": "add", "path": "/resource_changes/-", "value": {
		"address": concat("", [ali_s, "alicloud_oss_bucket_server_side_encryption.this"]),
		"module_address": "module.platform.module.bad_storage",
		"mode": "managed", "type": "alicloud_oss_bucket_server_side_encryption",
		"change": {"actions": ["create"], "after": {"bucket": "b", "sse_algorithm": "KMS"}, "after_unknown": {}},
	}}])
	got := deny with input as doc
	not has_finding(got, "HCF-POL-002", concat("", [ali_s, "alicloud_oss_bucket.plain"]))
}

test_alibaba_bucket_without_acl_or_block_warns if {
	got := warn with input as data.fixtures.alibaba_violations
	count([m | some m in got; startswith(m, "HCF-POL-001: module.platform.module.bad_storage:")]) == 2
}
