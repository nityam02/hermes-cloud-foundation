# Test helpers. Fixtures are synthetic `show -json` plan documents under fixtures/<name>/plan.json,
# loaded with `--data policies/tests` and therefore available as data.fixtures.<name>.
package main

# "HCF-POL-nnn: <address>" for every message (drops the explanation text).
finding_keys(msgs) := {concat(": ", array.slice(split(m, ": "), 0, 2)) | some m in msgs}

has_finding(msgs, rule, address) if {
	sprintf("%s: %s", [rule, address]) in finding_keys(msgs)
}

rules_of(msgs) := {split(m, ": ")[0] | some m in msgs}

# The rule ids that have deny coverage in the fixtures; test_every_rule_has_failing_fixture checks it.
all_deny_rules := {
	"HCF-POL-000", "HCF-POL-001", "HCF-POL-002", "HCF-POL-003", "HCF-POL-004", "HCF-POL-005",
	"HCF-POL-006", "HCF-POL-008", "HCF-POL-009", "HCF-POL-010", "HCF-POL-011",
}
