# Shared helpers for the Hermes plan policies.
#
# Input: the JSON document produced by `tofu show -json <planfile>` (or `terraform show -json`).
# Every rule message starts with a stable rule id "HCF-POL-nnn: <resource address>: ...".
# Messages never contain attribute values that could be secret.
#
# Data (optional, loaded with `conftest test --data <file>`; see policies/README.md):
#   hermes:
#     protected: true                         # the CLI knows the environment is protected
#     protected_environments: [production]    # default when absent: ["production"]
#     secret_placeholders: [set-out-of-band]  # allowed placeholder values for alicloud_kms_secret
#     exceptions:                             # time-boxed, per-address exceptions
#       - rule: HCF-POL-009                   # one rule id (no wildcards)
#         address: module.platform.aws_secretsmanager_secret_version.bootstrap  # exact address
#         reason: "why this is acceptable"
#         expires: "2027-01-31"               # at most 90 days from now
package main

# ---------------------------------------------------------------- configuration

default_protected_environments := ["production"]

protected_environments := data.hermes.protected_environments if {
	is_array(data.hermes.protected_environments)
} else := default_protected_environments

default_secret_placeholders := ["set-out-of-band"]

secret_placeholders := data.hermes.secret_placeholders if {
	is_array(data.hermes.secret_placeholders)
} else := default_secret_placeholders

exceptions := data.hermes.exceptions if {
	is_array(data.hermes.exceptions)
} else := []

# ---------------------------------------------------------------- resource selection

# Managed resources that exist after the plan is applied (create, update, no-op, replace).
planned_resources contains rc if {
	some rc in input.resource_changes
	rc.mode == "managed"
	is_object(rc.change.after)
}

# Managed resources the plan deletes (delete or replace).
deleted_resources contains rc if {
	some rc in input.resource_changes
	rc.mode == "managed"
	"delete" in rc.change.actions
}

module_of(rc) := object.get(rc, "module_address", "")

after_unknown(rc, attr) if {
	rc.change.after_unknown[attr] == true
}

# ---------------------------------------------------------------- tags and labels

# The effective tags/labels of a planned resource, or {} when it has none we can read.
# AWS: tags_all (includes provider default_tags) then tags. Alibaba: tags.
# Google: terraform_labels (includes provider default_labels), effective_labels, user_labels,
# settings[0].user_labels (Cloud SQL). Google `labels` alone is not used because some resources
# (for example monitoring notification channels) use `labels` for configuration, not metadata.
resource_tags(rc) := t if {
	t := rc.change.after.tags_all
	is_object(t)
} else := t if {
	t := rc.change.after.tags
	is_object(t)
} else := t if {
	t := rc.change.after.terraform_labels
	is_object(t)
} else := t if {
	t := rc.change.after.effective_labels
	is_object(t)
} else := t if {
	t := rc.change.after.user_labels
	is_object(t)
} else := t if {
	t := rc.change.after.settings[0].user_labels
	is_object(t)
} else := {}

tag_attributes := ["tags_all", "tags", "terraform_labels", "effective_labels", "user_labels"]

# The resource type supports tags/labels (the attribute exists in the planned state).
taggable(rc) if {
	some attr in tag_attributes
	attr in object.keys(rc.change.after)
}

taggable(rc) if {
	rc.type == "google_sql_database_instance"
}

# Tags cannot be evaluated because they are unknown until apply.
# Note: negations use object.get defaults because `not f(<undefined ref>)` does not succeed in Rego.
tags_unknown(rc) if {
	some attr in ["tags_all", "terraform_labels", "effective_labels"]
	after_unknown(rc, attr)
	not is_object(object.get(rc.change.after, attr, null))
}

# ---------------------------------------------------------------- environments

# The plan as a whole targets a protected environment.
plan_protected if {
	data.hermes.protected == true
}

plan_protected if {
	some call in input.configuration.root_module.module_calls
	call.expressions.protected.constant_value == true
}

plan_protected if {
	input.variables.protected.value == true
}

resource_environment(rc) := env if {
	env := resource_tags(rc)["hermes-environment"]
	is_string(env)
}

in_protected_environment(rc) if {
	plan_protected
	rc.address
}

in_protected_environment(rc) if {
	resource_environment(rc) in protected_environments
}

# ---------------------------------------------------------------- exceptions

# An exception names one rule and one exact resource address (no wildcards: "*" would switch a
# rule off for the whole plan), has a reason, and expires within 90 days. hermes also rejects
# invalid exceptions before conftest runs; this is the same rule for direct conftest use.
max_exception_days := 90

valid_exception(e) if {
	is_string(e.rule)
	is_string(e.address)
	not contains(e.rule, "*")
	not contains(e.address, "*")
	count(trim_space(e.address)) > 0
	is_string(e.reason)
	count(trim_space(e.reason)) > 0
	is_string(e.expires)
	expiry := time.parse_ns("2006-01-02", e.expires)
	expiry > time.now_ns()
	expiry <= time.add_date(time.now_ns(), 0, 0, max_exception_days)
}

excepted(rule, address) if {
	some e in exceptions
	valid_exception(e)
	e.rule == rule
	e.address == address
}

# ---------------------------------------------------------------- JSON policy documents

as_list(x) := x if {
	is_array(x)
} else := [x] if {
	is_string(x)
} else := [x] if {
	is_object(x)
} else := []

# Parse a policy document that may be a JSON string (jsonencode) or already an object.
# Unknown or unparseable documents yield nothing (rules warn about unknown documents separately).
parsed_document(doc) := doc if {
	is_object(doc)
} else := json.unmarshal(doc) if {
	is_string(doc)
	json.is_valid(doc)
}

statements(doc) := [s | some s in as_list(parsed_document(doc).Statement)]

allow_statement(s) if {
	lower(s.Effect) == "allow"
}

# Principal "*", ["*"], {"AWS": "*"}, {"AWS": ["*"]}, {"RAM": ["*"]} ...
public_principal(p) if {
	p == "*"
}

public_principal(p) if {
	is_array(p)
	"*" in p
}

public_principal(p) if {
	is_object(p)
	some v in p
	some x in as_list(v)
	x == "*"
}

has_condition(s) if {
	is_object(s.Condition)
	count(s.Condition) > 0
}

# ---------------------------------------------------------------- small utilities

open_cidrs := {"0.0.0.0/0", "::/0", "0.0.0.0"}

message(rule, rc, text) := sprintf("%s: %s: %s", [rule, rc.address, text])

attr_false(rc, attr) if {
	rc.change.after[attr] == false
}

attr_true(rc, attr) if {
	rc.change.after[attr] == true
}

empty_or_null(x) if {
	x == null
}

empty_or_null(x) if {
	x == ""
}

# ---------------------------------------------------------------- HCF-POL-000: input sanity

deny contains "HCF-POL-000: input is not a plan JSON document (missing format_version); evaluate `tofu show -json <planfile>`" if {
	not input.format_version
}

deny contains "HCF-POL-000: input has no planned_values; it looks like a state document, not a plan; evaluate `tofu show -json <planfile>`" if {
	input.format_version
	not input.planned_values
}

deny contains "HCF-POL-000: the plan errored; policies cannot evaluate an incomplete plan" if {
	input.errored == true
}

warn contains msg if {
	some e in exceptions
	not valid_exception(e)
	msg := sprintf("HCF-POL-000: ignoring an invalid or expired policy exception for rule %v address %v (needs one rule, one exact address without wildcards, a non-empty reason and an expires date YYYY-MM-DD within 90 days)", [object.get(e, "rule", "?"), object.get(e, "address", "?")])
}
