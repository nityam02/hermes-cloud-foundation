output "oidc_provider_arn" {
  description = "ARN of the GitHub Actions OIDC provider used by the roles."
  value       = local.oidc_arn
}

output "plan_role_arn" {
  description = "Role for plan jobs (pull requests and the listed branches). Set as repository variable HERMES_AWS_PLAN_ROLE_ARN."
  value       = aws_iam_role.plan.arn
}

output "apply_role_arns" {
  description = "Environment name -> apply role ARN. Set each as GitHub environment variable HERMES_AWS_APPLY_ROLE_ARN."
  value       = { for e, r in aws_iam_role.apply : e => r.arn }
}

output "trusted_subjects" {
  description = "Exact OIDC subjects each role trusts (for review)."
  value = merge(
    { plan = local.plan_subjects },
    { for e, s in local.apply_subjects : "apply-${e}" => [s] },
  )
}
