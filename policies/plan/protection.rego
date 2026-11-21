# HCF-POL-006: databases in protected environments must have deletion protection.
# HCF-POL-011: databases in protected environments must keep backups and a final snapshot;
#              buckets in protected environments should keep versioning (warn).
#
# A resource is in a protected environment when any of these holds:
#   - data.hermes.protected == true (the CLI passes this for a protected environment);
#   - the rendered root passes protected = true to the composition module (read from the plan's
#     configuration section) or the root variable "protected" is true;
#   - its hermes-environment tag/label is in data.hermes.protected_environments
#     (default ["production"]).
package main

database_types := {"aws_db_instance", "aws_rds_cluster", "google_sql_database_instance", "alicloud_db_instance", "alicloud_polardb_cluster"}

deletion_protection_disabled(rc) if {
	attr_false(rc, "deletion_protection")
}

deletion_protection_disabled(rc) if {
	rc.type == "google_sql_database_instance"
	some s in rc.change.after.settings
	s.deletion_protection_enabled == false
}

deny contains message("HCF-POL-006", rc, "deletion protection is disabled for a database in a protected environment") if {
	some rc in planned_resources
	rc.type in database_types
	in_protected_environment(rc)
	deletion_protection_disabled(rc)
	not excepted("HCF-POL-006", rc.address)
}

warn contains message("HCF-POL-006", rc, "cannot determine the environment of this database (no hermes-environment tag/label and the plan is not marked protected); deletion protection was not evaluated") if {
	some rc in planned_resources
	rc.type in database_types
	not plan_protected
	not resource_environment(rc)
}

# ---------------------------------------------------------------- HCF-POL-011

deny contains message("HCF-POL-011", rc, "skip_final_snapshot = true for a database in a protected environment") if {
	some rc in planned_resources
	rc.type in {"aws_db_instance", "aws_rds_cluster"}
	in_protected_environment(rc)
	attr_true(rc, "skip_final_snapshot")
	not excepted("HCF-POL-011", rc.address)
}

deny contains message("HCF-POL-011", rc, "automated backups are disabled (backup_retention_period = 0) in a protected environment") if {
	some rc in planned_resources
	rc.type in {"aws_db_instance", "aws_rds_cluster"}
	in_protected_environment(rc)
	rc.change.after.backup_retention_period == 0
	not excepted("HCF-POL-011", rc.address)
}

deny contains message("HCF-POL-011", rc, sprintf("Cloud SQL backup_configuration.%s = false in a protected environment", [attr])) if {
	some rc in planned_resources
	rc.type == "google_sql_database_instance"
	in_protected_environment(rc)
	some s in rc.change.after.settings
	some b in s.backup_configuration
	some attr in ["enabled", "point_in_time_recovery_enabled"]
	b[attr] == false
	not excepted("HCF-POL-011", rc.address)
}

bucket_versioning_off(rc) if {
	rc.type == "aws_s3_bucket_versioning"
	some v in rc.change.after.versioning_configuration
	v.status in {"Suspended", "Disabled"}
}

bucket_versioning_off(rc) if {
	rc.type == "google_storage_bucket"
	some v in rc.change.after.versioning
	v.enabled == false
}

bucket_versioning_off(rc) if {
	rc.type == "alicloud_oss_bucket"
	some v in rc.change.after.versioning
	v.status == "Suspended"
}

warn contains message("HCF-POL-011", rc, "bucket versioning is off in a protected environment; deleted or overwritten objects cannot be restored") if {
	some rc in planned_resources
	bucket_versioning_off(rc)
	in_protected_environment(rc)
}
