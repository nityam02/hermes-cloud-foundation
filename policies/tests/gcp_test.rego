package main

gcp_b := "module.platform.module.bad."

expected_gcp_denies := {sprintf("%s: %s%s", [r, gcp_b, a]) | some pair in [
	["HCF-POL-001", "google_storage_bucket.inherited"],
	["HCF-POL-001", "google_storage_bucket.acls"],
	["HCF-POL-001", "google_storage_bucket_iam_member.public"],
	["HCF-POL-001", "google_storage_bucket_iam_binding.public"],
	["HCF-POL-001", "google_pubsub_topic_iam_member.public"],
	["HCF-POL-002", "google_sql_database_instance.bad"],
	["HCF-POL-003", "google_sql_database_instance.bad"],
	["HCF-POL-004", "google_compute_firewall.ssh"],
	["HCF-POL-004", "google_compute_firewall.web_unmarked"],
	["HCF-POL-005", "google_project_iam_member.editor"],
	["HCF-POL-006", "google_sql_database_instance.bad"],
	["HCF-POL-008", "google_pubsub_topic.unlabeled"],
	["HCF-POL-009", "google_sql_user.password"],
	["HCF-POL-009", "google_secret_manager_secret_version.value"],
	["HCF-POL-009", "google_service_account_key.ci"],
	["HCF-POL-010", "google_iam_workload_identity_pool_provider.open"],
	["HCF-POL-010", "google_service_account_iam_member.whole_pool"],
	["HCF-POL-011", "google_sql_database_instance.bad"],
]; r := pair[0]; a := pair[1]}

test_gcp_compliant_plan_passes if {
	count(deny) == 0 with input as data.fixtures.gcp_compliant
	count(warn) == 0 with input as data.fixtures.gcp_compliant
}

test_gcp_violations_exact if {
	got := finding_keys(deny) with input as data.fixtures.gcp_violations
	got == expected_gcp_denies
}

# Cloud Run public services legitimately grant roles/run.invoker to allUsers.
test_gcp_public_cloud_run_invoker_allowed if {
	got := deny with input as data.fixtures.gcp_compliant
	not has_finding(got, "HCF-POL-001", "module.platform.module.compute.google_cloud_run_v2_service_iam_member.public[\"api\"]")
}

test_gcp_both_cloud_sql_public_findings if {
	msgs := deny with input as data.fixtures.gcp_violations
	count([m | some m in msgs; startswith(m, "HCF-POL-003")]) == 2
	count([m | some m in msgs; startswith(m, "HCF-POL-011")]) == 2
}

test_gcp_viewer_role_warns if {
	b := {"address": "google_project_iam_member.plan", "mode": "managed", "type": "google_project_iam_member", "change": {"actions": ["create"], "after": {"role": "roles/viewer", "member": "serviceAccount:plan@sample-project.iam.gserviceaccount.com"}, "after_unknown": {}}}
	doc := {"format_version": "1.2", "planned_values": {}, "resource_changes": [b]}
	count(deny) == 0 with input as doc
	has_finding(warn, "HCF-POL-005", "google_project_iam_member.plan") with input as doc
}
