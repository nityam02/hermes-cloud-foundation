# HCF-POL-004: no ingress from the whole internet (0.0.0.0/0 or ::/0), except TCP 80/443 on
# resources that front public services (load balancers), marked hermes-component = compute.
#
# How the exception is recognised:
#   - resources that support tags (aws_security_group, aws_vpc_security_group_ingress_rule):
#     tag hermes-component = "compute";
#   - rule resources without tags (aws_security_group_rule, google_compute_firewall,
#     alicloud_security_group_rule): the description contains "hermes-component=compute".
# Any other public ingress (SSH, RDP, database ports, all protocols) is denied.
package main

web_ports := {80, 443}

tcp_protocols := {"tcp", "6"}

public_web_ports(from, to, protocol) if {
	from == to
	from in web_ports
	lower(sprintf("%v", [protocol])) in tcp_protocols
}

port_desc(from, to, protocol) := "all traffic" if {
	lower(sprintf("%v", [protocol])) in {"-1", "all"}
} else := sprintf("%v-%v/%v", [from, to, protocol])

compute_tagged(rc) if {
	resource_tags(rc)["hermes-component"] == "compute"
}

compute_described(rc) if {
	contains(object.get(rc.change.after, "description", ""), "hermes-component=compute")
}

open_ranges(cidrs) if {
	some c in as_list(cidrs)
	c in open_cidrs
}

# ---------------------------------------------------------------- AWS

deny contains message("HCF-POL-004", rc, sprintf("inline ingress %s from the internet", [port_desc(rule.from_port, rule.to_port, rule.protocol)])) if {
	some rc in planned_resources
	rc.type == "aws_security_group"
	some rule in rc.change.after.ingress
	open_aws_rule(rule)
	not allowed_web_ingress(rc, rule.from_port, rule.to_port, rule.protocol)
	not excepted("HCF-POL-004", rc.address)
}

open_aws_rule(rule) if open_ranges(object.get(rule, "cidr_blocks", []))

open_aws_rule(rule) if open_ranges(object.get(rule, "ipv6_cidr_blocks", []))

allowed_web_ingress(rc, from, to, protocol) if {
	public_web_ports(from, to, protocol)
	compute_tagged(rc)
}

deny contains message("HCF-POL-004", rc, sprintf("ingress %s from the internet", [port_desc(object.get(rc.change.after, "from_port", null), object.get(rc.change.after, "to_port", null), rc.change.after.ip_protocol)])) if {
	some rc in planned_resources
	rc.type == "aws_vpc_security_group_ingress_rule"
	some attr in ["cidr_ipv4", "cidr_ipv6"]
	rc.change.after[attr] in open_cidrs
	not allowed_web_ingress(rc, object.get(rc.change.after, "from_port", null), object.get(rc.change.after, "to_port", null), rc.change.after.ip_protocol)
	not excepted("HCF-POL-004", rc.address)
}

deny contains message("HCF-POL-004", rc, sprintf("ingress %s from the internet (aws_security_group_rule has no tags; mark load-balancer rules with description \"hermes-component=compute\")", [port_desc(rc.change.after.from_port, rc.change.after.to_port, rc.change.after.protocol)])) if {
	some rc in planned_resources
	rc.type == "aws_security_group_rule"
	rc.change.after.type == "ingress"
	open_aws_rule(rc.change.after)
	not described_web_ingress(rc, rc.change.after.from_port, rc.change.after.to_port, rc.change.after.protocol)
	not excepted("HCF-POL-004", rc.address)
}

described_web_ingress(rc, from, to, protocol) if {
	public_web_ports(from, to, protocol)
	compute_described(rc)
}

# ---------------------------------------------------------------- Google Cloud

gcp_ingress(after) if {
	not after.direction
}

gcp_ingress(after) if {
	after.direction == "INGRESS"
}

gcp_web_only(after) if {
	count(after.allow) > 0
	every a in after.allow {
		lower(a.protocol) == "tcp"
		count(a.ports) > 0
		every p in a.ports {
			p in {"80", "443"}
		}
	}
}

deny contains message("HCF-POL-004", rc, "firewall allows ingress from the internet") if {
	some rc in planned_resources
	rc.type == "google_compute_firewall"
	gcp_ingress(rc.change.after)
	object.get(rc.change.after, "disabled", false) != true
	count(object.get(rc.change.after, "allow", [])) > 0
	open_ranges(object.get(rc.change.after, "source_ranges", []))
	not gcp_allowed_web(rc)
	not excepted("HCF-POL-004", rc.address)
}

gcp_allowed_web(rc) if {
	gcp_web_only(rc.change.after)
	compute_described(rc)
}

# ---------------------------------------------------------------- Alibaba Cloud

ali_web_port_ranges := {"80/80", "443/443"}

deny contains message("HCF-POL-004", rc, sprintf("security group rule allows %v/%v from the internet", [rc.change.after.port_range, rc.change.after.ip_protocol])) if {
	some rc in planned_resources
	rc.type == "alicloud_security_group_rule"
	rc.change.after.type == "ingress"
	object.get(rc.change.after, "policy", "accept") != "drop"
	some attr in ["cidr_ip", "ipv6_cidr_ip"]
	rc.change.after[attr] in open_cidrs
	not ali_allowed_web(rc)
	not excepted("HCF-POL-004", rc.address)
}

ali_allowed_web(rc) if {
	lower(rc.change.after.ip_protocol) == "tcp"
	rc.change.after.port_range in ali_web_port_ranges
	compute_described(rc)
}
