output "role_arn" {
  description = "Pass to `hermes inventory aws --role-arn`."
  value       = aws_iam_role.discovery.arn
}
