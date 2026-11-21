# HCF-POL-003: databases must not be reachable from the internet.
#
# Hermes databases are private by contract (data-contracts §1): services reach them over the
# private network. A public endpoint is always a defect.
package main

deny contains message("HCF-POL-003", rc, "publicly_accessible = true") if {
	some rc in planned_resources
	rc.type in {"aws_db_instance", "aws_rds_cluster_instance", "aws_redshift_cluster"}
	attr_true(rc, "publicly_accessible")
	not excepted("HCF-POL-003", rc.address)
}

deny contains message("HCF-POL-003", rc, "Cloud SQL ip_configuration.ipv4_enabled = true assigns a public IP; use private IP only") if {
	some rc in planned_resources
	rc.type == "google_sql_database_instance"
	some s in rc.change.after.settings
	some ip in s.ip_configuration
	ip.ipv4_enabled == true
	not excepted("HCF-POL-003", rc.address)
}

deny contains message("HCF-POL-003", rc, "Cloud SQL authorized network allows the whole internet") if {
	some rc in planned_resources
	rc.type == "google_sql_database_instance"
	some s in rc.change.after.settings
	some ip in s.ip_configuration
	some net in ip.authorized_networks
	net.value in open_cidrs
	not excepted("HCF-POL-003", rc.address)
}

deny contains message("HCF-POL-003", rc, "ApsaraDB RDS security_ips allow the whole internet (0.0.0.0/0)") if {
	some rc in planned_resources
	rc.type in {"alicloud_db_instance", "alicloud_polardb_cluster"}
	some raw in as_list(object.get(rc.change.after, "security_ips", []))
	some ip in split(raw, ",")
	trim_space(ip) in open_cidrs
	not excepted("HCF-POL-003", rc.address)
}

deny contains message("HCF-POL-003", rc, "allocates a public (Internet) connection string for an ApsaraDB RDS instance") if {
	some rc in planned_resources
	rc.type in {"alicloud_db_connection", "alicloud_rds_db_instance_endpoint_public_address"}
	not excepted("HCF-POL-003", rc.address)
}
