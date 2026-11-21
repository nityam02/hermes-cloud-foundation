# Mock-provider plan of a rendered Hermes root (multi-cloud example, environment awsdev), used by
# scripts/gen-policy-fixtures.sh to derive policies/tests/fixtures/aws_default/plan.json.
# Mock providers only: no cloud API is called.

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
  # Realistic values where the provider validates the format of a referencing argument
  # (copied from infra/providers/aws/platform/tests/aws.tftest.hcl).
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
      # Obviously fake value for a computed attribute (a random mock string trips gitleaks).
      routing_http_response_access_control_allow_credentials_header_value = "demo-not-a-secret"
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

run "plan" {
  command = plan
}
