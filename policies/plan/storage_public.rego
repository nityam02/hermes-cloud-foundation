# HCF-POL-001: object storage must never be public.
#
# Hermes has no "public bucket" option in its spec, so any public configuration is a defect.
# Covers: AWS S3 (public access block, canned ACLs, ACL grants, bucket policies),
# GCS (public access prevention, uniform bucket-level access, allUsers/allAuthenticatedUsers
# bindings and ACLs, also on other data resources such as Pub/Sub topics and secrets),
# Alibaba OSS (bucket ACL, public access block, bucket policy).
# Unknown values (computed until apply) are not evaluated; see the warn rules below.
package main

pab_flags := ["block_public_acls", "block_public_policy", "ignore_public_acls", "restrict_public_buckets"]

public_canned_acls := {"public-read", "public-read-write", "authenticated-read"}

public_grantee_uris := {
	"http://acs.amazonaws.com/groups/global/AllUsers",
	"http://acs.amazonaws.com/groups/global/AuthenticatedUsers",
}

public_members := {"allUsers", "allAuthenticatedUsers"}

# ---------------------------------------------------------------- AWS

deny contains message("HCF-POL-001", rc, sprintf("S3 public access block has %s = false; all four flags must be true", [flag])) if {
	some rc in planned_resources
	rc.type in {"aws_s3_bucket_public_access_block", "aws_s3_account_public_access_block"}
	some flag in pab_flags
	attr_false(rc, flag)
	not excepted("HCF-POL-001", rc.address)
}

deny contains message("HCF-POL-001", rc, sprintf("public canned ACL %q", [rc.change.after.acl])) if {
	some rc in planned_resources
	rc.type in {"aws_s3_bucket", "aws_s3_bucket_acl", "aws_s3_object"}
	rc.change.after.acl in public_canned_acls
	not excepted("HCF-POL-001", rc.address)
}

deny contains message("HCF-POL-001", rc, "ACL grant to AllUsers or AuthenticatedUsers") if {
	some rc in planned_resources
	rc.type == "aws_s3_bucket_acl"
	some acp in rc.change.after.access_control_policy
	some grant in acp.grant
	some grantee in grant.grantee
	grantee.uri in public_grantee_uris
	not excepted("HCF-POL-001", rc.address)
}

deny contains message("HCF-POL-001", rc, "bucket policy allows a public principal (\"*\") without a condition") if {
	some rc in planned_resources
	rc.type in {"aws_s3_bucket_policy", "alicloud_oss_bucket_policy"}
	some s in statements(rc.change.after.policy)
	allow_statement(s)
	public_principal(s.Principal)
	not has_condition(s)
	not excepted("HCF-POL-001", rc.address)
}

warn contains sprintf("HCF-POL-001: %s: %d S3 bucket(s) but %d aws_s3_bucket_public_access_block resource(s) in this module; every bucket needs a public access block", [m, nb, np]) if {
	some m in {module_of(rc) | some rc in planned_resources; rc.type == "aws_s3_bucket"}
	nb := count([rc | some rc in planned_resources; rc.type == "aws_s3_bucket"; module_of(rc) == m])
	np := count([rc | some rc in planned_resources; rc.type == "aws_s3_bucket_public_access_block"; module_of(rc) == m])
	np < nb
}

# ---------------------------------------------------------------- Google Cloud

deny contains message("HCF-POL-001", rc, sprintf("public_access_prevention is %q; it must be \"enforced\"", [rc.change.after.public_access_prevention])) if {
	some rc in planned_resources
	rc.type == "google_storage_bucket"
	is_string(rc.change.after.public_access_prevention)
	rc.change.after.public_access_prevention != "enforced"
	not excepted("HCF-POL-001", rc.address)
}

warn contains message("HCF-POL-001", rc, "public_access_prevention is not set to \"enforced\" in configuration (inherits the organization policy); cannot verify the bucket is private") if {
	some rc in planned_resources
	rc.type == "google_storage_bucket"
	not is_string(object.get(rc.change.after, "public_access_prevention", null))
}

deny contains message("HCF-POL-001", rc, "uniform_bucket_level_access = false allows object ACLs, including public ones") if {
	some rc in planned_resources
	rc.type == "google_storage_bucket"
	attr_false(rc, "uniform_bucket_level_access")
	not excepted("HCF-POL-001", rc.address)
}

# IAM members/bindings on data resources. Cloud Run invoker bindings are deliberately not
# covered: public services legitimately grant roles/run.invoker to allUsers.
google_data_iam_type(t) if {
	regex.match(`^google_(storage_bucket|pubsub_topic|pubsub_subscription|secret_manager_secret|kms_crypto_key|kms_key_ring|bigquery_dataset|bigquery_table|artifact_registry_repository|sql_database_instance)_iam_(member|binding)$`, t)
}

google_data_iam_policy_type(t) if {
	regex.match(`^google_(storage_bucket|pubsub_topic|pubsub_subscription|secret_manager_secret|kms_crypto_key|kms_key_ring|bigquery_dataset|bigquery_table|artifact_registry_repository)_iam_policy$`, t)
}

iam_members(after) := {m | some m in as_list(object.get(after, "member", []))} | {m | some m in as_list(object.get(after, "members", []))}

deny contains message("HCF-POL-001", rc, sprintf("grants %s on a data resource to allUsers/allAuthenticatedUsers", [rc.change.after.role])) if {
	some rc in planned_resources
	google_data_iam_type(rc.type)
	some m in iam_members(rc.change.after)
	m in public_members
	not excepted("HCF-POL-001", rc.address)
}

deny contains message("HCF-POL-001", rc, "IAM policy on a data resource includes allUsers/allAuthenticatedUsers") if {
	some rc in planned_resources
	google_data_iam_policy_type(rc.type)
	some b in parsed_document(rc.change.after.policy_data).bindings
	some m in b.members
	m in public_members
	not excepted("HCF-POL-001", rc.address)
}

deny contains message("HCF-POL-001", rc, "ACL grants access to allUsers/allAuthenticatedUsers") if {
	some rc in planned_resources
	rc.type in {"google_storage_bucket_acl", "google_storage_default_object_acl", "google_storage_object_acl"}
	some entity in object.get(rc.change.after, "role_entity", [])
	some pm in public_members
	contains(entity, pm)
	not excepted("HCF-POL-001", rc.address)
}

deny contains message("HCF-POL-001", rc, sprintf("public predefined ACL %q", [rc.change.after.predefined_acl])) if {
	some rc in planned_resources
	rc.type in {"google_storage_bucket_acl", "google_storage_default_object_acl", "google_storage_object_acl", "google_storage_bucket_object"}
	lower(rc.change.after.predefined_acl) in {"publicread", "publicreadwrite", "authenticatedread"}
	not excepted("HCF-POL-001", rc.address)
}

# ---------------------------------------------------------------- Alibaba Cloud

oss_public_acls := {"public-read", "public-read-write"}

deny contains message("HCF-POL-001", rc, sprintf("OSS ACL %q is public; use \"private\"", [rc.change.after.acl])) if {
	some rc in planned_resources
	rc.type in {"alicloud_oss_bucket", "alicloud_oss_bucket_acl", "alicloud_oss_bucket_object"}
	rc.change.after.acl in oss_public_acls
	not excepted("HCF-POL-001", rc.address)
}

deny contains message("HCF-POL-001", rc, "OSS block_public_access = false") if {
	some rc in planned_resources
	rc.type in {"alicloud_oss_bucket_public_access_block", "alicloud_oss_account_public_access_block"}
	attr_false(rc, "block_public_access")
	not excepted("HCF-POL-001", rc.address)
}

deny contains message("HCF-POL-001", rc, "inline OSS bucket policy allows a public principal (\"*\") without a condition") if {
	some rc in planned_resources
	rc.type == "alicloud_oss_bucket"
	some s in statements(rc.change.after.policy)
	allow_statement(s)
	public_principal(s.Principal)
	not has_condition(s)
	not excepted("HCF-POL-001", rc.address)
}

warn contains sprintf("HCF-POL-001: %s: %d OSS bucket(s) but %d alicloud_oss_bucket_public_access_block resource(s) in this module; every bucket needs a public access block", [m, nb, np]) if {
	some m in {module_of(rc) | some rc in planned_resources; rc.type == "alicloud_oss_bucket"}
	nb := count([rc | some rc in planned_resources; rc.type == "alicloud_oss_bucket"; module_of(rc) == m])
	np := count([rc | some rc in planned_resources; rc.type == "alicloud_oss_bucket_public_access_block"; module_of(rc) == m])
	np < nb
}

warn contains sprintf("HCF-POL-001: %s: %d OSS bucket(s) but %d alicloud_oss_bucket_acl resource(s) in this module; set an explicit private ACL for every bucket", [m, nb, na]) if {
	some m in {module_of(rc) | some rc in planned_resources; rc.type == "alicloud_oss_bucket"}
	nb := count([rc | some rc in planned_resources; rc.type == "alicloud_oss_bucket"; module_of(rc) == m])
	na := count([rc | some rc in planned_resources; rc.type == "alicloud_oss_bucket_acl"; module_of(rc) == m])
	na < nb
}
