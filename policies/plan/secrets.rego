# HCF-POL-009: secret values and long-lived credentials are never managed by IaC
# (CLAUDE.md rule 2, ADR 0012). Anything below would put secret material into state or
# into the plan file, which many people and systems can read.
#   - secret versions/values: aws_secretsmanager_secret_version, google_secret_manager_secret_version,
#     aws_ssm_parameter of type SecureString, alicloud_kms_secret whose secret_data is not an
#     approved placeholder (data.hermes.secret_placeholders, default ["set-out-of-band"]);
#   - generated secrets: random_password, tls_private_key;
#   - long-lived keys and passwords: aws_iam_access_key, aws_iam_user_login_profile,
#     google_service_account_key, alicloud_ram_access_key, alicloud_ram_login_profile;
#   - database passwords as plain arguments (use provider-managed master secrets, IAM
#     authentication or write-only *_wo arguments instead).
# Write-only arguments (*_wo) are not flagged: their values never reach state or the plan.
# Messages never include the value.
package main

secret_value_types := {
	"aws_secretsmanager_secret_version",
	"google_secret_manager_secret_version",
	"random_password",
	"tls_private_key",
	"aws_iam_access_key",
	"aws_iam_user_login_profile",
	"google_service_account_key",
	"alicloud_ram_access_key",
	"alicloud_ram_login_profile",
}

deny contains message("HCF-POL-009", rc, sprintf("%s puts secret material into state; create and rotate it out-of-band", [rc.type])) if {
	some rc in planned_resources
	rc.type in secret_value_types
	not excepted("HCF-POL-009", rc.address)
}

deny contains message("HCF-POL-009", rc, "SSM SecureString parameter value is managed by IaC; create the parameter value out-of-band") if {
	some rc in planned_resources
	rc.type == "aws_ssm_parameter"
	rc.change.after.type == "SecureString"
	not excepted("HCF-POL-009", rc.address)
}

deny contains message("HCF-POL-009", rc, "secret_data is not an approved placeholder; the real value would be stored in state") if {
	some rc in planned_resources
	rc.type == "alicloud_kms_secret"
	is_string(rc.change.after.secret_data)
	not rc.change.after.secret_data in secret_placeholders
	not excepted("HCF-POL-009", rc.address)
}

# {type: [password attributes that hold a plaintext value in state]}
password_attributes := {
	"aws_db_instance": ["password"],
	"aws_rds_cluster": ["master_password"],
	"aws_docdb_cluster": ["master_password"],
	"aws_elasticache_user": ["passwords"],
	"google_sql_user": ["password"],
	"alicloud_db_account": ["account_password", "password"],
	"alicloud_rds_account": ["account_password", "password"],
	"alicloud_db_instance": ["tde_password", "ssl_password"],
}

deny contains message("HCF-POL-009", rc, sprintf("%s is set in configuration and stored in state; use a provider-managed secret, IAM authentication or the write-only %s_wo argument", [attr, attr])) if {
	some rc in planned_resources
	some attr in password_attributes[rc.type]
	value := rc.change.after[attr]
	value != null
	value != ""
	value != []
	not excepted("HCF-POL-009", rc.address)
}
