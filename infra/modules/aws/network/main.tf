# VPC with one public and one private subnet per zone.
#
# Address plan (documented in docs/providers/aws.md):
#   private subnet i = cidrsubnet(cidr, 2, i)                      (quarters 0..2; /18 in a /16)
#   public  subnet i = cidrsubnet(cidrsubnet(cidr, 2, 3), 3, i)    (eighths of the last quarter; /21 in a /16)
# Public subnets hold only the ALB and NAT gateways; tasks and the database live in private subnets.

locals {
  azs           = slice(var.availability_zones, 0, min(var.zone_count, length(var.availability_zones)))
  private_cidrs = [for i in range(var.zone_count) : cidrsubnet(var.cidr, 2, i)]
  public_cidrs  = [for i in range(var.zone_count) : cidrsubnet(cidrsubnet(var.cidr, 2, 3), 3, i)]
  nat_count     = var.nat ? (var.nat_per_az ? var.zone_count : 1) : 0
  flow_log_arn  = "arn:${var.partition}:logs:${var.region}:${var.account_id}:log-group:${var.flow_log_group_name}"
}

resource "aws_vpc" "this" {
  cidr_block           = var.cidr
  enable_dns_support   = true
  enable_dns_hostnames = true
  tags                 = merge(var.tags, { Name = var.name })

  lifecycle {
    precondition {
      condition     = length(var.availability_zones) >= var.zone_count
      error_message = "The region exposes ${length(var.availability_zones)} available zones but network.zones is ${var.zone_count}."
    }
  }
}

# Removes every rule from the default security group so nothing can use it by accident.
resource "aws_default_security_group" "this" {
  vpc_id = aws_vpc.this.id
  tags   = merge(var.tags, { Name = "${var.name}-default-deny" })
}

resource "aws_internet_gateway" "this" {
  vpc_id = aws_vpc.this.id
  tags   = merge(var.tags, { Name = var.name })
}

resource "aws_subnet" "public" {
  count = var.zone_count

  vpc_id                  = aws_vpc.this.id
  cidr_block              = local.public_cidrs[count.index]
  availability_zone       = element(local.azs, count.index)
  map_public_ip_on_launch = false
  tags                    = merge(var.tags, { Name = "${var.name}-public-${element(local.azs, count.index)}", "hermes-subnet-tier" = "public" })
}

resource "aws_subnet" "private" {
  count = var.zone_count

  vpc_id            = aws_vpc.this.id
  cidr_block        = local.private_cidrs[count.index]
  availability_zone = element(local.azs, count.index)
  tags              = merge(var.tags, { Name = "${var.name}-private-${element(local.azs, count.index)}", "hermes-subnet-tier" = "private" })
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.this.id
  tags   = merge(var.tags, { Name = "${var.name}-public" })
}

resource "aws_route" "public_internet" {
  route_table_id         = aws_route_table.public.id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.this.id
}

resource "aws_route_table_association" "public" {
  count = var.zone_count

  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

resource "aws_eip" "nat" {
  count = local.nat_count

  domain = "vpc"
  tags   = merge(var.tags, { Name = "${var.name}-nat-${element(local.azs, count.index)}" })
}

resource "aws_nat_gateway" "this" {
  count = local.nat_count

  allocation_id = aws_eip.nat[count.index].id
  subnet_id     = aws_subnet.public[count.index].id
  tags          = merge(var.tags, { Name = "${var.name}-nat-${element(local.azs, count.index)}" })

  depends_on = [aws_internet_gateway.this]
}

# One private route table per zone, so switching between single and per-zone NAT only changes routes.
resource "aws_route_table" "private" {
  count = var.zone_count

  vpc_id = aws_vpc.this.id
  tags   = merge(var.tags, { Name = "${var.name}-private-${element(local.azs, count.index)}" })
}

resource "aws_route" "private_nat" {
  count = var.nat ? var.zone_count : 0

  route_table_id         = aws_route_table.private[count.index].id
  destination_cidr_block = "0.0.0.0/0"
  nat_gateway_id         = aws_nat_gateway.this[var.nat_per_az ? count.index : 0].id
}

resource "aws_route_table_association" "private" {
  count = var.zone_count

  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private[count.index].id
}

# S3 gateway endpoint: free, keeps S3 traffic (including ECR image layers) off the NAT gateway.
resource "aws_vpc_endpoint" "s3" {
  vpc_id            = aws_vpc.this.id
  service_name      = "com.amazonaws.${var.region}.s3"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = aws_route_table.private[*].id
  tags              = merge(var.tags, { Name = "${var.name}-s3" })
}

# --- VPC flow logs -------------------------------------------------------------------------

resource "aws_cloudwatch_log_group" "flow_logs" {
  count = var.flow_logs_enabled ? 1 : 0

  name              = var.flow_log_group_name
  retention_in_days = var.log_retention_days
  kms_key_id        = var.kms_key_arn
  tags              = var.tags
}

resource "aws_iam_role" "flow_logs" {
  count = var.flow_logs_enabled ? 1 : 0

  name = var.flow_log_role_name
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "vpc-flow-logs.amazonaws.com" }
      Action    = "sts:AssumeRole"
      Condition = {
        StringEquals = { "aws:SourceAccount" = var.account_id }
        ArnLike      = { "aws:SourceArn" = "arn:${var.partition}:ec2:${var.region}:${var.account_id}:vpc-flow-log/*" }
      }
    }]
  })
  tags = var.tags
}

resource "aws_iam_role_policy" "flow_logs" {
  count = var.flow_logs_enabled ? 1 : 0

  name = "flow-log-delivery"
  role = aws_iam_role.flow_logs[0].id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "WriteFlowLogGroup"
        Effect   = "Allow"
        Action   = ["logs:CreateLogStream", "logs:PutLogEvents", "logs:DescribeLogStreams"]
        Resource = [local.flow_log_arn, "${local.flow_log_arn}:*"]
      },
      {
        Sid      = "DescribeLogGroups"
        Effect   = "Allow"
        Action   = ["logs:DescribeLogGroups"]
        Resource = "arn:${var.partition}:logs:${var.region}:${var.account_id}:log-group:*"
      },
    ]
  })
}

resource "aws_flow_log" "this" {
  count = var.flow_logs_enabled ? 1 : 0

  vpc_id               = aws_vpc.this.id
  traffic_type         = "ALL"
  log_destination_type = "cloud-watch-logs"
  log_destination      = "arn:${var.partition}:logs:${var.region}:${var.account_id}:log-group:${aws_cloudwatch_log_group.flow_logs[0].name}"
  iam_role_arn         = "arn:${var.partition}:iam::${var.account_id}:role/${aws_iam_role.flow_logs[0].name}"
  tags                 = merge(var.tags, { Name = var.name })

  depends_on = [aws_iam_role_policy.flow_logs]
}
