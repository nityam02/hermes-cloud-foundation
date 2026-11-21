# ECS on Fargate: one cluster per environment; per service a task definition (app container plus
# an optional ADOT collector sidecar), an ECS service in private subnets without public IPs,
# and Application Auto Scaling (CPU target tracking; queue-backlog step scaling for queue
# consumers that scale to zero). Public services sit behind one shared ALB (alb.tf).

locals {
  public_services = { for k, s in var.services : k => s if s.public }
  has_public      = length(local.public_services) > 0
  has_certificate = var.alb.certificate_arn != ""
  scheme          = local.has_certificate ? "https" : "http"
  default_public  = length(var.public_service_order) > 0 ? var.public_service_order[0] : ""
  path_routed     = sort([for n in var.public_service_order : n if n != local.default_public])

  db_services = { for k, s in var.services : k => s if s.access.database && var.database != null }

  # Queue consumers at min 0: scaled out by queue backlog, scaled back to zero when idle.
  queue_scaled = { for k, s in var.services : k => s if s.min_instances == 0 && length(s.access.queue_names) > 0 }

  cpu_units = { for k, s in var.services : k => floor(s.cpu * 1024) }

  # Valid Fargate (Linux) CPU units -> memory (MiB) combinations for the spec's 0.25-4 vCPU range.
  fargate_memory = {
    "256"  = [512, 1024, 2048]
    "512"  = [for m in range(1, 5) : m * 1024]
    "1024" = [for m in range(2, 9) : m * 1024]
    "2048" = [for m in range(4, 17) : m * 1024]
    "4096" = [for m in range(8, 31) : m * 1024]
  }

  task_role_arns      = { for k, s in var.services : k => "arn:${var.partition}:iam::${var.account_id}:role/${s.names.task_role}" }
  execution_role_arns = { for k, s in var.services : k => "arn:${var.partition}:iam::${var.account_id}:role/${s.names.execution_role}" }

  # OpenTelemetry collector configuration (passed in AOT_CONFIG_CONTENT; not a secret).
  # OTLP is bound to 127.0.0.1: only containers of the same task can reach it.
  collector_config = {
    for k, s in var.services : k => yamlencode({
      extensions = { health_check = {} }
      receivers = {
        otlp = { protocols = { grpc = { endpoint = "127.0.0.1:4317" }, http = { endpoint = "127.0.0.1:4318" } } }
      }
      processors = { batch = {} }
      exporters = merge(
        { awsxray = {} },
        var.metrics ? { awsemf = { namespace = var.metrics_namespace, log_group_name = s.log_group_name, log_stream_name = "otel-metrics/{TaskId}" } } : {},
      )
      service = {
        extensions = ["health_check"]
        pipelines = merge(
          { traces = { receivers = ["otlp"], processors = ["batch"], exporters = ["awsxray"] } },
          var.metrics ? { metrics = { receivers = ["otlp"], processors = ["batch"], exporters = ["awsemf"] } } : {},
        )
      }
    })
  }

  container_definitions = {
    for k, s in var.services : k => concat(
      [merge(
        {
          name         = "app"
          image        = s.image
          essential    = true
          portMappings = [{ name = "http", containerPort = s.port, protocol = "tcp" }]
          environment  = [for n in sort(keys(s.environment)) : { name = n, value = s.environment[n] }]
          # Secrets are injected by ECS from Secrets Manager: only the ARN appears here.
          secrets                = [for n in sort(keys(s.secrets)) : { name = n, valueFrom = s.secrets[n] }]
          readonlyRootFilesystem = var.readonly_root_filesystem
          mountPoints            = var.readonly_root_filesystem ? [{ sourceVolume = "tmp", containerPath = "/tmp", readOnly = false }] : []
          linuxParameters        = { initProcessEnabled = true }
          dependsOn              = var.tracing ? [{ containerName = "otel-collector", condition = "START" }] : []
          logConfiguration = {
            logDriver = "awslogs"
            options   = { "awslogs-group" = s.log_group_name, "awslogs-region" = var.region, "awslogs-stream-prefix" = "app" }
          }
        },
        length(s.command) > 0 ? { command = s.command } : {},
        var.container_health_check ? {
          healthCheck = {
            command     = ["CMD-SHELL", "wget -q -O /dev/null http://127.0.0.1:${s.port}${s.health_check_path} || curl -fsS -o /dev/null http://127.0.0.1:${s.port}${s.health_check_path} || exit 1"]
            interval    = 30
            timeout     = 5
            retries     = 3
            startPeriod = 30
          }
        } : {},
      )],
      var.tracing ? [{
        name                   = "otel-collector"
        image                  = var.otel_collector_image
        essential              = false
        readonlyRootFilesystem = true
        memoryReservation      = 64
        environment            = [{ name = "AOT_CONFIG_CONTENT", value = local.collector_config[k] }]
        healthCheck            = { command = ["/healthcheck"], interval = 30, timeout = 5, retries = 3, startPeriod = 10 }
        logConfiguration = {
          logDriver = "awslogs"
          options   = { "awslogs-group" = s.log_group_name, "awslogs-region" = var.region, "awslogs-stream-prefix" = "otel" }
        }
      }] : [],
    )
  }
}

# Container Insights follows observability.metrics (it is billed per metric).
#trivy:ignore:AWS-0034
resource "aws_ecs_cluster" "this" {
  #checkov:skip=CKV_AWS_65:Container Insights follows the spec's observability.metrics (default true); turning it off is an explicit cost decision in the spec.
  name = var.cluster_name

  setting {
    name  = "containerInsights"
    value = var.container_insights ? "enabled" : "disabled"
  }

  tags = var.tags
}

resource "aws_ecs_task_definition" "this" {
  #checkov:skip=CKV_AWS_336:Read-only root filesystem is the default; extensions.aws.readonly_root_filesystem=false is an explicit opt-out.
  for_each = var.services

  family                   = each.value.names.family
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = tostring(local.cpu_units[each.key])
  memory                   = tostring(each.value.memory_mb)
  execution_role_arn       = local.execution_role_arns[each.key]
  task_role_arn            = local.task_role_arns[each.key]
  container_definitions    = jsonencode(local.container_definitions[each.key])

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = var.cpu_architecture
  }

  dynamic "volume" {
    for_each = var.readonly_root_filesystem ? ["tmp"] : []
    content {
      name = volume.value
    }
  }

  tags = var.tags

  lifecycle {
    precondition {
      condition     = contains(try(local.fargate_memory[tostring(local.cpu_units[each.key])], []), each.value.memory_mb)
      error_message = "Service ${each.key}: cpu ${each.value.cpu} with memory_mb ${each.value.memory_mb} is not a valid Fargate combination (0.25 vCPU: 512/1024/2048; 0.5: 1024-4096; 1: 2048-8192; 2: 4096-16384; 4: 8192-30720, in 1024 MiB steps)."
    }
  }

  depends_on = [aws_iam_role.execution, aws_iam_role.task]
}

resource "aws_ecs_service" "this" {
  for_each = var.services

  name                               = each.value.names.service
  cluster                            = aws_ecs_cluster.this.arn
  task_definition                    = aws_ecs_task_definition.this[each.key].arn
  desired_count                      = each.value.min_instances
  launch_type                        = "FARGATE"
  platform_version                   = "LATEST"
  propagate_tags                     = "SERVICE"
  enable_ecs_managed_tags            = true
  enable_execute_command             = false
  health_check_grace_period_seconds  = each.value.public ? 60 : null
  deployment_minimum_healthy_percent = 100
  deployment_maximum_percent         = 200

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [aws_security_group.task[each.key].id]
    assign_public_ip = false
  }

  dynamic "load_balancer" {
    for_each = each.value.public ? [each.value.port] : []
    content {
      target_group_arn = aws_lb_target_group.this[each.key].arn
      container_name   = "app"
      container_port   = load_balancer.value
    }
  }

  tags = var.tags

  # Application Auto Scaling owns the running count after creation.
  lifecycle {
    ignore_changes = [desired_count]
  }

  depends_on = [aws_lb_listener.http, aws_lb_listener.https, aws_lb_listener_rule.path, aws_iam_role_policy.execution, aws_iam_role_policy.task]
}

# --- Auto scaling ----------------------------------------------------------------------------

resource "aws_appautoscaling_target" "this" {
  for_each = var.services

  service_namespace  = "ecs"
  resource_id        = "service/${aws_ecs_cluster.this.name}/${aws_ecs_service.this[each.key].name}"
  scalable_dimension = "ecs:service:DesiredCount"
  min_capacity       = each.value.min_instances
  max_capacity       = each.value.max_instances
  tags               = var.tags
}

resource "aws_appautoscaling_policy" "cpu" {
  for_each = var.services

  name               = "${each.value.names.service}-cpu"
  policy_type        = "TargetTrackingScaling"
  service_namespace  = aws_appautoscaling_target.this[each.key].service_namespace
  resource_id        = aws_appautoscaling_target.this[each.key].resource_id
  scalable_dimension = aws_appautoscaling_target.this[each.key].scalable_dimension

  target_tracking_scaling_policy_configuration {
    target_value       = 60
    scale_in_cooldown  = 300
    scale_out_cooldown = 60
    # Queue-scaled consumers are scaled in by the idle-queue policy instead.
    disable_scale_in = contains(keys(local.queue_scaled), each.key)

    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
  }
}

resource "aws_appautoscaling_policy" "backlog_out" {
  for_each = local.queue_scaled

  name               = "${each.value.names.service}-queue-backlog"
  policy_type        = "StepScaling"
  service_namespace  = aws_appautoscaling_target.this[each.key].service_namespace
  resource_id        = aws_appautoscaling_target.this[each.key].resource_id
  scalable_dimension = aws_appautoscaling_target.this[each.key].scalable_dimension

  step_scaling_policy_configuration {
    adjustment_type         = "ChangeInCapacity"
    cooldown                = 60
    metric_aggregation_type = "Maximum"

    step_adjustment {
      metric_interval_lower_bound = 0
      scaling_adjustment          = 1
    }
  }
}

resource "aws_appautoscaling_policy" "idle_in" {
  for_each = local.queue_scaled

  name               = "${each.value.names.service}-queue-idle"
  policy_type        = "StepScaling"
  service_namespace  = aws_appautoscaling_target.this[each.key].service_namespace
  resource_id        = aws_appautoscaling_target.this[each.key].resource_id
  scalable_dimension = aws_appautoscaling_target.this[each.key].scalable_dimension

  step_scaling_policy_configuration {
    adjustment_type         = "ExactCapacity"
    cooldown                = 300
    metric_aggregation_type = "Maximum"

    step_adjustment {
      metric_interval_upper_bound = 0
      scaling_adjustment          = 0
    }
  }
}

resource "aws_cloudwatch_metric_alarm" "queue_backlog" {
  for_each = local.queue_scaled

  alarm_name          = "${each.value.names.service}-queue-backlog"
  alarm_description   = "Scale ${each.key} out from zero when its queues have visible messages."
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  threshold           = 1
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_appautoscaling_policy.backlog_out[each.key].arn]
  tags                = var.tags

  dynamic "metric_query" {
    for_each = { for i, q in each.value.access.queue_names : "v${i}" => q }
    content {
      id          = metric_query.key
      return_data = false
      metric {
        namespace   = "AWS/SQS"
        metric_name = "ApproximateNumberOfMessagesVisible"
        dimensions  = { QueueName = metric_query.value }
        period      = 60
        stat        = "Maximum"
      }
    }
  }

  metric_query {
    id          = "backlog"
    expression  = "SUM([${join(",", [for i, q in each.value.access.queue_names : "v${i}"])}])"
    label       = "Visible messages"
    return_data = true
  }
}

resource "aws_cloudwatch_metric_alarm" "queue_idle" {
  for_each = local.queue_scaled

  alarm_name          = "${each.value.names.service}-queue-idle"
  alarm_description   = "Scale ${each.key} back to zero after 15 minutes without visible or in-flight messages."
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = 15
  threshold           = 1
  treat_missing_data  = "breaching"
  alarm_actions       = [aws_appautoscaling_policy.idle_in[each.key].arn]
  tags                = var.tags

  dynamic "metric_query" {
    for_each = merge(
      { for i, q in each.value.access.queue_names : "v${i}" => { queue = q, metric = "ApproximateNumberOfMessagesVisible" } },
      { for i, q in each.value.access.queue_names : "n${i}" => { queue = q, metric = "ApproximateNumberOfMessagesNotVisible" } },
    )
    content {
      id          = metric_query.key
      return_data = false
      metric {
        namespace   = "AWS/SQS"
        metric_name = metric_query.value.metric
        dimensions  = { QueueName = metric_query.value.queue }
        period      = 60
        stat        = "Maximum"
      }
    }
  }

  metric_query {
    id          = "work"
    expression  = "SUM([${join(",", flatten([for i, q in each.value.access.queue_names : ["v${i}", "n${i}"]]))}])"
    label       = "Visible plus in-flight messages"
    return_data = true
  }
}
