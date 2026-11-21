mock_provider "aws" {}

variables {
  name                = "shop-dev"
  cidr                = "10.20.0.0/16"
  availability_zones  = ["eu-north-1a", "eu-north-1b", "eu-north-1c"]
  zone_count          = 2
  nat                 = true
  nat_per_az          = false
  flow_logs_enabled   = true
  flow_log_group_name = "/hermes/shop/dev/vpc-flow-logs"
  flow_log_role_name  = "shop-dev-flow-logs"
  account_id          = "123456789012"
  region              = "eu-north-1"
}

run "single_nat_by_default" {
  command = apply

  assert {
    condition     = length(aws_nat_gateway.this) == 1 && length(aws_route.private_nat) == 2
    error_message = "Non-protected environments use one shared NAT gateway routed from every private subnet."
  }

  assert {
    condition     = tolist(aws_subnet.private[*].cidr_block) == tolist(["10.20.0.0/18", "10.20.64.0/18"]) && tolist(aws_subnet.public[*].cidr_block) == tolist(["10.20.192.0/21", "10.20.200.0/21"])
    error_message = "Subnets must follow the documented address plan."
  }

  assert {
    condition     = alltrue([for s in aws_subnet.public : !s.map_public_ip_on_launch])
    error_message = "Public subnets must not auto-assign public IPs."
  }

  assert {
    condition     = length(aws_default_security_group.this.ingress) == 0 && length(aws_default_security_group.this.egress) == 0
    error_message = "The default security group must have no rules."
  }

  assert {
    condition     = aws_vpc.this.enable_dns_support && aws_vpc.this.enable_dns_hostnames
    error_message = "VPC DNS support and hostnames must be enabled."
  }

  assert {
    condition     = aws_flow_log.this[0].traffic_type == "ALL" && aws_cloudwatch_log_group.flow_logs[0].retention_in_days == 30
    error_message = "Flow logs must capture all traffic into a retained log group."
  }

  assert {
    condition     = aws_vpc_endpoint.s3.vpc_endpoint_type == "Gateway" && aws_vpc_endpoint.s3.service_name == "com.amazonaws.eu-north-1.s3"
    error_message = "An S3 gateway endpoint must be attached."
  }
}

run "nat_per_zone_when_protected" {
  command = apply

  variables {
    zone_count  = 3
    nat_per_az  = true
    kms_key_arn = "arn:aws:kms:eu-north-1:123456789012:key/00000000-0000-4000-8000-000000000000"
  }

  assert {
    condition     = length(aws_nat_gateway.this) == 3 && length(aws_eip.nat) == 3
    error_message = "Protected environments get one NAT gateway per zone."
  }

  assert {
    condition     = aws_cloudwatch_log_group.flow_logs[0].kms_key_id == "arn:aws:kms:eu-north-1:123456789012:key/00000000-0000-4000-8000-000000000000"
    error_message = "The flow-log group must use the CMK when one is given."
  }
}

run "no_nat_and_no_flow_logs" {
  command = apply

  variables {
    nat               = false
    flow_logs_enabled = false
  }

  assert {
    condition     = length(aws_nat_gateway.this) == 0 && length(aws_route.private_nat) == 0 && length(aws_flow_log.this) == 0 && length(aws_iam_role.flow_logs) == 0
    error_message = "nat=false must create no NAT gateways or default routes; disabled flow logs create nothing."
  }
}

run "small_vpc_cidr" {
  command = plan

  variables {
    cidr       = "10.30.0.0/20"
    zone_count = 3
  }

  assert {
    condition     = aws_subnet.private[2].cidr_block == "10.30.8.0/22" && aws_subnet.public[2].cidr_block == "10.30.13.0/25"
    error_message = "A /20 VPC must still fit three private /22 and three public /25 subnets."
  }
}

run "rejects_too_few_zones" {
  command = plan

  variables {
    availability_zones = ["eu-north-1a"]
  }

  expect_failures = [aws_vpc.this]
}
