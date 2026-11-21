# HCF-POL-005: identity policies must not grant wildcards.
#   - Action "*" (or "*:*") in an Allow statement;
#   - Allow with NotAction (grants everything not listed);
#   - Resource "*" on a data-plane action (object storage, queues, topics, keys, secrets,
#     tables, streams, logs), except list/describe calls that only support "*";
#   - iam:PassRole on Resource "*" and service-wide wildcards on identity services
#     (iam:*, sts:*, ram:*, organizations:*), which allow privilege escalation;
#   - GCP basic roles roles/owner and roles/editor (deny) and roles/viewer (warn) on
#     projects, folders and organizations.
# Only identity policies are evaluated. Resource policies (bucket/key/queue policies) have
# different semantics (a KMS key policy's Resource "*" means "this key").
# Policy documents that are unknown until apply (built from attributes of resources created
# in the same plan) cannot be evaluated: they produce a warning, never a silent pass.
#
# HCF-POL-010: trust policies must not let anyone assume a role.
#   - Allow with a public principal and no condition;
#   - web-identity federation (GitHub Actions OIDC and others) without a pinned subject
#     condition, or with a wildcard subject (StringLike "repo:org/*");
#   - GCP workload identity pool providers without an attribute_condition (any repository
#     on a shared issuer such as GitHub could federate).
package main

# ---------------------------------------------------------------- identity policy documents

# {"rc": <resource change>, "doc": <policy string or object>}
identity_policy_docs contains {"rc": rc, "doc": rc.change.after.policy} if {
	some rc in planned_resources
	rc.type in {"aws_iam_policy", "aws_iam_role_policy", "aws_iam_user_policy", "aws_iam_group_policy"}
	rc.change.after.policy
}

identity_policy_docs contains {"rc": rc, "doc": p.policy} if {
	some rc in planned_resources
	rc.type == "aws_iam_role"
	some p in object.get(rc.change.after, "inline_policy", [])
	p.policy
}

identity_policy_docs contains {"rc": rc, "doc": doc} if {
	some rc in planned_resources
	rc.type == "alicloud_ram_policy"
	some attr in ["policy_document", "document"]
	doc := rc.change.after[attr]
	is_string(doc)
	doc != ""
}

warn contains message("HCF-POL-005", rc, "policy document is unknown until apply and was not evaluated; review it in the saved plan after apply or build it from known values") if {
	some rc in planned_resources
	rc.type in {"aws_iam_policy", "aws_iam_role_policy", "aws_iam_user_policy", "aws_iam_group_policy", "alicloud_ram_policy"}
	some attr in ["policy", "policy_document"]
	after_unknown(rc, attr)
}

full_wildcard_actions := {"*", "*:*"}

# Services whose actions read or write customer data (AWS and Alibaba Cloud prefixes).
data_plane_services := {
	"s3", "sqs", "sns", "dynamodb", "kms", "secretsmanager", "ssm", "kinesis", "firehose",
	"rds-db", "rds-data", "elasticfilesystem", "glacier", "es", "lambda", "logs", "ecr",
	"oss", "mns", "ots", "log",
}

identity_services := {"iam", "sts", "ram", "organizations"}

# Actions in data-plane services that AWS/Alibaba only allow on Resource "*".
resource_less_actions := {"kms:createkey", "kms:generaterandom", "secretsmanager:getrandompassword", "ecr:getauthorizationtoken", "logs:describeloggroups"}

action_service(a) := lower(split(a, ":")[0])

action_name(a) := lower(concat(":", array.slice(split(a, ":"), 1, count(split(a, ":")))))

# Metadata/configuration reads (bucket settings, queue attributes, policies) are not data access.
metadata_action_name(n) if {
	regex.match(`^(list|describe|getbucket|getproject$|getlogstore$|getindex$|getkeyrotationstatus$)|(attributes|policy|acl|tagging|tags)$`, n)
}

data_plane_action(a) if {
	action_service(a) in data_plane_services
	not metadata_action_name(action_name(a))
	not lower(a) in resource_less_actions
}

wildcard_resource(s) if {
	some r in as_list(s.Resource)
	r == "*"
}

deny contains message("HCF-POL-005", d.rc, "Allow statement with Action \"*\"") if {
	some d in identity_policy_docs
	some s in statements(d.doc)
	allow_statement(s)
	some a in as_list(s.Action)
	a in full_wildcard_actions
	not excepted("HCF-POL-005", d.rc.address)
}

deny contains message("HCF-POL-005", d.rc, "Allow statement with NotAction grants every other action") if {
	some d in identity_policy_docs
	some s in statements(d.doc)
	allow_statement(s)
	s.NotAction
	not excepted("HCF-POL-005", d.rc.address)
}

deny contains message("HCF-POL-005", d.rc, sprintf("data-plane action %q on Resource \"*\"; scope it to the resources the identity needs", [a])) if {
	some d in identity_policy_docs
	some s in statements(d.doc)
	allow_statement(s)
	wildcard_resource(s)
	some a in as_list(s.Action)
	not a in full_wildcard_actions
	data_plane_action(a)
	not excepted("HCF-POL-005", d.rc.address)
}

deny contains message("HCF-POL-005", d.rc, "iam:PassRole on Resource \"*\" allows passing any role (privilege escalation)") if {
	some d in identity_policy_docs
	some s in statements(d.doc)
	allow_statement(s)
	wildcard_resource(s)
	some a in as_list(s.Action)
	lower(a) in {"iam:passrole", "iam:pass*", "ram:passrole"}
	not excepted("HCF-POL-005", d.rc.address)
}

deny contains message("HCF-POL-005", d.rc, sprintf("service-wide wildcard %q on an identity service allows privilege escalation", [a])) if {
	some d in identity_policy_docs
	some s in statements(d.doc)
	allow_statement(s)
	some a in as_list(s.Action)
	action_service(a) in identity_services
	action_name(a) == "*"
	not excepted("HCF-POL-005", d.rc.address)
}

# ---------------------------------------------------------------- GCP basic roles

gcp_scope_iam_type(t) if {
	regex.match(`^google_(project|folder|organization)_iam_(member|binding)$`, t)
}

deny contains message("HCF-POL-005", rc, sprintf("grants the basic role %s; use predefined or custom roles", [rc.change.after.role])) if {
	some rc in planned_resources
	gcp_scope_iam_type(rc.type)
	rc.change.after.role in {"roles/owner", "roles/editor"}
	not excepted("HCF-POL-005", rc.address)
}

deny contains message("HCF-POL-005", rc, sprintf("IAM policy grants the basic role %s", [b.role])) if {
	some rc in planned_resources
	regex.match(`^google_(project|folder|organization)_iam_policy$`, rc.type)
	some b in parsed_document(rc.change.after.policy_data).bindings
	b.role in {"roles/owner", "roles/editor"}
	not excepted("HCF-POL-005", rc.address)
}

warn contains message("HCF-POL-005", rc, "grants roles/viewer, which can read object data in buckets that keep the default projectViewer bindings; prefer service-specific viewer roles") if {
	some rc in planned_resources
	gcp_scope_iam_type(rc.type)
	rc.change.after.role == "roles/viewer"
}

# ---------------------------------------------------------------- HCF-POL-010: trust policies

trust_docs contains {"rc": rc, "doc": rc.change.after.assume_role_policy} if {
	some rc in planned_resources
	rc.type == "aws_iam_role"
	rc.change.after.assume_role_policy
}

trust_docs contains {"rc": rc, "doc": doc} if {
	some rc in planned_resources
	rc.type == "alicloud_ram_role"
	some attr in ["assume_role_policy_document", "document"]
	doc := rc.change.after[attr]
	is_string(doc)
	doc != ""
}

deny contains message("HCF-POL-010", d.rc, "trust policy lets any principal (\"*\") assume the role without a condition") if {
	some d in trust_docs
	some s in statements(d.doc)
	allow_statement(s)
	public_principal(s.Principal)
	not has_condition(s)
	not excepted("HCF-POL-010", d.rc.address)
}

federated_statement(s) if {
	s.Principal.Federated
}

# Condition entries whose key is a subject claim: "<issuer>:sub" (AWS) or "oidc:sub" (Alibaba).
subject_conditions(s) := [{"op": op, "values": as_list(v)} |
	some op, kv in s.Condition
	some key, v in kv
	endswith(lower(key), ":sub")
]

deny contains message("HCF-POL-010", d.rc, "web-identity trust has no subject (sub) condition; any identity from the issuer could assume the role") if {
	some d in trust_docs
	some s in statements(d.doc)
	allow_statement(s)
	federated_statement(s)
	count(subject_conditions(s)) == 0
	not excepted("HCF-POL-010", d.rc.address)
}

deny contains message("HCF-POL-010", d.rc, sprintf("web-identity trust uses a wildcard subject (%s); pin exact subjects with StringEquals", [c.op])) if {
	some d in trust_docs
	some s in statements(d.doc)
	allow_statement(s)
	federated_statement(s)
	some c in subject_conditions(s)
	some v in c.values
	regex.match(`[*?]`, v)
	not excepted("HCF-POL-010", d.rc.address)
}

deny contains message("HCF-POL-010", rc, "workload identity pool provider has no attribute_condition; every identity from the issuer (for example any GitHub repository) could federate") if {
	some rc in planned_resources
	rc.type == "google_iam_workload_identity_pool_provider"
	empty_or_null(object.get(rc.change.after, "attribute_condition", null))
	not excepted("HCF-POL-010", rc.address)
}

deny contains message("HCF-POL-010", rc, sprintf("grants %s to a whole workload identity pool; bind a precise principal or principalSet attribute", [rc.change.after.role])) if {
	some rc in planned_resources
	rc.type == "google_service_account_iam_member"
	rc.change.after.role in {"roles/iam.workloadIdentityUser", "roles/iam.serviceAccountTokenCreator"}
	regex.match(`^principalSet://iam\.googleapis\.com/.+/workloadIdentityPools/[^/]+/\*$`, rc.change.after.member)
	not excepted("HCF-POL-010", rc.address)
}
