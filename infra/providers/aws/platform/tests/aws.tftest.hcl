# AWS-specific composition tests (mock providers only; they prove wiring, not real-cloud behaviour).

mock_provider "aws" {
  mock_data "aws_caller_identity" {
    defaults = {
      account_id = "123456789012"
      arn        = "arn:aws:iam::123456789012:user/hermes-mock"
      user_id    = "AIDAMOCKMOCKMOCKMOCK"
    }
  }
  mock_data "aws_partition" {
    defaults = {
      partition  = "aws"
      dns_suffix = "amazonaws.com"
    }
  }
  mock_data "aws_region" {
    defaults = {
      region = "eu-north-1"
    }
  }
  mock_data "aws_availability_zones" {
    defaults = {
      names    = ["eu-north-1a", "eu-north-1b", "eu-north-1c"]
      zone_ids = ["eun1-az1", "eun1-az2", "eun1-az3"]
    }
  }
  mock_resource "aws_kms_key" {
    defaults = {
      arn    = "arn:aws:kms:eu-north-1:123456789012:key/00000000-0000-4000-8000-000000000000"
      key_id = "00000000-0000-4000-8000-000000000000"
    }
  }
  mock_resource "aws_db_instance" {
    defaults = {
      address     = "mock-db.abcdefghijkl.eu-north-1.rds.amazonaws.com"
      resource_id = "db-ABCDEFGHIJKLMNOPQRSTUVWXY"
      master_user_secret = [{
        secret_arn    = "arn:aws:secretsmanager:eu-north-1:123456789012:secret:rds!db-00000000-0000-4000-8000-000000000000-AbCdEf"
        kms_key_id    = "arn:aws:kms:eu-north-1:123456789012:key/00000000-0000-4000-8000-000000000000"
        secret_status = "active"
      }]
    }
  }
  mock_resource "aws_lb" {
    defaults = {
      arn        = "arn:aws:elasticloadbalancing:eu-north-1:123456789012:loadbalancer/app/mock-web/0123456789abcdef"
      arn_suffix = "app/mock-web/0123456789abcdef"
      dns_name   = "mock-web-123456789.eu-north-1.elb.amazonaws.com"
    }
  }
  mock_resource "aws_lb_target_group" {
    defaults = {
      arn        = "arn:aws:elasticloadbalancing:eu-north-1:123456789012:targetgroup/mock/0123456789abcdef"
      arn_suffix = "targetgroup/mock/0123456789abcdef"
    }
  }
  mock_resource "aws_lb_listener" {
    defaults = {
      arn = "arn:aws:elasticloadbalancing:eu-north-1:123456789012:listener/app/mock-web/0123456789abcdef/0123456789abcdef"
    }
  }
  mock_resource "aws_ecs_cluster" {
    defaults = {
      arn = "arn:aws:ecs:eu-north-1:123456789012:cluster/mock"
    }
  }
  mock_resource "aws_ecs_task_definition" {
    defaults = {
      arn = "arn:aws:ecs:eu-north-1:123456789012:task-definition/mock:1"
    }
  }
  mock_resource "aws_appautoscaling_policy" {
    defaults = {
      arn = "arn:aws:autoscaling:eu-north-1:123456789012:scalingPolicy:00000000-0000-4000-8000-000000000000:resource/ecs/service/mock/mock:policyName/mock"
    }
  }
}

variables {
  region     = "eu-north-1"
  extensions = {}

  project     = "contract-full"
  environment = "staging"
  protected   = false
  labels      = { cost-center = "platform" }
  network     = { cidr = "10.40.0.0/16", zones = 2, nat = true }
  security    = { customer_managed_keys = true }

  compute = {
    type = "containerized"
    services = [
      {
        name          = "api"
        image         = "ghcr.io/example/api:1.0.0"
        public        = true
        cpu           = 0.5
        memory_mb     = 1024
        env           = { LOG_LEVEL = "info" }
        secret_env    = { MAPS_API_KEY = "maps-api-key" }
        min_instances = 1
        max_instances = 3
        access = {
          buckets  = { artifacts = "read-write", images = "read" }
          publish  = ["telemetry"]
          database = true
        }
      },
      {
        name          = "worker"
        image         = "ghcr.io/example/worker:1.0.0"
        cpu           = 0.25
        memory_mb     = 512
        min_instances = 0
        max_instances = 2
        access        = { consume = ["telemetry-ingest"] }
      },
    ]
  }

  database = { enabled = true, high_availability = true, deletion_protection = false, extensions = ["postgis"] }
  storage = {
    enabled = true
    buckets = [
      { name = "artifacts", versioning = true, retention_days = 0 },
      { name = "images", versioning = false, retention_days = 30 },
    ]
  }
  messaging = {
    enabled = true
    topics = [{
      name            = "telemetry"
      retention_hours = 168
      subscriptions = [
        { name = "telemetry-ingest", ack_deadline_seconds = 30, max_delivery_attempts = 5 },
        { name = "telemetry-archive", ack_deadline_seconds = 60, max_delivery_attempts = 0 },
      ]
    }]
  }
  secrets       = { enabled = true, names = ["maps-api-key", "webhook-signing-key"] }
  observability = { enabled = true, log_retention_days = 30, metrics = true, tracing = true, alert_email = "oncall@example.com" }
}

run "least_privilege_for_contract_fixture" {
  command = apply

  assert {
    condition = [for st in jsondecode(module.kms[0].policy).Statement : st.Condition.ArnLike["kms:EncryptionContext:aws:logs:arn"] if st.Sid == "AllowCloudWatchLogsInThisAccount"][0] == [
      "arn:aws:logs:eu-north-1:123456789012:log-group:/hermes/contract-full/staging/*",
      "arn:aws:logs:eu-north-1:123456789012:log-group:/aws/rds/instance/${module.naming.names["db"]}/*",
    ]
    error_message = "The KMS Logs statement covers only this environment's log groups and its RDS log exports."
  }

  assert {
    condition = alltrue([
      for n in concat([for g in values(module.observability.service_log_groups) : g.name], [module.network.flow_log_group_name]) :
      startswith(n, "/hermes/contract-full/staging/")
    ])
    error_message = "Every log group the composition encrypts must match the KMS key policy's log-group pattern."
  }

  assert {
    condition     = [for st in jsondecode(module.kms[0].policy).Statement : st.Condition.ArnLike["aws:SourceArn"] if st.Sid == "AllowSnsToEncryptedQueuesInThisAccount"][0] == ["arn:aws:sns:eu-north-1:123456789012:${module.naming.names["topic:telemetry"]}"]
    error_message = "SNS may use the key only for this environment's topics."
  }

  assert {
    condition     = length([for s in jsondecode(module.container_service[0].task_role_policies["worker"]).Statement : s if anytrue([for a in s.Action : startswith(a, "s3:") || startswith(a, "sns:") || startswith(a, "rds-db:")])]) == 0
    error_message = "worker must not be able to read buckets, publish or connect to the database."
  }

  assert {
    condition     = tolist(flatten([for s in jsondecode(module.container_service[0].task_role_policies["worker"]).Statement : s.Resource if s.Sid == "ConsumeGrantedQueues"])) == tolist([module.messaging.subscriptions["telemetry-ingest"].arn])
    error_message = "worker may consume only telemetry-ingest (not telemetry-archive)."
  }

  assert {
    condition     = tolist(flatten([for s in jsondecode(module.container_service[0].task_role_policies["api"]).Statement : s.Resource if s.Sid == "PublishGrantedTopics"])) == tolist([module.messaging.topics["telemetry"].arn])
    error_message = "api may publish only to telemetry."
  }

  assert {
    condition     = tolist(flatten([for s in jsondecode(module.container_service[0].task_role_policies["api"]).Statement : s.Resource if s.Sid == "WriteGrantedObjects"])) == tolist(["${module.object_storage.buckets["artifacts"].arn}/*"])
    error_message = "api may write only to artifacts (images is read-only)."
  }

  assert {
    condition     = tolist(flatten([for s in jsondecode(module.container_service[0].execution_role_policies["api"]).Statement : s.Resource if s.Sid == "ReadServiceSecrets"])) == tolist([module.secrets.arns["maps-api-key"]])
    error_message = "api's execution role may read only maps-api-key (not webhook-signing-key)."
  }

  assert {
    condition     = length([for s in jsondecode(module.container_service[0].execution_role_policies["worker"]).Statement : s if s.Sid == "ReadServiceSecrets"]) == 0
    error_message = "worker has no secret_env and must not read any secret."
  }

  assert {
    condition     = tolist(module.postgres[0].allowed_services) == tolist(["api"])
    error_message = "Only services with access.database may reach PostgreSQL."
  }

  assert {
    condition     = output.service_environment["api"]["HERMES_DB_AUTH"] == "iam" && output.service_environment["api"]["HERMES_DB_USER"] == "app" && output.service_environment["api"]["HERMES_DB_PORT"] == "5432"
    error_message = "Database bindings use IAM authentication as user app."
  }

  assert {
    condition     = output.service_environment["api"]["OTEL_EXPORTER_OTLP_ENDPOINT"] == "http://localhost:4317" && output.service_environment["api"]["OTEL_EXPORTER_OTLP_PROTOCOL"] == "grpc" && output.bindings.observability.otlp_endpoint == "http://localhost:4317"
    error_message = "With tracing the OTLP endpoint is the localhost collector sidecar."
  }

  assert {
    condition     = output.service_environment["worker"]["HERMES_SUBSCRIPTION_TELEMETRY_INGEST"] == output.bindings.topics["telemetry"].subscriptions["telemetry-ingest"] && output.service_environment["api"]["HERMES_BUCKET_ARTIFACTS"] == output.bindings.buckets["artifacts"].name
    error_message = "Injected binding env vars must equal the bindings output."
  }

  assert {
    condition     = output.secret_environment["api"]["MAPS_API_KEY"] == output.bindings.secrets["maps-api-key"]
    error_message = "secret_environment carries the secret ARN reference only."
  }

  assert {
    condition     = startswith(output.bindings.services["api"].url, "http://") && output.bindings.services["worker"].url == "" && startswith(output.bindings.services["worker"].identity, "arn:aws:iam::123456789012:role/")
    error_message = "Public service URL via the ALB; private services have no URL; identity is the task role ARN."
  }

  assert {
    condition     = output.bindings.database.credentials_ref != "" && output.bindings.database.instance_id == "contract-full-staging-db" && output.bindings.database.name == "app"
    error_message = "credentials_ref is the RDS-managed master secret; instance_id is the native identifier."
  }

  assert {
    condition     = output.bindings.observability.alert_channel == "arn:aws:sns:eu-north-1:123456789012:contract-full-staging-hermes-alerts" && output.bindings.observability.log_destination == "/hermes/contract-full/staging"
    error_message = "Alert channel is the alert topic; logs live under /hermes/<project>/<environment>."
  }

  assert {
    condition     = module.network.nat_gateway_count == 1
    error_message = "Non-protected environments use a single NAT gateway."
  }
}

run "protected_long_names_https" {
  command = apply

  variables {
    project     = "sample-logistics-platform"
    environment = "production"
    protected   = true
    network     = { cidr = "10.40.0.0/16", zones = 3, nat = true }
    extensions = {
      acm_certificate_arn = "arn:aws:acm:eu-north-1:123456789012:certificate/00000000-0000-4000-8000-000000000000"
      alb_ingress_cidrs   = ["0.0.0.0/0"]
      task_egress_cidrs   = ["10.40.0.0/16", "198.51.100.0/24"]
      cpu_architecture    = "ARM64"
    }
    compute = {
      type = "containerized"
      services = [{
        name   = "abcdefghijklmnopqrst"
        image  = "ghcr.io/example/api:1.0.0"
        public = true
        access = { buckets = { abcdefghijklmnopqrsu = "read" }, consume = ["abcdefghijklmnopqrsv"] }
      }]
    }
    storage   = { enabled = true, buckets = [{ name = "abcdefghijklmnopqrsu" }] }
    messaging = { enabled = true, topics = [{ name = "abcdefghijklmnopqrsw", subscriptions = [{ name = "abcdefghijklmnopqrsv" }] }] }
    secrets   = { enabled = false }
  }

  assert {
    condition = (length(module.naming.names["alb"]) <= 32
      && length(module.naming.names["tg:abcdefghijklmnopqrst"]) <= 32
      && length(module.naming.names["task_role:abcdefghijklmnopqrst"]) <= 64
      && length(module.naming.names["exec_role:abcdefghijklmnopqrst"]) <= 64
      && length(module.naming.names["db"]) <= 63
      && length(module.naming.names["queue:abcdefghijklmnopqrsv"]) <= 76
      && length(module.naming.names["flow_logs_role"]) <= 64
    && length(module.naming.names["rds_monitoring"]) <= 64)
    error_message = "Every native name must fit its AWS limit."
  }

  assert {
    condition     = length(output.bindings.buckets["abcdefghijklmnopqrsu"].name) <= 63
    error_message = "Bucket names (with random suffix) must be at most 63 characters."
  }

  assert {
    condition     = module.container_service[0].https_enabled && startswith(output.bindings.services["abcdefghijklmnopqrst"].url, "https://")
    error_message = "With a certificate public services are served over HTTPS."
  }

  assert {
    condition     = module.network.nat_gateway_count == 3
    error_message = "Protected environments get one NAT gateway per zone."
  }

  assert {
    condition     = module.container_service[0].task_egress_cidrs == tolist(["10.40.0.0/16", "198.51.100.0/24"])
    error_message = "extensions.aws.task_egress_cidrs must replace the 0.0.0.0/0 task egress default."
  }
}

# F-14 regression: without customer-managed keys (the default outside protected environments) the
# alert topic must still be encrypted (HCF-POL-002) with a dedicated key CloudWatch can use.
run "default_keys_alert_topic_encrypted" {
  command = apply

  variables {
    security = { customer_managed_keys = false }
  }

  assert {
    condition     = length(module.kms) == 0 && length(module.observability.alert_key_policy) > 0
    error_message = "Without the environment CMK the observability module creates a dedicated alert key."
  }

  assert {
    condition     = module.observability.alert_key_arn != null && module.observability.alert_key_arn != "alias/aws/sns"
    error_message = "The alert topic must be encrypted with a customer-managed key CloudWatch can use."
  }

  assert {
    condition     = jsondecode(module.observability.alert_key_policy).Statement[1].Condition.ArnLike["aws:SourceArn"] == "arn:aws:cloudwatch:eu-north-1:123456789012:alarm:*"
    error_message = "Only CloudWatch alarms of this account and region may use the alert key."
  }
}

run "rejects_unknown_extension_key" {
  command = plan

  variables {
    extensions = { acm_cert_arn = "typo" }
  }

  expect_failures = [output.bindings]
}

run "rejects_dangling_bucket_reference" {
  command = plan

  variables {
    storage = { enabled = false }
  }

  expect_failures = [output.bindings]
}

run "rejects_region_mismatch" {
  command = plan

  variables {
    region = "us-east-1"
  }

  expect_failures = [data.aws_region.current]
}
