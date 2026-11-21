# Mock-provider tests: they prove wiring and policy contents, NOT real AWS behaviour.
mock_provider "aws" {
  mock_data "aws_caller_identity" {
    defaults = { account_id = "123456789012" }
  }
  mock_data "aws_partition" {
    defaults = { partition = "aws" }
  }
  mock_resource "aws_iam_openid_connect_provider" {
    defaults = { arn = "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com" }
  }
}

variables {
  project           = "sample-platform"
  github_repository = "example-org/example-repo"
  environments      = ["staging", "production"]
  state_bucket      = "example-hermes-state"
}

run "trust_policies_pin_exact_subjects" {
  command = apply

  assert {
    condition = jsondecode(aws_iam_role.plan.assume_role_policy).Statement[0].Condition.StringEquals["token.actions.githubusercontent.com:sub"] == [
      "repo:example-org/example-repo:pull_request",
      "repo:example-org/example-repo:ref:refs/heads/main",
    ]
    error_message = "The plan role must trust exactly the pull_request and main-branch subjects."
  }

  assert {
    condition     = jsondecode(aws_iam_role.apply["production"].assume_role_policy).Statement[0].Condition.StringEquals["token.actions.githubusercontent.com:sub"] == ["repo:example-org/example-repo:environment:production"]
    error_message = "Each apply role must trust only its GitHub environment subject."
  }

  assert {
    condition = alltrue([for r in concat([aws_iam_role.plan], values(aws_iam_role.apply)) :
      jsondecode(r.assume_role_policy).Statement[0].Condition.StringEquals["token.actions.githubusercontent.com:aud"] == "sts.amazonaws.com"
    && !can(jsondecode(r.assume_role_policy).Statement[0].Condition.StringLike)])
    error_message = "Every trust policy must pin the audience and must not use StringLike (wildcard) conditions."
  }

  assert {
    condition     = alltrue([for r in concat([aws_iam_role.plan], values(aws_iam_role.apply)) : !strcontains(r.assume_role_policy, "*")])
    error_message = "Trust policies must not contain wildcards."
  }

  assert {
    condition     = alltrue([for r in concat([aws_iam_role.plan], values(aws_iam_role.apply)) : r.max_session_duration == 3600 && r.path == "/hermes-ci/"])
    error_message = "CI roles must have 1-hour sessions and live under /hermes-ci/."
  }

  assert {
    condition     = sort(keys(aws_iam_role.apply)) == tolist(["production", "staging"])
    error_message = "One apply role per environment."
  }
}

run "plan_role_cannot_write_state" {
  command = apply

  assert {
    condition = [for s in jsondecode(aws_iam_role_policy.plan.policy).Statement : s.Resource if s.Effect == "Allow" && contains(s.Action, "s3:PutObject")] == [
      "arn:aws:s3:::example-hermes-state/hermes/sample-platform/*.tflock",
    ]
    error_message = "The plan role may only write lock files."
  }

  assert {
    condition     = contains(keys(aws_iam_role_policy_attachment.plan), "arn:aws:iam::aws:policy/ReadOnlyAccess")
    error_message = "The plan role gets ReadOnlyAccess for refresh by default."
  }

  assert {
    condition     = length([for s in jsondecode(aws_iam_role_policy.plan.policy).Statement : s if s.Effect == "Deny" && contains(s.Action, "secretsmanager:GetSecretValue")]) == 1
    error_message = "The plan role must explicitly deny reading secret values."
  }
}

run "apply_role_scoped_to_its_environment" {
  command = apply

  assert {
    condition = [for s in jsondecode(aws_iam_role_policy.apply["staging"].policy).Statement : s.Resource if s.Sid == "StateReadWriteThisEnvironment"] == [
      "arn:aws:s3:::example-hermes-state/hermes/sample-platform/staging/*",
    ]
    error_message = "The staging apply role may only write staging state."
  }

  assert {
    condition = alltrue([for s in jsondecode(aws_iam_role_policy.apply["production"].policy).Statement :
    !contains(flatten([s.Action]), "*") if s.Effect == "Allow"])
    error_message = "No Allow statement may grant Action \"*\"."
  }

  assert {
    condition = alltrue([for s in jsondecode(aws_iam_role_policy.apply["production"].policy).Statement :
      alltrue([for a in flatten([s.Action]) : !contains(["s3", "sqs", "sns", "secretsmanager", "logs", "dynamodb", "ssm"], split(":", a)[0]) || can(regex("^[a-z0-9-]+:(List|Describe)", a))])
    if s.Effect == "Allow" && s.Resource == "*"])
    error_message = "Data-plane services must never be granted on Resource \"*\" (List/Describe calls excepted)."
  }

  assert {
    condition = one([for s in jsondecode(aws_iam_role_policy.apply["production"].policy).Statement : s.Condition.StringEquals["iam:PassedToService"] if s.Sid == "PassProjectRolesToServices"]) == [
      "ecs-tasks.amazonaws.com", "monitoring.rds.amazonaws.com", "vpc-flow-logs.amazonaws.com",
    ]
    error_message = "iam:PassRole must be limited to the services the composition uses."
  }

  assert {
    condition     = one([for s in jsondecode(aws_iam_role_policy.apply["production"].policy).Statement : s.Resource if s.Sid == "DenyCiSelfModification"]) == ["arn:aws:iam::123456789012:role/hermes-ci/*", "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com"]
    error_message = "The CI roles must not be able to modify themselves or the OIDC provider."
  }

  assert {
    condition     = !can(one([for s in jsondecode(aws_iam_role_policy.apply["production"].policy).Statement : s.Condition if s.Sid == "WriteProjectRoles"]))
    error_message = "Without a boundary ARN, role writes carry no boundary condition."
  }
}

run "permissions_boundary_enforced_when_set" {
  command = apply

  variables {
    workload_permissions_boundary_arn = "arn:aws:iam::123456789012:policy/hermes-workload-boundary"
    state_kms_key_arn                 = "arn:aws:kms:eu-north-1:123456789012:key/00000000-0000-0000-0000-000000000000"
  }

  assert {
    condition     = one([for s in jsondecode(aws_iam_role_policy.apply["production"].policy).Statement : s.Condition.StringEquals["iam:PermissionsBoundary"] if s.Sid == "WriteProjectRoles"]) == "arn:aws:iam::123456789012:policy/hermes-workload-boundary"
    error_message = "CreateRole/PutRolePolicy must require the permissions boundary."
  }

  assert {
    condition     = length([for s in jsondecode(aws_iam_role_policy.plan.policy).Statement : s if s.Sid == "StateKey"]) == 1
    error_message = "A state KMS key grants decrypt/encrypt on that key only."
  }
}

run "existing_oidc_provider" {
  command = apply

  variables {
    create_oidc_provider = false
    oidc_provider_arn    = "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com"
    plan_on_pull_request = false
    plan_branches        = ["main", "release"]
  }

  assert {
    condition     = length(aws_iam_openid_connect_provider.github) == 0
    error_message = "No provider is created when an existing ARN is passed."
  }

  assert {
    condition     = output.trusted_subjects["plan"] == ["repo:example-org/example-repo:ref:refs/heads/main", "repo:example-org/example-repo:ref:refs/heads/release"]
    error_message = "Without pull_request trust, only the listed branches can plan."
  }
}

run "rejects_wildcard_repository" {
  command = plan

  variables {
    github_repository = "example-org/*"
  }

  expect_failures = [var.github_repository]
}

run "rejects_wildcard_branch" {
  command = plan

  variables {
    plan_branches = ["release/*"]
  }

  expect_failures = [var.plan_branches]
}
