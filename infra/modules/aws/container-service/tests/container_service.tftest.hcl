mock_provider "aws" {
  mock_resource "aws_lb" {
    defaults = {
      arn        = "arn:aws:elasticloadbalancing:eu-north-1:123456789012:loadbalancer/app/shop-dev-web/0123456789abcdef"
      arn_suffix = "app/shop-dev-web/0123456789abcdef"
      dns_name   = "shop-dev-web-123456789.eu-north-1.elb.amazonaws.com"
    }
  }
  mock_resource "aws_lb_target_group" {
    defaults = {
      arn        = "arn:aws:elasticloadbalancing:eu-north-1:123456789012:targetgroup/shop-dev-api/0123456789abcdef"
      arn_suffix = "targetgroup/shop-dev-api/0123456789abcdef"
    }
  }
  mock_resource "aws_lb_listener" {
    defaults = {
      arn = "arn:aws:elasticloadbalancing:eu-north-1:123456789012:listener/app/shop-dev-web/0123456789abcdef/0123456789abcdef"
    }
  }
  mock_resource "aws_ecs_cluster" {
    defaults = {
      arn = "arn:aws:ecs:eu-north-1:123456789012:cluster/shop-dev"
    }
  }
  mock_resource "aws_ecs_task_definition" {
    defaults = {
      arn = "arn:aws:ecs:eu-north-1:123456789012:task-definition/shop-dev-api:1"
    }
  }
  mock_resource "aws_appautoscaling_policy" {
    defaults = {
      arn = "arn:aws:autoscaling:eu-north-1:123456789012:scalingPolicy:00000000-0000-4000-8000-000000000000:resource/ecs/service/shop-dev/shop-dev-worker:policyName/mock"
    }
  }
}

variables {
  cluster_name       = "shop-dev"
  container_insights = true
  vpc_id             = "vpc-0123456789abcdef0"
  private_subnet_ids = ["subnet-0123456789abcdef0", "subnet-0123456789abcdef1"]
  public_subnet_ids  = ["subnet-0123456789abcdef2", "subnet-0123456789abcdef3"]
  account_id         = "123456789012"
  region             = "eu-north-1"
  tracing            = true
  metrics            = true
  metrics_namespace  = "Hermes/shop/dev"
  alarms_enabled     = true
  alarm_topic_arn    = "arn:aws:sns:eu-north-1:123456789012:shop-dev-alerts"
  kms_enabled        = true
  kms_key_arn        = "arn:aws:kms:eu-north-1:123456789012:key/00000000-0000-4000-8000-000000000000"

  database = {
    security_group_id = "sg-0000000000000000d"
    iam_connect_arn   = "arn:aws:rds-db:eu-north-1:123456789012:dbuser:db-ABCDEFGHIJKLMNOPQRSTUVWXY/app"
  }

  alb = {
    name                = "shop-dev-web"
    security_group_name = "shop-dev-alb"
    certificate_arn     = ""
    ingress_cidrs       = ["0.0.0.0/0"]
  }

  public_service_order = ["api", "admin"]

  services = {
    api = {
      image             = "123456789012.dkr.ecr.eu-north-1.amazonaws.com/shop/api:1.0.0"
      port              = 8080
      cpu               = 0.5
      memory_mb         = 1024
      min_instances     = 1
      max_instances     = 3
      public            = true
      health_check_path = "/healthz"
      command           = []
      environment       = { HERMES_SERVICE = "api", LOG_LEVEL = "info" }
      secrets           = { MAPS_API_KEY = "arn:aws:secretsmanager:eu-north-1:123456789012:secret:shop-dev-maps-api-key-AbCdEf" }
      log_group_name    = "/hermes/shop/dev/services/api"
      log_group_arn     = "arn:aws:logs:eu-north-1:123456789012:log-group:/hermes/shop/dev/services/api"
      names             = { service = "shop-dev-api", family = "shop-dev-api", task_role = "shop-dev-api-task", execution_role = "shop-dev-api-exec", security_group = "shop-dev-api-tasks", target_group = "shop-dev-api" }
      access = {
        bucket_read_arns       = ["arn:aws:s3:::shop-dev-images-0a1b2c3d"]
        bucket_read_write_arns = ["arn:aws:s3:::shop-dev-artifacts-0a1b2c3d"]
        topic_arns             = ["arn:aws:sns:eu-north-1:123456789012:shop-dev-telemetry"]
        queue_arns             = []
        queue_names            = []
        database               = true
      }
    }
    admin = {
      image             = "ghcr.io/example/admin:1.0.0"
      port              = 9000
      cpu               = 0.25
      memory_mb         = 512
      min_instances     = 1
      max_instances     = 1
      public            = true
      health_check_path = "/ready"
      command           = ["/admin", "serve"]
      environment       = { HERMES_SERVICE = "admin" }
      secrets           = {}
      log_group_name    = "/hermes/shop/dev/services/admin"
      log_group_arn     = "arn:aws:logs:eu-north-1:123456789012:log-group:/hermes/shop/dev/services/admin"
      names             = { service = "shop-dev-admin", family = "shop-dev-admin", task_role = "shop-dev-admin-task", execution_role = "shop-dev-admin-exec", security_group = "shop-dev-admin-tasks", target_group = "shop-dev-admin" }
      access            = { bucket_read_arns = [], bucket_read_write_arns = [], topic_arns = [], queue_arns = [], queue_names = [], database = false }
    }
    worker = {
      image             = "ghcr.io/example/worker:1.0.0"
      port              = 8080
      cpu               = 0.25
      memory_mb         = 512
      min_instances     = 0
      max_instances     = 2
      public            = false
      health_check_path = "/healthz"
      command           = []
      environment       = { HERMES_SERVICE = "worker" }
      secrets           = {}
      log_group_name    = "/hermes/shop/dev/services/worker"
      log_group_arn     = "arn:aws:logs:eu-north-1:123456789012:log-group:/hermes/shop/dev/services/worker"
      names             = { service = "shop-dev-worker", family = "shop-dev-worker", task_role = "shop-dev-worker-task", execution_role = "shop-dev-worker-exec", security_group = "shop-dev-worker-tasks", target_group = "shop-dev-worker" }
      access = {
        bucket_read_arns       = []
        bucket_read_write_arns = []
        topic_arns             = []
        queue_arns             = ["arn:aws:sqs:eu-north-1:123456789012:shop-dev-telemetry-ingest"]
        queue_names            = ["shop-dev-telemetry-ingest"]
        database               = false
      }
    }
  }
}

run "least_privilege_and_wiring" {
  command = apply

  # --- task roles ---------------------------------------------------------------------------
  assert {
    condition = tolist(sort(flatten([for s in jsondecode(output.task_role_policies["api"]).Statement : s.Resource if contains(["ListGrantedBuckets"], s.Sid)]))) == tolist([
      "arn:aws:s3:::shop-dev-artifacts-0a1b2c3d", "arn:aws:s3:::shop-dev-images-0a1b2c3d",
    ])
    error_message = "api may list exactly its two granted buckets."
  }

  assert {
    condition     = tolist(flatten([for s in jsondecode(output.task_role_policies["api"]).Statement : s.Resource if s.Sid == "WriteGrantedObjects"])) == tolist(["arn:aws:s3:::shop-dev-artifacts-0a1b2c3d/*"])
    error_message = "api may write only to the read-write bucket (artifacts), never to images."
  }

  assert {
    condition     = tolist(flatten([for s in jsondecode(output.task_role_policies["api"]).Statement : s.Resource if s.Sid == "PublishGrantedTopics"])) == tolist(["arn:aws:sns:eu-north-1:123456789012:shop-dev-telemetry"])
    error_message = "api may publish only to telemetry."
  }

  assert {
    condition     = tolist([for s in jsondecode(output.task_role_policies["api"]).Statement : s.Resource if s.Sid == "ConnectAsAppDatabaseUser"]) == tolist(["arn:aws:rds-db:eu-north-1:123456789012:dbuser:db-ABCDEFGHIJKLMNOPQRSTUVWXY/app"])
    error_message = "api connects to the database only as dbuser app."
  }

  assert {
    condition     = tolist(sort([for s in jsondecode(output.task_role_policies["api"]).Statement : s.Condition.StringEquals["kms:ViaService"] if s.Sid == "UseEnvironmentKeyViaGrantedServices"][0])) == tolist(["s3.eu-north-1.amazonaws.com", "sns.eu-north-1.amazonaws.com"])
    error_message = "api may use the CMK only through S3 and SNS."
  }

  assert {
    condition     = length([for s in jsondecode(output.task_role_policies["worker"]).Statement : s if anytrue([for a in s.Action : startswith(a, "s3:") || startswith(a, "sns:") || startswith(a, "rds-db:")])]) == 0
    error_message = "worker must not get any S3, SNS or database permissions."
  }

  assert {
    condition     = tolist(flatten([for s in jsondecode(output.task_role_policies["worker"]).Statement : s.Resource if s.Sid == "ConsumeGrantedQueues"])) == tolist(["arn:aws:sqs:eu-north-1:123456789012:shop-dev-telemetry-ingest"])
    error_message = "worker may consume only telemetry-ingest."
  }

  assert {
    condition     = tolist(sort([for s in jsondecode(output.task_role_policies["admin"]).Statement : s.Sid])) == tolist(["CollectorEmbeddedMetrics", "PutCustomMetrics", "WriteTraces"])
    error_message = "A service without access grants only gets observability permissions."
  }

  assert {
    condition     = jsondecode(aws_iam_role.task["api"].assume_role_policy).Statement[0].Condition.StringEquals["aws:SourceAccount"] == "123456789012"
    error_message = "Task roles must be protected against the confused-deputy problem."
  }

  # --- execution roles ----------------------------------------------------------------------
  assert {
    condition     = tolist(flatten([for s in jsondecode(output.execution_role_policies["api"]).Statement : s.Resource if s.Sid == "ReadServiceSecrets"])) == tolist(["arn:aws:secretsmanager:eu-north-1:123456789012:secret:shop-dev-maps-api-key-AbCdEf"])
    error_message = "api's execution role reads only the secrets in its secret_env."
  }

  assert {
    condition     = tolist([for s in jsondecode(output.execution_role_policies["api"]).Statement : s.Resource if s.Sid == "PullServiceImage"]) == tolist(["arn:aws:ecr:eu-north-1:123456789012:repository/shop/api"])
    error_message = "ECR pull must be scoped to the service's own repository."
  }

  assert {
    condition     = length([for s in jsondecode(output.execution_role_policies["worker"]).Statement : s if contains(["ReadServiceSecrets", "DecryptServiceSecrets", "PullServiceImage"], s.Sid)]) == 0
    error_message = "worker has no secrets and a public image: no secret, KMS or ECR permissions."
  }

  # --- containers ---------------------------------------------------------------------------
  assert {
    condition     = tolist([for c in jsondecode(aws_ecs_task_definition.this["api"].container_definitions) : c.name]) == tolist(["app", "otel-collector"])
    error_message = "With tracing each task runs the app plus the ADOT collector sidecar."
  }

  assert {
    condition     = tolist([for c in jsondecode(aws_ecs_task_definition.this["api"].container_definitions) : c.secrets if c.name == "app"][0]) == tolist([{ name = "MAPS_API_KEY", valueFrom = "arn:aws:secretsmanager:eu-north-1:123456789012:secret:shop-dev-maps-api-key-AbCdEf" }])
    error_message = "Secrets are injected by reference (valueFrom), never as values."
  }

  assert {
    condition     = alltrue([for c in jsondecode(aws_ecs_task_definition.this["api"].container_definitions) : c.readonlyRootFilesystem]) && one(aws_ecs_task_definition.this["api"].volume).name == "tmp"
    error_message = "Containers run with a read-only root filesystem and a scratch /tmp volume."
  }

  assert {
    condition     = strcontains([for c in jsondecode(aws_ecs_task_definition.this["api"].container_definitions) : c.environment[0].value if c.name == "otel-collector"][0], "127.0.0.1:4317")
    error_message = "The collector must listen for OTLP on localhost only."
  }

  assert {
    condition     = aws_ecs_task_definition.this["api"].cpu == "512" && aws_ecs_task_definition.this["worker"].cpu == "256" && aws_ecs_task_definition.this["worker"].memory == "512"
    error_message = "CPU units must be cpu * 1024."
  }

  assert {
    condition     = tolist([for c in jsondecode(aws_ecs_task_definition.this["admin"].container_definitions) : c.command if c.name == "app"][0]) == tolist(["/admin", "serve"])
    error_message = "The spec command overrides the image entrypoint arguments."
  }

  # --- services, networking, ingress -------------------------------------------------------
  assert {
    condition     = alltrue([for s in aws_ecs_service.this : !one(s.network_configuration).assign_public_ip && one(s.deployment_circuit_breaker).rollback])
    error_message = "Tasks never get public IPs and failed deployments roll back."
  }

  assert {
    condition     = aws_ecs_service.this["worker"].desired_count == 0 && length(aws_ecs_service.this["worker"].load_balancer) == 0 && !contains(keys(aws_lb_target_group.this), "worker")
    error_message = "A private service has no load balancer; desired count starts at min_instances."
  }

  assert {
    condition     = tolist(keys(aws_vpc_security_group_ingress_rule.task_from_alb)) == tolist(["admin", "api"]) && aws_vpc_security_group_ingress_rule.task_from_alb["api"].referenced_security_group_id == aws_security_group.alb[0].id
    error_message = "Public tasks accept traffic only from the ALB security group; private tasks have no ingress."
  }

  assert {
    condition     = tolist(keys(aws_vpc_security_group_egress_rule.task_database)) == tolist(["api"])
    error_message = "Only services with access.database may reach PostgreSQL."
  }

  assert {
    condition     = tolist(keys(aws_vpc_security_group_ingress_rule.alb)) == tolist(["80-0.0.0.0/0"]) && one(aws_lb_listener.http[0].default_action).type == "forward" && length(aws_lb_listener.https) == 0
    error_message = "Without a certificate the ALB serves plain HTTP on 80 only."
  }

  assert {
    condition     = output.service_urls["api"] == "http://shop-dev-web-123456789.eu-north-1.elb.amazonaws.com" && output.service_urls["admin"] == "http://shop-dev-web-123456789.eu-north-1.elb.amazonaws.com/admin/" && output.service_urls["worker"] == ""
    error_message = "The first public service is the default route; others are path-routed; private services have no URL."
  }

  assert {
    condition     = tolist(keys(aws_lb_listener_rule.path)) == tolist(["admin"]) && one(one(aws_lb_listener_rule.path["admin"].condition).path_pattern).values == toset(["/admin", "/admin/*"])
    error_message = "Non-default public services are routed by path prefix."
  }

  assert {
    condition     = aws_lb.this[0].drop_invalid_header_fields && !aws_lb.this[0].enable_deletion_protection
    error_message = "The ALB drops invalid headers; deletion protection only when protected."
  }

  # --- scaling and alarms -------------------------------------------------------------------
  assert {
    condition     = tolist(keys(aws_appautoscaling_policy.backlog_out)) == tolist(["worker"]) && tolist(keys(aws_cloudwatch_metric_alarm.queue_idle)) == tolist(["worker"])
    error_message = "Queue consumers at min 0 scale on queue backlog (out from zero, back to zero when idle)."
  }

  assert {
    condition     = one(aws_appautoscaling_policy.cpu["worker"].target_tracking_scaling_policy_configuration).disable_scale_in && !one(aws_appautoscaling_policy.cpu["api"].target_tracking_scaling_policy_configuration).disable_scale_in
    error_message = "CPU target tracking scales in, except for queue-scaled consumers."
  }

  assert {
    condition     = aws_appautoscaling_target.this["api"].min_capacity == 1 && aws_appautoscaling_target.this["api"].max_capacity == 3
    error_message = "Autoscaling bounds follow min/max instances."
  }

  assert {
    condition     = length(aws_cloudwatch_metric_alarm.alb_5xx) == 1 && tolist(keys(aws_cloudwatch_metric_alarm.target_5xx)) == tolist(["admin", "api"]) && length(aws_cloudwatch_metric_alarm.service_cpu_high) == 3
    error_message = "ALB 5xx, per-target 5xx and per-service CPU alarms must exist."
  }

  assert {
    condition     = one(aws_ecs_cluster.this.setting).value == "enabled"
    error_message = "Container Insights follows observability.metrics."
  }
}

run "https_with_certificate_and_protected" {
  command = apply

  variables {
    protected = true
    alb = {
      name                = "shop-prod-web"
      security_group_name = "shop-prod-alb"
      certificate_arn     = "arn:aws:acm:eu-north-1:123456789012:certificate/00000000-0000-4000-8000-000000000000"
      ingress_cidrs       = ["203.0.113.0/24"]
    }
  }

  assert {
    condition     = aws_lb_listener.https[0].ssl_policy == "ELBSecurityPolicy-TLS13-1-2-2021-06" && aws_lb_listener.https[0].port == 443
    error_message = "HTTPS listener with a TLS 1.2+ policy."
  }

  assert {
    condition     = one(aws_lb_listener.http[0].default_action).type == "redirect" && one(one(aws_lb_listener.http[0].default_action).redirect).protocol == "HTTPS"
    error_message = "HTTP must redirect to HTTPS when a certificate is set."
  }

  assert {
    condition     = tolist(sort(keys(aws_vpc_security_group_ingress_rule.alb))) == tolist(["443-203.0.113.0/24", "80-203.0.113.0/24"])
    error_message = "ALB ingress follows alb_ingress_cidrs on 80 and 443."
  }

  assert {
    condition     = aws_lb.this[0].enable_deletion_protection && startswith(output.service_urls["api"], "https://")
    error_message = "Protected ALBs have deletion protection; URLs use https."
  }
}

run "protected_public_without_certificate_fails" {
  command = plan

  variables {
    protected = true
  }

  expect_failures = [aws_lb.this]
}

run "invalid_fargate_combination_fails" {
  command = plan

  variables {
    services = {
      bad = {
        image             = "ghcr.io/example/bad:1.0.0"
        port              = 8080
        cpu               = 0.25
        memory_mb         = 4096
        min_instances     = 1
        max_instances     = 1
        public            = false
        health_check_path = "/healthz"
        command           = []
        environment       = {}
        secrets           = {}
        log_group_name    = "/hermes/shop/dev/services/bad"
        log_group_arn     = "arn:aws:logs:eu-north-1:123456789012:log-group:/hermes/shop/dev/services/bad"
        names             = { service = "shop-dev-bad", family = "shop-dev-bad", task_role = "shop-dev-bad-task", execution_role = "shop-dev-bad-exec", security_group = "shop-dev-bad-tasks", target_group = "shop-dev-bad" }
        access            = { bucket_read_arns = [], bucket_read_write_arns = [], topic_arns = [], queue_arns = [], queue_names = [], database = false }
      }
    }
    public_service_order = []
  }

  expect_failures = [aws_ecs_task_definition.this]
}

run "no_tracing_no_metrics_minimal_identity" {
  command = apply

  variables {
    tracing        = false
    metrics        = false
    alarms_enabled = false
    kms_enabled    = false
    kms_key_arn    = null
  }

  assert {
    condition     = output.task_role_policies["admin"] == "" && !contains(keys(aws_iam_role_policy.task), "admin")
    error_message = "A service with no grants and no observability gets a task role without any policy."
  }

  assert {
    condition     = tolist([for c in jsondecode(aws_ecs_task_definition.this["api"].container_definitions) : c.name]) == tolist(["app"])
    error_message = "Without tracing there is no collector sidecar."
  }

  assert {
    condition     = length(aws_cloudwatch_metric_alarm.service_cpu_high) == 0 && length(aws_cloudwatch_metric_alarm.alb_5xx) == 0
    error_message = "No alarms when alarms are disabled."
  }
}

run "task_egress_defaults_to_https_anywhere" {
  command = apply

  assert {
    condition     = sort(keys(aws_vpc_security_group_egress_rule.task_https)) == sort(["admin", "api", "worker"]) && alltrue([for r in values(aws_vpc_security_group_egress_rule.task_https) : r.cidr_ipv4 == "0.0.0.0/0" && r.from_port == 443 && r.to_port == 443])
    error_message = "By default each service gets one TCP 443 egress rule to 0.0.0.0/0 (keyed by service name, unchanged from earlier versions)."
  }
}

run "task_egress_follows_task_egress_cidrs" {
  command = apply

  variables {
    task_egress_cidrs = ["10.0.0.0/16", "192.0.2.0/24"]
  }

  assert {
    condition     = length(aws_vpc_security_group_egress_rule.task_https) == 6 && sort(distinct([for r in values(aws_vpc_security_group_egress_rule.task_https) : r.cidr_ipv4])) == tolist(["10.0.0.0/16", "192.0.2.0/24"])
    error_message = "Each service gets a TCP 443 egress rule per task_egress_cidrs entry and nothing else."
  }

  assert {
    condition     = alltrue([for r in values(aws_vpc_security_group_egress_rule.task_https) : r.cidr_ipv4 != "0.0.0.0/0" && r.from_port == 443])
    error_message = "A custom task_egress_cidrs list replaces the 0.0.0.0/0 default."
  }

  assert {
    condition     = aws_vpc_security_group_egress_rule.task_https["api-10.0.0.0/16"].security_group_id == aws_security_group.task["api"].id
    error_message = "Egress rules attach to the service's own task security group."
  }
}

run "task_egress_rejects_invalid_cidr" {
  command = plan

  variables {
    task_egress_cidrs = ["not-a-cidr"]
  }

  expect_failures = [var.task_egress_cidrs]
}
