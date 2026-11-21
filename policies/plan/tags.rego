# HCF-POL-008: every taggable resource carries the Hermes ownership tags/labels
# (data-contracts §2): hermes-managed-by = "hermes" and hermes-environment (deny, because
# ownership and the protected-environment rules depend on them), hermes-project and
# hermes-component (warn).
# A resource is taggable when its planned state has a tags/labels attribute. Tags that are
# unknown until apply are not evaluated.
package main

required_tags := ["hermes-managed-by", "hermes-environment"]

recommended_tags := ["hermes-project", "hermes-component"]

deny contains message("HCF-POL-008", rc, "tag/label hermes-managed-by must be \"hermes\"") if {
	some rc in planned_resources
	taggable(rc)
	not tags_unknown(rc)
	resource_tags(rc)["hermes-managed-by"] != "hermes"
	not excepted("HCF-POL-008", rc.address)
}

deny contains message("HCF-POL-008", rc, sprintf("missing tag/label %s", [key])) if {
	some rc in planned_resources
	taggable(rc)
	not tags_unknown(rc)
	some key in required_tags
	not resource_tags(rc)[key]
	not excepted("HCF-POL-008", rc.address)
}

warn contains message("HCF-POL-008", rc, sprintf("missing tag/label %s", [key])) if {
	some rc in planned_resources
	taggable(rc)
	not tags_unknown(rc)
	some key in recommended_tags
	not resource_tags(rc)[key]
}
