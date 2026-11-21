# Security groups and the shared public ALB.
# Routing: the first public service (spec order) is the default action; every other public
# service is matched by path prefix /<name> and /<name>/* (the prefix is passed through to the
# application unchanged). Service URL: <scheme>://<alb dns>[/<name>/].

locals {
  listener_ports = local.has_certificate ? [80, 443] : [80]
  alb_ingress = local.has_public ? {
    for pair in setproduct(local.listener_ports, var.alb.ingress_cidrs) : "${pair[0]}-${pair[1]}" => { port = pair[0], cidr = pair[1] }
  } : {}

  # One HTTPS egress rule per service and destination CIDR. The default destination keeps the
  # historical key (the service name) so upgrading does not replace existing rules.
  task_egress = {
    for pair in setproduct(keys(var.services), var.task_egress_cidrs) :
    (pair[1] == "0.0.0.0/0" ? pair[0] : "${pair[0]}-${pair[1]}") => { service = pair[0], cidr = pair[1] }
  }
}

resource "aws_security_group" "task" {
  for_each = var.services

  name        = each.value.names.security_group
  description = "Tasks of service ${each.key}"
  vpc_id      = var.vpc_id
  tags        = merge(var.tags, { Name = each.value.names.security_group })
}

# Tasks reach AWS APIs, image registries and external HTTPS endpoints over TCP 443 only, to
# var.task_egress_cidrs (default 0.0.0.0/0). Narrow it (VPC endpoints, an egress proxy) to
# reduce the exfiltration path; see docs/providers/aws.md.
resource "aws_vpc_security_group_egress_rule" "task_https" {
  #checkov:skip=CKV_AWS_382:Egress is limited to TCP 443 and to extensions.aws.task_egress_cidrs (default 0.0.0.0/0 because AWS APIs, registries and external APIs are not enumerable without VPC endpoints or a proxy).
  for_each = local.task_egress

  security_group_id = aws_security_group.task[each.value.service].id
  description       = "HTTPS egress"
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
  cidr_ipv4         = each.value.cidr
  tags              = var.tags
}

resource "aws_vpc_security_group_egress_rule" "task_database" {
  for_each = local.db_services

  security_group_id            = aws_security_group.task[each.key].id
  description                  = "PostgreSQL"
  ip_protocol                  = "tcp"
  from_port                    = 5432
  to_port                      = 5432
  referenced_security_group_id = var.database.security_group_id
  tags                         = var.tags
}

resource "aws_vpc_security_group_ingress_rule" "task_from_alb" {
  for_each = local.public_services

  security_group_id            = aws_security_group.task[each.key].id
  description                  = "Traffic from the shared ALB only"
  ip_protocol                  = "tcp"
  from_port                    = each.value.port
  to_port                      = each.value.port
  referenced_security_group_id = aws_security_group.alb[0].id
  tags                         = var.tags
}

resource "aws_security_group" "alb" {
  count = local.has_public ? 1 : 0

  name        = var.alb.security_group_name
  description = "Shared public ALB"
  vpc_id      = var.vpc_id
  tags        = merge(var.tags, { Name = var.alb.security_group_name })
}

resource "aws_vpc_security_group_ingress_rule" "alb" {
  #checkov:skip=CKV_AWS_260:Public services are internet-facing by spec (public: true); port 80 only redirects to HTTPS when a certificate is configured. Restrict with extensions.aws.alb_ingress_cidrs.
  for_each = local.alb_ingress

  security_group_id = aws_security_group.alb[0].id
  description       = "Public ingress on ${each.value.port}"
  ip_protocol       = "tcp"
  from_port         = each.value.port
  to_port           = each.value.port
  cidr_ipv4         = each.value.cidr
  tags              = var.tags
}

resource "aws_vpc_security_group_egress_rule" "alb_to_tasks" {
  for_each = local.public_services

  security_group_id            = aws_security_group.alb[0].id
  description                  = "To tasks of service ${each.key}"
  ip_protocol                  = "tcp"
  from_port                    = each.value.port
  to_port                      = each.value.port
  referenced_security_group_id = aws_security_group.task[each.key].id
  tags                         = var.tags
}

resource "aws_lb" "this" {
  #checkov:skip=CKV_AWS_91:ALB access logs need a dedicated SSE-S3 log bucket; designed, not implemented in v1alpha1 (documented).
  #checkov:skip=CKV2_AWS_28:WAF is not part of the v1alpha1 spec; attach a web ACL out-of-band if required.
  #checkov:skip=CKV_AWS_150:Deletion protection follows var.protected.
  count = local.has_public ? 1 : 0

  name                       = var.alb.name
  internal                   = false
  load_balancer_type         = "application"
  security_groups            = [aws_security_group.alb[0].id]
  subnets                    = var.public_subnet_ids
  drop_invalid_header_fields = true
  enable_deletion_protection = var.protected
  tags                       = var.tags

  lifecycle {
    precondition {
      condition     = !(var.protected && !local.has_certificate)
      error_message = "Public services in a protected environment require HTTPS: set extensions.aws.acm_certificate_arn to an ACM certificate ARN in this region."
    }
    precondition {
      condition     = sort(var.public_service_order) == sort(keys(local.public_services))
      error_message = "public_service_order must list exactly the public services."
    }
  }
}

resource "aws_lb_target_group" "this" {
  #checkov:skip=CKV_AWS_378:The ALB terminates TLS (HTTPS listener with the ACM certificate); ALB-to-task traffic stays inside the VPC on private subnets and is restricted by the task security group to the ALB only.
  for_each = local.public_services

  name                 = each.value.names.target_group
  port                 = each.value.port
  protocol             = "HTTP"
  target_type          = "ip"
  vpc_id               = var.vpc_id
  deregistration_delay = 30
  tags                 = var.tags

  health_check {
    enabled             = true
    path                = each.value.health_check_path
    port                = "traffic-port"
    protocol            = "HTTP"
    matcher             = "200-399"
    interval            = 15
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }
}

resource "aws_lb_listener" "https" {
  count = local.has_public && local.has_certificate ? 1 : 0

  load_balancer_arn = aws_lb.this[0].arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = var.alb.certificate_arn
  tags              = var.tags

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.this[local.default_public].arn
  }
}

# With a certificate: permanent redirect to HTTPS. Without one (non-protected only): plain HTTP.
resource "aws_lb_listener" "http" {
  #checkov:skip=CKV_AWS_2:HTTP serves traffic only when no certificate is configured, which the module rejects for protected environments.
  #checkov:skip=CKV_AWS_103:TLS policy applies to the HTTPS listener (ELBSecurityPolicy-TLS13-1-2-2021-06).
  #checkov:skip=CKV2_AWS_20:Redirects to HTTPS whenever a certificate is configured.
  count = local.has_public ? 1 : 0

  load_balancer_arn = aws_lb.this[0].arn
  port              = 80
  protocol          = "HTTP"
  tags              = var.tags

  default_action {
    type             = local.has_certificate ? "redirect" : "forward"
    target_group_arn = local.has_certificate ? null : aws_lb_target_group.this[local.default_public].arn

    dynamic "redirect" {
      for_each = local.has_certificate ? ["https"] : []
      content {
        port        = "443"
        protocol    = "HTTPS"
        status_code = "HTTP_301"
      }
    }
  }
}

resource "aws_lb_listener_rule" "path" {
  for_each = toset(local.path_routed)

  listener_arn = local.has_certificate ? aws_lb_listener.https[0].arn : aws_lb_listener.http[0].arn
  priority     = 100 + index(local.path_routed, each.key)
  tags         = var.tags

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.this[each.key].arn
  }

  condition {
    path_pattern {
      values = ["/${each.key}", "/${each.key}/*"]
    }
  }
}
