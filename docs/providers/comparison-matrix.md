# Provider comparison matrix

One table, one row per capability feature, for the three providers. **Canonical source:** the Go capability
matrices in `internal/providers/{aws,gcp,alibaba}.go`. `hermes providers` and `hermes portability` read them, so
this table shows exactly what the scorer uses. The notes come from the provider guides
([AWS](aws.md), [GCP](gcp.md), [Alibaba Cloud](alibaba.md)), which have the details.

> **Every status describes the Hermes OpenTofu modules as designed and tested with mock providers.** None is
> verified against a real cloud account. Read "implemented" as "implemented, not tested against a real cloud".
> See the [limitations register](../limitations.md).

Regenerate the status columns with `hermes providers` (text) or `hermes providers -o json`.

## Services behind each capability

| Capability | AWS | GCP | Alibaba Cloud |
|---|---|---|---|
| containerized compute | ECS on Fargate behind one shared ALB per environment | Cloud Run (v2) services | Serverless App Engine (SAE), one internet CLB per public service |
| database | RDS for PostgreSQL (Multi-AZ when HA) | Cloud SQL for PostgreSQL, Enterprise (REGIONAL when HA) | ApsaraDB RDS for PostgreSQL (High-availability or Basic Edition) |
| object storage | S3 | Cloud Storage | OSS |
| messaging | SNS topic + one SQS queue per subscription | Pub/Sub topic + pull subscriptions | MNS (Simple Message Queue) topic + queue per subscription |
| secrets | Secrets Manager | Secret Manager | KMS Secrets Manager (needs a KMS 3.0 instance) |
| observability | CloudWatch Logs and alarms, X-Ray via an ADOT sidecar | Cloud Logging bucket, Cloud Monitoring, Cloud Trace via an OTel Collector sidecar | SLS logstore, CloudMonitor |
| state backend | S3 (native lockfile, or DynamoDB) | GCS (native locking) | OSS + Tablestore locking |
| engine providers (exact pins) | `hashicorp/aws` 6.73.0 | `hashicorp/google` 8.12.0 + `hashicorp/google-beta` 8.12.0 | `aliyun/alicloud` 2.2.0 |

All three also pin `hashicorp/random` 3.9.1.

## Capability matrix

Legend: ✅ implemented · ◐ partial (scored 0.5) · ✗ not implemented (scored 0).

| Feature | AWS | GCP | Alibaba Cloud |
|---|---|---|---|
| `compute.public_https` | ✅ Shared ALB. HTTPS only with `extensions.aws.acm_certificate_arn`, otherwise HTTP, which protected environments cannot plan (`HCF207`: warning at validate, error at plan) | ✅ Managed `run.app` HTTPS URL. No custom domains | ✅ SAE internet CLB. HTTPS only with `extensions.alibaba.certificate_id`; protected + public + no certificate cannot be planned (`HCF207`) |
| `compute.scale_to_zero` | ◐ Only queue consumers scale from zero (SQS backlog). HTTP services stay at `min_instances` | ✅ `min_instances: 0`, request-driven | ✗ SAE minimum is 1 instance (the provider guide labels this *not supported*) |
| `compute.otel_sidecar` | ✅ ADOT collector sidecar per task | ✅ OTel Collector contrib sidecar | ✗ No sidecar. Export to `extensions.alibaba.otlp_endpoint` |
| `database.high_availability` | ✅ RDS Multi-AZ | ✅ Cloud SQL `REGIONAL` | ✅ High-availability Edition, standby in a second zone |
| `database.postgis` | ◐ Available; `CREATE EXTENSION` is manual | ◐ Available; `CREATE EXTENSION` is manual | ◐ Available; `CREATE EXTENSION` is manual |
| `database.iam_auth` | ✅ `rds-db:connect` for user `app` (`HERMES_DB_AUTH=iam`); create the user with `rds_iam` | ✅ One IAM DB user per service account; GRANTs are manual | ✗ Password authentication; the account is created out-of-band (the provider guide labels this *not supported*) |
| `database.pitr` | ✅ Always on with automated backups; `point_in_time_recovery: false` cannot disable it | ✅ `point_in_time_recovery_enabled` | ◐ High-availability Edition only |
| `database.customer_managed_keys` | ✅ KMS CMK per environment | ✅ Cloud SQL CMEK via the google-beta service identity | ✅ RDS disk encryption with KMS; needs a manual RDS→KMS authorization |
| `storage.versioning` | ✅ | ✅ | ✅ Once enabled, it can only be suspended |
| `storage.retention` | ✅ Lifecycle expiration | ✅ Lifecycle `Delete` at `age = retention_days` | ✅ Lifecycle expiration |
| `storage.customer_managed_keys` | ✅ SSE-KMS with bucket keys | ✅ CMEK default key | ✅ SSE-KMS |
| `messaging.dead_letter` | ✅ SQS DLQ + redrive | ✅ Dead-letter topic (5–100 attempts) | ✅ MNS dead-letter queue |
| `messaging.retention` | ✅ Per-subscription queue (≤ 7 days by contract) | ✅ Topic + subscription (subscription ≤ 7 days) | ✅ Queue retention (≤ 7 days); topics do not retain |
| `secrets.native_injection` | ✅ ECS `secrets[].valueFrom` | ✅ Cloud Run `secret_key_ref` (`latest`) | ✗ No injection into SAE; apps read secrets with their role |
| `observability.logs_retention` | ✅ Rounded up to a CloudWatch value (`HCF202`) | ✅ Dedicated log bucket + sink; the `_Default` exclusion is unverified | ✅ SLS logstore TTL |
| `observability.alerts` | ✅ CloudWatch alarms → SNS e-mail; fixed thresholds | ✅ Alert policies → e-mail channel | ◐ Contact group + RDS CPU and disk alarms only |
| `observability.tracing` | ✅ OTLP → ADOT → X-Ray | ✅ OTLP → collector → Cloud Trace | ◐ Endpoint from the extension only; no collector |
| `state.remote_backend` | ✅ S3 | ✅ GCS | ✅ OSS |
| `state.locking` | ✅ S3 lockfile (`use_lockfile`), or DynamoDB with `state.lock_table` | ✅ GCS native | ✅ Tablestore (`state_tablestore_endpoint` + `_table`; required when protected) |
| `state.client_side_encryption` | ✅ OpenTofu only: `aws_kms` key provider | ✅ OpenTofu only: `gcp_kms` key provider | ✗ No OpenTofu Alibaba KMS key provider (`HCF208`) |
| `identity.per_service` | ✅ One IAM task role per service, scoped from `access` | ✅ One service account per service, scoped from `access` | ◐ RAM role per service, but binding it to the SAE app is manual |

Totals (from `hermes providers -o json`, 21 features):

| | implemented | partial | not implemented |
|---|---|---|---|
| AWS | 19 | 2 | 0 |
| GCP | 20 | 1 | 0 |
| Alibaba Cloud | 11 | 5 | 5 |

## Behaviour that differs even when every row is ✅

These do not change the score, but they change what an application or operator sees:

| Topic | AWS | GCP | Alibaba Cloud |
|---|---|---|---|
| Size rounding | Invalid Fargate CPU/memory pairs are an error (`HCF201`) | CPU rounded up to 1/2/4/6/8 vCPU (`HCF202`) | Rounded to the nearest SAE pair ≥ the request (`HCF202`) |
| `network.zones` | One public + one private subnet per zone | No effect (regional subnet) | One vSwitch per zone |
| NAT | One NAT gateway; one per zone when protected | Cloud NAT (regional) | Enhanced NAT (CrossAZ) + EIP |
| Private services URL | `""` (no service discovery) | `""` (internal-only, no private DNS) | `""` (no internal load balancer) |
| Unknown `extensions.<p>` keys | error | error | warning |
| Infracost (`hermes cost`) | supported | supported | not supported |
| Scanner coverage | TFLint, Trivy, Checkov, conftest | TFLint, Trivy, Checkov, conftest | conftest only (no TFLint ruleset, no Trivy checks, few Checkov checks) |

## Discrepancies between the provider guides and the Go matrices (2026-11-19)

They agree on every status that appears in both. The differences are omissions and wording, reported to the
lead rather than resolved here:

1. **`state.client_side_encryption` is missing** from the feature tables in all three provider guides. Go lists it
   (AWS ✅, GCP ✅, Alibaba ✗). It is covered in [state management](../operations/state-management.md) and in the
   Alibaba guide's known limitations.
2. **Vocabulary:** the Alibaba guide says *not supported* for `compute.scale_to_zero` and `database.iam_auth`.
   The Go matrix has only `implemented`, `partial` and `not-implemented`, so these print as `not-implemented`.
   Both score 0. *Not supported* is the more accurate label for scale to zero, because SAE cannot do it.
3. **AWS `state.locking`:** the guide mentions only `use_lockfile`. Go also lists DynamoDB through
   `state.lock_table`.
4. **Alibaba extension keys:** Go documents `state_tablestore_endpoint` and `state_tablestore_table` (read by
   `hermes`, not by the module). The Alibaba guide's extension-key table omits them.
5. **GCP CMEK:** both say Cloud Run and the log bucket stay Google-managed. The guide calls CMEK "partial
   overall", while the Go matrix marks the database and storage CMK rows ✅. Both are accurate for the resources
   they describe. There is no separate "CMEK everywhere" feature.
6. **Test depth differs:** the Alibaba `kms`, `observability` and `secrets` modules, and the GCP `kms` module,
   have no module-level `tofu test` files. They are covered only through the composition and contract runs.
