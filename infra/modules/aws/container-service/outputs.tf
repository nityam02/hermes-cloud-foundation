output "cluster_name" {
  description = "ECS cluster name."
  value       = aws_ecs_cluster.this.name
}

output "cluster_arn" {
  description = "ECS cluster ARN."
  value       = aws_ecs_cluster.this.arn
}

output "service_urls" {
  description = "Service name -> public URL (\"\" for private services; no internal service discovery in v1alpha1)."
  value = {
    for k, s in var.services : k => (
      !s.public ? "" : (k == local.default_public ? "${local.scheme}://${aws_lb.this[0].dns_name}" : "${local.scheme}://${aws_lb.this[0].dns_name}/${k}/")
    )
  }
}

output "task_role_arns" {
  description = "Service name -> task (application) role ARN."
  value       = { for k, r in aws_iam_role.task : k => local.task_role_arns[k] }
}

output "execution_role_arns" {
  description = "Service name -> execution role ARN."
  value       = { for k, r in aws_iam_role.execution : k => local.execution_role_arns[k] }
}

output "task_security_group_ids" {
  description = "Service name -> task security group id."
  value       = { for k, g in aws_security_group.task : k => g.id }
}

output "task_role_policies" {
  description = "Service name -> task role inline policy JSON (\"\" when the service needs no permissions)."
  value       = { for k in keys(var.services) : k => contains(local.task_policy_services, k) ? aws_iam_role_policy.task[k].policy : "" }
}

output "execution_role_policies" {
  description = "Service name -> execution role inline policy JSON."
  value       = { for k, p in aws_iam_role_policy.execution : k => p.policy }
}

output "alb_dns_name" {
  description = "Shared ALB DNS name (\"\" when no service is public)."
  value       = local.has_public ? aws_lb.this[0].dns_name : ""
}

output "https_enabled" {
  description = "True when the shared ALB terminates HTTPS."
  value       = local.has_public && local.has_certificate
}

output "task_egress_cidrs" {
  description = "Distinct destination CIDRs of the tasks' TCP 443 egress rules."
  value       = sort(distinct([for r in aws_vpc_security_group_egress_rule.task_https : r.cidr_ipv4]))
}
