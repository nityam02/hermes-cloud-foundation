# Hermes AWS composition: implements infra/contract (variables.tf copied verbatim; outputs per
# infra/contract/outputs.md). The calling root module configures provider "aws" (region =
# var.region, allowed_account_ids); the region guard below rejects a mismatch.

data "aws_caller_identity" "current" {}

data "aws_partition" "current" {}

# Read only for its region-guard postcondition.
# tflint-ignore: terraform_unused_declarations
data "aws_region" "current" {
  lifecycle {
    postcondition {
      condition     = self.region == var.region
      error_message = "The aws provider is configured for region ${self.region} but the spec region is ${var.region}."
    }
  }
}

data "aws_availability_zones" "available" {
  #checkov:skip=CKV_AWS_394:Hermes is region-generic, so zone names cannot be allowlisted here. The composition uses the first network.zones zones by name; a new zone sorts after existing ones, and any zone change shows as subnet replacement, which apply refuses without --allow-destructive.
  state = "available"

  filter {
    name   = "opt-in-status"
    values = ["opt-in-not-required"]
  }
}

locals {
  account_id = data.aws_caller_identity.current.account_id
  partition  = data.aws_partition.current.partition
}

module "naming" {
  source = "../../../modules/aws/naming"

  project     = var.project
  environment = var.environment
  names       = local.name_requests
}

module "kms" {
  source = "../../../modules/aws/kms"
  count  = local.cmk ? 1 : 0

  alias_name              = "alias/hermes/${local.prefix}"
  description             = "Hermes ${local.prefix}: encryption at rest for logs, storage, messaging, secrets and the database"
  deletion_window_in_days = var.protected ? 30 : 7
  account_id              = local.account_id
  partition               = local.partition
  region                  = var.region
  # Only this environment's log groups (services, VPC flow logs) and the RDS log exports.
  log_group_patterns = concat(["${local.log_prefix}/*"], local.db_enabled ? ["/aws/rds/instance/${local.names["db"]}/*"] : [])
  sns_topic_arns     = [for k in keys(local.topics) : "arn:${local.partition}:sns:${var.region}:${local.account_id}:${local.names["topic:${k}"]}"]
  tags               = local.tags["secrets"]
}

module "network" {
  source = "../../../modules/aws/network"

  name                = local.names["network"]
  cidr                = var.network.cidr
  availability_zones  = data.aws_availability_zones.available.names
  zone_count          = var.network.zones
  nat                 = var.network.nat
  nat_per_az          = var.protected
  flow_logs_enabled   = local.obs.enabled
  flow_log_group_name = "${local.log_prefix}/vpc-flow-logs"
  flow_log_role_name  = local.names["flow_logs_role"]
  log_retention_days  = local.log_retention_days
  kms_key_arn         = local.kms_key_arn
  account_id          = local.account_id
  partition           = local.partition
  region              = var.region
  tags                = local.tags["network"]
}

module "observability" {
  source = "../../../modules/aws/observability"

  log_group_prefix   = local.log_prefix
  service_names      = keys(local.services)
  log_retention_days = local.log_retention_days
  kms_enabled        = local.cmk
  kms_key_arn        = local.kms_key_arn
  alerts_enabled     = local.obs.enabled
  alert_topic_name   = local.names["alerts"]
  # Without the environment CMK the alert topic gets its own key (HCF-POL-002).
  alert_key_deletion_window_in_days = var.protected ? 30 : 7
  alert_email                       = local.obs.alert_email
  account_id                        = local.account_id
  partition                         = local.partition
  region                            = var.region
  tags                              = local.tags["observability"]
}

module "object_storage" {
  source = "../../../modules/aws/object-storage"

  buckets = {
    for k, b in local.buckets : k => {
      base_name      = local.names["bucket:${k}"]
      versioning     = b.versioning
      retention_days = b.retention_days
    }
  }
  kms_enabled             = local.cmk
  kms_key_arn             = local.kms_key_arn
  noncurrent_version_days = var.protected ? 90 : 30
  partition               = local.partition
  tags                    = local.tags["storage"]
}

module "messaging" {
  source = "../../../modules/aws/messaging"

  topics = {
    for k, t in local.topics : k => {
      name = local.names["topic:${k}"]
      subscriptions = {
        for s in t.subscriptions : s.name => {
          queue_name            = local.names["queue:${s.name}"]
          ack_deadline_seconds  = s.ack_deadline_seconds
          max_delivery_attempts = s.max_delivery_attempts
          retention_seconds     = t.retention_hours * 3600
        }
      }
    }
  }
  kms_enabled     = local.cmk
  kms_key_arn     = local.kms_key_arn
  alarms_enabled  = local.obs.enabled
  alarm_topic_arn = module.observability.alert_topic_arn
  account_id      = local.account_id
  partition       = local.partition
  region          = var.region
  dns_suffix      = data.aws_partition.current.dns_suffix
  tags            = local.tags["messaging"]
}

module "secrets" {
  source = "../../../modules/aws/secrets"

  secrets                 = { for n in local.secret_names : n => local.names["secret:${n}"] }
  kms_key_arn             = local.kms_key_arn
  recovery_window_in_days = var.protected ? 30 : 7
  tags                    = local.tags["secrets"]
}

module "postgres" {
  source = "../../../modules/aws/postgres"
  count  = local.db_enabled ? 1 : 0

  identifier                 = local.names["db"]
  engine_version             = var.database.version
  size                       = var.database.size
  instance_class_override    = local.ext.db_instance_class
  storage_gb                 = var.database.storage_gb
  max_storage_gb             = var.database.max_storage_gb
  high_availability          = var.database.high_availability
  backup_retention_days      = var.database.backup_retention_days
  deletion_protection        = var.database.deletion_protection || var.protected
  protected                  = var.protected
  extensions                 = var.database.extensions
  kms_key_arn                = local.kms_key_arn
  vpc_id                     = module.network.vpc_id
  subnet_ids                 = module.network.private_subnet_ids
  security_group_name        = local.names["db_sg"]
  allowed_security_group_ids = { for k, s in local.services : k => module.container_service[0].task_security_group_ids[k] if s.access.database }
  log_retention_days         = local.log_retention_days
  enhanced_monitoring        = local.metrics
  monitoring_role_name       = local.names["rds_monitoring"]
  alarms_enabled             = local.obs.enabled
  alarm_topic_arn            = module.observability.alert_topic_arn
  account_id                 = local.account_id
  partition                  = local.partition
  region                     = var.region
  tags                       = local.tags["database"]
}

module "container_service" {
  source = "../../../modules/aws/container-service"
  count  = local.containerized ? 1 : 0

  cluster_name       = local.names["cluster"]
  container_insights = local.metrics
  vpc_id             = module.network.vpc_id
  private_subnet_ids = module.network.private_subnet_ids
  public_subnet_ids  = module.network.public_subnet_ids
  protected          = var.protected

  alb = {
    name                = local.names["alb"]
    security_group_name = local.names["alb_sg"]
    certificate_arn     = local.ext.acm_certificate_arn
    ingress_cidrs       = local.ext.alb_ingress_cidrs
  }
  public_service_order = local.public_service_order
  task_egress_cidrs    = local.ext.task_egress_cidrs

  services = {
    for k, s in local.services : k => {
      image             = s.image
      port              = s.port
      cpu               = s.cpu
      memory_mb         = s.memory_mb
      min_instances     = s.min_instances
      max_instances     = s.max_instances
      public            = s.public
      health_check_path = s.health_check_path
      command           = s.command
      environment       = local.service_environment[k]
      secrets           = local.secret_environment[k]
      log_group_name    = module.observability.service_log_groups[k].name
      log_group_arn     = module.observability.service_log_groups[k].arn
      names = {
        service        = local.names["svc:${k}"]
        family         = local.names["svc:${k}"]
        task_role      = local.names["task_role:${k}"]
        execution_role = local.names["exec_role:${k}"]
        security_group = local.names["task_sg:${k}"]
        target_group   = local.names["tg:${k}"]
      }
      access = local.service_access[k]
    }
  }

  database = local.db_enabled ? {
    security_group_id = module.postgres[0].security_group_id
    iam_connect_arn   = module.postgres[0].iam_connect_arn
  } : null

  kms_enabled              = local.cmk
  kms_key_arn              = local.kms_key_arn
  tracing                  = local.tracing
  metrics                  = local.metrics
  metrics_namespace        = "Hermes/${var.project}/${var.environment}"
  cpu_architecture         = local.ext.cpu_architecture
  readonly_root_filesystem = local.ext.readonly_root_filesystem
  container_health_check   = local.ext.container_health_check
  alarms_enabled           = local.obs.enabled
  alarm_topic_arn          = module.observability.alert_topic_arn
  account_id               = local.account_id
  partition                = local.partition
  region                   = var.region
  tags                     = local.tags["compute"]
}

# --- warnings (check blocks never fail a plan) ----------------------------------------------------

check "private_egress" {
  assert {
    condition     = var.network.nat || length(local.services) == 0
    error_message = "network.nat is false: tasks in private subnets have no internet egress. Images must come from ECR and AWS APIs need VPC interface endpoints (not created by Hermes in v1alpha1); otherwise tasks will not start."
  }
}

check "public_scale_to_zero" {
  assert {
    condition     = alltrue([for s in values(local.services) : !(s.public && s.min_instances == 0)])
    error_message = "A public service has min_instances = 0. ECS on Fargate does not scale HTTP services up from zero: the service stays at zero tasks (the ALB answers 503) until min_instances is raised or it is scaled manually."
  }
}
