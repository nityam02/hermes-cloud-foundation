# AWS discovery demo (moto, no real AWS)

`run-demo.sh` runs the discovery and migration pipeline end to end against a **local moto server**
on `127.0.0.1:5055`, using fake credentials and `AWS_CONFIG_FILE` / `AWS_SHARED_CREDENTIALS_FILE`
set to `/dev/null`. No request can reach AWS.

1. `seed_moto.py` uses boto3 to build an environment by hand: no Terraform, no CloudFormation. It
   creates a VPC with public and private subnets, an IGW, a NAT gateway, route tables and security
   groups; two EC2 instances tagged as Docker hosts; an EC2 `vpn-relay` with source/destination check
   disabled; RDS PostgreSQL Multi-AZ with a PostGIS parameter group; S3 buckets with objects; an SNS
   topic fanning out to SQS queues, one with a DLQ, and a FIFO queue; Secrets Manager secrets (value
   `demo-not-a-secret`); a KMS key; IAM roles and an instance profile; DynamoDB; ECS
   cluster/service/task definition; an ALB; a Lambda function with an SQS trigger. It also creates an
   operator user and the `HermesDiscoveryReadOnly` role with the generated policy, plus a demo-only
   deny for `kinesis:ListStreams` and `s3:GetBucketPolicy`.
2. `hermes inventory aws --endpoint-url ... --role-arn ...HermesDiscoveryReadOnly --external-id demo-external-id`.
3. The same discovery again with moto's IAM enforcement switched on. Denied calls become
   `access-denied` gaps, the affected resources become `partial`, and discovery continues.
4. `hermes migrate assess --target gcp`, `migrate plan` and `migrate generate`, then the mermaid graph.
5. The Go integration test `go test -tags moto ./tests/integration/discovery/...` against the same server.

```bash
python3 -m venv .venv && .venv/bin/pip install 'moto[server]' boto3     # once, about 180 MB
VENV=.venv ./run-demo.sh                 # WORK=<dir> keeps the artifacts; RUN_GO_TEST=0 skips the Go test
```

## Result on the maintainer machine (2026-11-19, moto 5.1.22)

Without enforcement: 58 resources of 28 types and 78 relationships. Two gaps appear because moto does
not implement the CBOR protocol the Go SDK now uses for CloudWatch (`DescribeAlarms`, `GetMetricData`),
so they are classified `unsupported`, not treated as an empty result. 81 distinct API operations were
called, all on the read-only allowlist. No secret or environment value appears in any output.

With moto IAM enforcement: 26 `access-denied` gaps. They include the two seeded denies. The others
come from moto's IAM emulation mapping some actions differently from AWS (for example S3 bucket
sub-resources and some REST APIs such as Lambda and Route 53). Those are emulator limitations, not
policy errors. moto also does not evaluate the `sts:ExternalId` trust condition; that the ExternalId
is sent is verified by a unit test against a local fake STS.

Not covered by moto: Cloud Control (unit-tested with fakes), Resource Explorer, CloudWatch metrics
(see above).
