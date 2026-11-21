# Per-service identities.
#   execution role: used by ECS to pull the image, write the service log group and read ONLY
#                   the secrets referenced by the service's secret_env.
#   task role:      the application identity, derived from spec `access` (exact ARNs only).

locals {
  ecs_tasks_trust = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
      Action    = "sts:AssumeRole"
      Condition = {
        StringEquals = { "aws:SourceAccount" = var.account_id }
        ArnLike      = { "aws:SourceArn" = "arn:${var.partition}:ecs:${var.region}:${var.account_id}:*" }
      }
    }]
  })

  # Same-account/any-account private ECR image -> [account, region, repository]; null otherwise
  # (public registries need no permissions).
  ecr = {
    for k, s in var.services : k => try(regex("^([0-9]{12})\\.dkr\\.ecr\\.([a-z0-9-]+)\\.amazonaws\\.com(?:\\.cn)?/([^:@]+)", s.image), null)
  }

  execution_statements = {
    for k, s in var.services : k => [for st in [
      {
        enabled = true
        body = {
          Sid      = "WriteServiceLogs"
          Effect   = "Allow"
          Action   = ["logs:CreateLogStream", "logs:PutLogEvents"]
          Resource = [s.log_group_arn, "${s.log_group_arn}:*"]
        }
      },
      {
        enabled = local.ecr[k] != null
        body = {
          Sid      = "EcrAuthorizationToken"
          Effect   = "Allow"
          Action   = ["ecr:GetAuthorizationToken"]
          Resource = "*"
        }
      },
      {
        enabled = local.ecr[k] != null
        body = {
          Sid      = "PullServiceImage"
          Effect   = "Allow"
          Action   = ["ecr:BatchCheckLayerAvailability", "ecr:BatchGetImage", "ecr:GetDownloadUrlForLayer"]
          Resource = local.ecr[k] == null ? "" : "arn:${var.partition}:ecr:${local.ecr[k][1]}:${local.ecr[k][0]}:repository/${local.ecr[k][2]}"
        }
      },
      {
        enabled = length(s.secrets) > 0
        body = {
          Sid      = "ReadServiceSecrets"
          Effect   = "Allow"
          Action   = ["secretsmanager:GetSecretValue"]
          Resource = distinct(values(s.secrets))
        }
      },
      {
        enabled = length(s.secrets) > 0 && var.kms_enabled
        body = {
          Sid       = "DecryptServiceSecrets"
          Effect    = "Allow"
          Action    = ["kms:Decrypt"]
          Resource  = var.kms_key_arn
          Condition = { StringEquals = { "kms:ViaService" = "secretsmanager.${var.region}.amazonaws.com" } }
        }
      },
    ] : st.body if st.enabled]
  }

  kms_via = {
    for k, s in var.services : k => concat(
      length(s.access.bucket_read_arns) + length(s.access.bucket_read_write_arns) > 0 ? ["s3.${var.region}.amazonaws.com"] : [],
      length(s.access.topic_arns) > 0 ? ["sns.${var.region}.amazonaws.com"] : [],
      length(s.access.queue_arns) > 0 ? ["sqs.${var.region}.amazonaws.com"] : [],
    )
  }

  task_statements = {
    for k, s in var.services : k => [for st in [
      {
        enabled = length(s.access.bucket_read_arns) + length(s.access.bucket_read_write_arns) > 0
        body = {
          Sid      = "ListGrantedBuckets"
          Effect   = "Allow"
          Action   = ["s3:ListBucket", "s3:GetBucketLocation"]
          Resource = concat(s.access.bucket_read_arns, s.access.bucket_read_write_arns)
        }
      },
      {
        enabled = length(s.access.bucket_read_arns) + length(s.access.bucket_read_write_arns) > 0
        body = {
          Sid      = "ReadGrantedObjects"
          Effect   = "Allow"
          Action   = ["s3:GetObject", "s3:GetObjectVersion"]
          Resource = [for a in concat(s.access.bucket_read_arns, s.access.bucket_read_write_arns) : "${a}/*"]
        }
      },
      {
        enabled = length(s.access.bucket_read_write_arns) > 0
        body = {
          Sid      = "WriteGrantedObjects"
          Effect   = "Allow"
          Action   = ["s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload"]
          Resource = [for a in s.access.bucket_read_write_arns : "${a}/*"]
        }
      },
      {
        enabled = length(s.access.bucket_read_write_arns) > 0
        body = {
          Sid      = "ListMultipartUploads"
          Effect   = "Allow"
          Action   = ["s3:ListBucketMultipartUploads"]
          Resource = s.access.bucket_read_write_arns
        }
      },
      {
        enabled = length(s.access.topic_arns) > 0
        body = {
          Sid      = "PublishGrantedTopics"
          Effect   = "Allow"
          Action   = ["sns:Publish"]
          Resource = s.access.topic_arns
        }
      },
      {
        enabled = length(s.access.queue_arns) > 0
        body = {
          Sid      = "ConsumeGrantedQueues"
          Effect   = "Allow"
          Action   = ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:ChangeMessageVisibility", "sqs:GetQueueAttributes", "sqs:GetQueueUrl"]
          Resource = s.access.queue_arns
        }
      },
      {
        enabled = s.access.database && var.database != null
        body = {
          Sid      = "ConnectAsAppDatabaseUser"
          Effect   = "Allow"
          Action   = ["rds-db:connect"]
          Resource = try(var.database.iam_connect_arn, "")
        }
      },
      {
        enabled = var.tracing
        body = {
          Sid      = "WriteTraces"
          Effect   = "Allow"
          Action   = ["xray:PutTraceSegments", "xray:PutTelemetryRecords", "xray:GetSamplingRules", "xray:GetSamplingTargets", "xray:GetSamplingStatisticSummaries"]
          Resource = "*"
        }
      },
      {
        enabled = var.tracing && var.metrics
        body = {
          Sid      = "CollectorEmbeddedMetrics"
          Effect   = "Allow"
          Action   = ["logs:CreateLogStream", "logs:PutLogEvents", "logs:DescribeLogStreams"]
          Resource = [s.log_group_arn, "${s.log_group_arn}:*"]
        }
      },
      {
        enabled = var.metrics
        body = {
          Sid      = "PutCustomMetrics"
          Effect   = "Allow"
          Action   = ["cloudwatch:PutMetricData"]
          Resource = "*"
        }
      },
      {
        enabled = var.kms_enabled && length(local.kms_via[k]) > 0
        body = {
          Sid       = "UseEnvironmentKeyViaGrantedServices"
          Effect    = "Allow"
          Action    = ["kms:Decrypt", "kms:GenerateDataKey"]
          Resource  = var.kms_key_arn
          Condition = { StringEquals = { "kms:ViaService" = local.kms_via[k] } }
        }
      },
    ] : st.body if st.enabled]
  }

  task_policy_services = toset([for k, v in local.task_statements : k if length(v) > 0])
}

resource "aws_iam_role" "execution" {
  for_each = var.services

  name               = each.value.names.execution_role
  description        = "ECS execution role for service ${each.key}"
  assume_role_policy = local.ecs_tasks_trust
  tags               = var.tags
}

resource "aws_iam_role_policy" "execution" {
  for_each = var.services

  name   = "execution"
  role   = aws_iam_role.execution[each.key].name
  policy = jsonencode({ Version = "2012-10-17", Statement = local.execution_statements[each.key] })
}

resource "aws_iam_role" "task" {
  for_each = var.services

  name               = each.value.names.task_role
  description        = "Application identity for service ${each.key}"
  assume_role_policy = local.ecs_tasks_trust
  tags               = var.tags
}

resource "aws_iam_role_policy" "task" {
  for_each = local.task_policy_services

  name   = "access"
  role   = aws_iam_role.task[each.key].name
  policy = jsonencode({ Version = "2012-10-17", Statement = local.task_statements[each.key] })
}
