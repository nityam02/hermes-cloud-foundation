output "oidc_provider_arn" {
  description = "ARN of the RAM OIDC provider used by the roles."
  value       = local.oidc_arn
}

output "plan_role_arn" {
  description = "Plan role ARN (repository variable HERMES_ALIBABA_PLAN_ROLE_ARN)."
  value       = alicloud_ram_role.plan.arn
}

output "apply_role_arns" {
  description = "Environment -> apply role ARN (GitHub environment variable HERMES_ALIBABA_APPLY_ROLE_ARN)."
  value       = { for e, r in alicloud_ram_role.apply : e => r.arn }
}

output "trusted_subjects" {
  description = "Exact OIDC subjects each role trusts (for review)."
  value = merge(
    { plan = local.plan_subjects },
    { for e in var.environments : "apply-${e}" => ["${local.subject}:environment:${e}"] },
  )
}
