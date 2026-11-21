# Amazon RDS for PostgreSQL in private subnets.
# - Master credentials are generated and stored by RDS in Secrets Manager
#   (manage_master_user_password): no password ever passes through state.
# - Applications authenticate with IAM (rds_iam) as database user "app"; see
#   docs/providers/aws.md for the one-time SQL that creates it.
# - TLS is enforced with rds.force_ssl = 1.

locals {
  instance_classes = {
    small  = "db.t4g.small"
    medium = "db.m7g.large"
    large  = "db.m7g.xlarge"
    xlarge = "db.m7g.2xlarge"
  }
  instance_class = var.instance_class_override != "" ? var.instance_class_override : local.instance_classes[var.size]

  db_name   = "app"
  app_user  = "app"
  port      = 5432
  log_types = ["postgresql", "upgrade"]
}

resource "aws_db_subnet_group" "this" {
  name        = var.identifier
  description = "Private subnets for ${var.identifier}"
  subnet_ids  = var.subnet_ids
  tags        = var.tags
}

# No egress rules: the database never initiates connections.
resource "aws_security_group" "this" {
  name        = var.security_group_name
  description = "PostgreSQL ${var.identifier}: 5432 only from services with database access"
  vpc_id      = var.vpc_id
  tags        = merge(var.tags, { Name = var.security_group_name })
}

resource "aws_vpc_security_group_ingress_rule" "from_service" {
  for_each = var.allowed_security_group_ids

  security_group_id            = aws_security_group.this.id
  description                  = "PostgreSQL from service ${each.key}"
  ip_protocol                  = "tcp"
  from_port                    = local.port
  to_port                      = local.port
  referenced_security_group_id = each.value
  tags                         = var.tags
}

resource "aws_db_parameter_group" "this" {
  name        = "${var.identifier}-pg${var.engine_version}"
  family      = "postgres${var.engine_version}"
  description = "Hermes parameters for ${var.identifier} (TLS enforced)"
  tags        = var.tags

  parameter {
    name         = "rds.force_ssl"
    value        = "1"
    apply_method = "pending-reboot"
  }

  # A major-version change creates a new group before the instance switches to it.
  lifecycle {
    create_before_destroy = true
  }
}

# Pre-created so exported logs get retention and the CMK (RDS would create them without either).
resource "aws_cloudwatch_log_group" "exports" {
  for_each = toset(local.log_types)

  name              = "/aws/rds/instance/${var.identifier}/${each.key}"
  retention_in_days = var.log_retention_days
  kms_key_id        = var.kms_key_arn
  tags              = var.tags
}

resource "aws_iam_role" "monitoring" {
  count = var.enhanced_monitoring ? 1 : 0

  name = var.monitoring_role_name
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "monitoring.rds.amazonaws.com" }
      Action    = "sts:AssumeRole"
      Condition = { StringEquals = { "aws:SourceAccount" = var.account_id } }
    }]
  })
  tags = var.tags

  lifecycle {
    precondition {
      condition     = var.monitoring_role_name != ""
      error_message = "monitoring_role_name is required when enhanced_monitoring is true."
    }
  }
}

resource "aws_iam_role_policy_attachment" "monitoring" {
  count = var.enhanced_monitoring ? 1 : 0

  role       = aws_iam_role.monitoring[0].name
  policy_arn = "arn:${var.partition}:iam::aws:policy/service-role/AmazonRDSEnhancedMonitoringRole"
}

resource "aws_db_instance" "this" {
  identifier     = var.identifier
  engine         = "postgres"
  engine_version = var.engine_version
  instance_class = local.instance_class
  port           = local.port
  db_name        = local.db_name

  username                            = "hermes_admin"
  manage_master_user_password         = true
  master_user_secret_kms_key_id       = var.kms_key_arn
  iam_database_authentication_enabled = true

  allocated_storage     = var.storage_gb
  max_allocated_storage = var.max_storage_gb > var.storage_gb ? var.max_storage_gb : 0
  storage_type          = "gp3"
  storage_encrypted     = true
  kms_key_id            = var.kms_key_arn

  multi_az               = var.high_availability
  db_subnet_group_name   = aws_db_subnet_group.this.name
  vpc_security_group_ids = [aws_security_group.this.id]
  publicly_accessible    = false
  parameter_group_name   = aws_db_parameter_group.this.name

  backup_retention_period   = var.backup_retention_days
  copy_tags_to_snapshot     = true
  delete_automated_backups  = !var.protected
  deletion_protection       = var.deletion_protection
  skip_final_snapshot       = !var.protected
  final_snapshot_identifier = var.protected ? "${var.identifier}-final" : null

  auto_minor_version_upgrade  = true
  allow_major_version_upgrade = true
  apply_immediately           = !var.protected

  performance_insights_enabled          = true
  performance_insights_kms_key_id       = var.kms_key_arn
  performance_insights_retention_period = 7

  enabled_cloudwatch_logs_exports = local.log_types
  monitoring_interval             = var.enhanced_monitoring ? 60 : 0
  monitoring_role_arn             = var.enhanced_monitoring ? "arn:${var.partition}:iam::${var.account_id}:role/${aws_iam_role.monitoring[0].name}" : null

  tags = var.tags

  depends_on = [aws_cloudwatch_log_group.exports, aws_iam_role_policy_attachment.monitoring]
}

resource "aws_cloudwatch_metric_alarm" "cpu_high" {
  count = var.alarms_enabled ? 1 : 0

  alarm_name          = "${var.identifier}-cpu-high"
  alarm_description   = "Database CPU above 80% for 15 minutes."
  namespace           = "AWS/RDS"
  metric_name         = "CPUUtilization"
  dimensions          = { DBInstanceIdentifier = aws_db_instance.this.identifier }
  statistic           = "Average"
  period              = 300
  evaluation_periods  = 3
  threshold           = 80
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "missing"
  alarm_actions       = [var.alarm_topic_arn]
  ok_actions          = [var.alarm_topic_arn]
  tags                = var.tags
}

resource "aws_cloudwatch_metric_alarm" "free_storage_low" {
  count = var.alarms_enabled ? 1 : 0

  alarm_name          = "${var.identifier}-free-storage-low"
  alarm_description   = "Free storage below 10% of the initial allocation (${var.storage_gb} GiB)."
  namespace           = "AWS/RDS"
  metric_name         = "FreeStorageSpace"
  dimensions          = { DBInstanceIdentifier = aws_db_instance.this.identifier }
  statistic           = "Minimum"
  period              = 300
  evaluation_periods  = 1
  threshold           = floor(var.storage_gb * 1073741824 / 10)
  comparison_operator = "LessThanThreshold"
  treat_missing_data  = "missing"
  alarm_actions       = [var.alarm_topic_arn]
  ok_actions          = [var.alarm_topic_arn]
  tags                = var.tags
}
