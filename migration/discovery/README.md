# Read-only discovery role for AWS (`HermesDiscoveryReadOnly`)

`hermes inventory aws` discovers an AWS account through official read-only APIs. It needs no agent,
no Terraform or CloudFormation state, and works for environments built by hand. In each account
to inspect, create one IAM role that the operator assumes with an ExternalId.

| File | What it is |
|---|---|
| `aws-readonly-discovery-policy.json` | The least-privilege policy. It is generated from the same operation registry that the client-side read-only guard enforces (`hermes inventory policy` prints it; a Go test fails if they differ) |
| `cloudformation/hermes-discovery-role.json` | CloudFormation template: the role, its trust policy (one principal, `sts:ExternalId` required), a 1-hour maximum session and the inline policy. Use it if the source organization does not use Terraform |
| `terraform/` | Plain-HCL equivalent (`trusted_principal_arn`, `external_id`, `role_name`, `tags`; output `role_arn`). Tested with `tofu validate` and `tofu test` with a mock provider |

## What the policy allows and denies

- **Allow** (92 actions, `Resource: "*"` because List/Describe actions do not support resource-level
  scoping): `Describe*`, `List*` and non-secret `Get*` metadata calls for EC2/VPC, ELBv2, RDS, S3
  bucket configuration, ECS, EKS, Lambda configuration, ECR, SQS, SNS, DynamoDB, ElastiCache,
  Kinesis, Route 53, ACM, IAM roles, Secrets Manager and SSM *metadata*, KMS, CloudWatch Logs and
  alarms, CloudFront, Step Functions, EventBridge, CloudFormation stack listings, and the Cloud
  Control API. `cloudwatch:GetMetricData` gives S3 bucket sizes for transfer estimates.
- **Allow `apigateway:GET`** only on `arn:aws:apigateway:*::/apis` and `/apis/*` (HTTP/WebSocket APIs).
- **Deny** (explicit, wins over any other policy attached to the role): secret values
  (`secretsmanager:GetSecretValue`, `BatchGetSecretValue`, `ssm:GetParameter*`), data
  (`s3:GetObject*`, `dynamodb:GetItem/BatchGetItem/Query/Scan`, `sqs:ReceiveMessage`,
  `kinesis:GetRecords/GetShardIterator`, `logs:GetLogEvents/FilterLogEvents/StartQuery`,
  `rds:DownloadDBLogFilePortion`), code and image downloads (`lambda:GetFunction`, `GetLayerVersion`,
  `ecr:GetDownloadUrlForLayer`, `BatchGetImage`, `GetAuthorizationToken`), and secrets in metadata
  responses (`ec2:GetPasswordData`, `GetConsoleOutput`, `GetLaunchTemplateData`,
  `cloudformation:GetTemplate`, `kms:Decrypt`).

The client-side guard is a second, independent layer: any operation that is not in the registry or
not named like a read is refused **before** the request is signed or sent.

Some allowed responses contain more than Hermes keeps. Hermes discards these fields at collection time
and never writes them: Lambda and ECS environment variable *values* (names only), VPN tunnel pre-shared
keys and customer gateway configuration, SNS email/SMS/HTTP endpoints, IAM trust policy documents
(principals are summarized), and bucket policies (flags only).

The optional Cloud Control fallback (`--cloudcontrol-types`) also needs the read permissions of each
type you list (for example `iot:ListThings` and `iot:DescribeThing` for `AWS::IoT::Thing`). Add them in
a separate statement. Properties with names matching password, secret, token, private key or
credential are dropped before storage.

## Why an ExternalId

The role trusts one principal (the operator's role or account). The `sts:ExternalId` condition stops
a confused-deputy attack: someone who knows the role ARN cannot make a tool that runs with the
operator's principal assume it for them without the agreed value. Use a random value of 16 or more
characters, share it out-of-band, and pass it with `--external-id`. Sessions are limited to 1 hour
(`MaxSessionDuration: 3600`); Hermes requests exactly 1 hour with session name `hermes-discovery`, so
CloudTrail shows every call as `assumed-role/HermesDiscoveryReadOnly/hermes-discovery`.

## Deploy

Console: CloudFormation, then Create stack, then upload `cloudformation/hermes-discovery-role.json`,
then set `TrustedPrincipalArn`, `ExternalId` and optionally `RoleName`. The stack output `RoleArn` is the
value for `--role-arn`.

CLI (run by an administrator of the inspected account, not by Hermes):

```bash
aws cloudformation deploy --stack-name hermes-discovery \
  --template-file migration/discovery/cloudformation/hermes-discovery-role.json \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameter-overrides TrustedPrincipalArn=arn:aws:iam::<operator-account>:role/<operator-role> ExternalId=<random-value>
```

OpenTofu/Terraform: call `migration/discovery/terraform` as a module with `trusted_principal_arn` and
`external_id`, then apply it with the inspected account's credentials.

Then, from the operator side:

```bash
hermes inventory aws --role-arn <RoleArn> --external-id <random-value> --regions eu-north-1 --out inventory.json
```

## Revoke

Delete the stack (`aws cloudformation delete-stack --stack-name hermes-discovery`) or destroy the
module. Existing sessions stay valid until they expire (at most 1 hour). To cut them off at once,
use the IAM console's "Revoke active sessions" on the role first. Keep the role only for the
duration of the migration.

## Status

Policy, template and module are **implemented and verified offline**. A Go test checks that they match
the registry; `tofu validate` and `tofu test` (mock provider) pass; the moto demo assumes a role
with this policy. **Not tested against a real AWS account** by the maintainers. Use
`tests/integration/aws-real` to check your own account.
