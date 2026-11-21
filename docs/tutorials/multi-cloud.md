# Multi-cloud tutorial: one spec, three clouds

This walk-through uses [`examples/multi-cloud/hermes.yaml`](../../examples/multi-cloud/hermes.yaml). The spec
declares three environments, `awsdev`, `gcpdev` and `alidev`. Each one overrides only `cloud.*` and
`state.bucket`. Services, access grants, database, buckets, topics and secrets are written once.

All output below was captured offline on 2026-11-19. **Nothing was deployed.** Account IDs, project IDs and
bucket names are placeholders.

## 1. The spec, in brief

```yaml
project:
  name: sample-portable
  environment: awsdev          # the default environment
cloud:
  provider: aws
  region: eu-north-1
  account: "123456789012"
state:
  bucket: example-hermes-state-aws
workloads:
  compute:
    services:
      - name: api              # public; reads/writes bucket "assets", publishes "events", uses the DB
        cpu: 1                 # a size every provider supports exactly
        memory_mb: 2048
        ...
      - name: consumer         # consumes subscription "events-default"
  database: {size: small}
  storage:   {buckets: [{name: assets}]}
  messaging: {enabled: true}   # default topic "events" + subscription "events-default"
  secrets:   {names: [signing-key]}
environments:
  awsdev: {}
  gcpdev:
    cloud: {provider: gcp, region: europe-north1, account: example-project-1234}
    state: {bucket: example-hermes-state-gcp}
  alidev:
    cloud: {provider: alibaba, region: eu-central-1, account: "5000000000000001"}
    state: {bucket: example-hermes-state-ali}
extensions:
  alibaba:
    kms_instance_id: kst-example-placeholder      # Alibaba secrets live in a KMS 3.0 instance
    state_tablestore_endpoint: https://example-hermes-lock.eu-central-1.ots.aliyuncs.com
    state_tablestore_table: hermes_state_lock
```

Overlays merge maps and replace lists. They cannot change `project.name`, `project.environment` or
`protected_environments`. `extensions.<provider>` is passed only to that provider's composition. In the rendered
roots below, `awsdev` and `gcpdev` receive `extensions = {}`.

## 2. Validate every environment

```bash
hermes validate -c examples/multi-cloud/hermes.yaml
```

```text
environment alidev (alibaba): valid
  warning HCF210: compute.otel_sidecar is requested by the spec but not implemented on Alibaba Cloud: no collector sidecar
  warning HCF210: database.iam_auth is requested by the spec but not implemented on Alibaba Cloud: password authentication (HERMES_DB_AUTH=password); the account is created out-of-band
  warning HCF210: secrets.native_injection is requested by the spec but not implemented on Alibaba Cloud: no injection into SAE; apps read secrets with their role; secrets need a KMS 3.0 instance (extensions.alibaba.kms_instance_id)
  warning HCF210 at workloads.database.point_in_time_recovery: point-in-time restore is only available on the RDS High-availability Edition (high_availability: true)
environment awsdev (aws): valid
environment gcpdev (gcp): valid
```

The spec is valid everywhere, and the differences are reported as warnings, not hidden.

## 3. Measure what differs

```bash
hermes portability -c examples/multi-cloud/hermes.yaml
```

```text
Portability of sample-portable/awsdev (13 requested features)
PROVIDER  SCORE  IMPLEMENTED  PARTIAL  NOT-IMPLEMENTED  NOTES
alibaba   65.4   7            3        3
aws *     100.0  13           0        0
gcp       100.0  13           0        0

Differences that matter:
  compute.otel_sidecar       alibaba not-implemented
  database.iam_auth          alibaba not-implemented (password authentication)
  database.pitr              alibaba partial (High-availability Edition only)
  identity.per_service       alibaba partial (RAM role binding to SAE is manual)
  observability.tracing      alibaba partial (endpoint from extensions.alibaba.otlp_endpoint)
  secrets.native_injection   alibaba not-implemented (apps read secrets with their role)
```

(The "differences" section is condensed here; the real output prints each provider's note.) The Alibaba score is
100 × (7 + 3 × 0.5) / 13 = 65.4.

## 4. Render one root per environment

```bash
for env in awsdev gcpdev alidev; do hermes render -c examples/multi-cloud/hermes.yaml --env $env; done
```

```text
Rendered .hermes/build/awsdev/main.tf.json (awsdev, aws, engine tofu)
  state   s3://example-hermes-state-aws/hermes/sample-portable/awsdev/aws/terraform.tfstate
Rendered .hermes/build/gcpdev/main.tf.json (gcpdev, gcp, engine tofu)
  state   gs://example-hermes-state-gcp/hermes/sample-portable/gcpdev/gcp/default.tfstate
Rendered .hermes/build/alidev/main.tf.json (alidev, alibaba, engine tofu)
  state   oss://example-hermes-state-ali/hermes/sample-portable/alidev/alibaba/terraform.tfstate
```

What each rendered root contains:

| | `awsdev` | `gcpdev` | `alidev` |
|---|---|---|---|
| Composition | `providers/aws/platform` | `providers/gcp/platform` | `providers/alibaba/platform` |
| Provider blocks | `aws` (`allowed_account_ids`, `default_tags`) | `google` + `google-beta` (`project`, `default_labels`) | `alicloud` (`region` only; the composition tags each resource) |
| Backend | `s3` (`key`, `encrypt`, `use_lockfile`) | `gcs` (`prefix`) | `oss` (`prefix`, `key`, `encrypt`, `tablestore_endpoint`, `tablestore_table`) |

All three roots share the same embedded module set (`.hermes/modules/<hermes-version>/`, digest `f1ad7c96005b`).

## 5. State isolation

Each environment has its own state object under `<prefix>/<project>/<environment>/<provider>/`
([ADR 0008](../decisions/0008-state-isolation-and-backends.md)), in its own cloud's bucket:

```bash
for env in awsdev gcpdev alidev; do hermes state show -c examples/multi-cloud/hermes.yaml --env $env; done
```

```text
environment awsdev
  backend:    s3
  location:   s3://example-hermes-state-aws/hermes/sample-portable/awsdev/aws/terraform.tfstate
  locking:    S3 native lockfile (use_lockfile)
  encryption: backend server-side encryption
environment gcpdev
  backend:    gcs
  location:   gs://example-hermes-state-gcp/hermes/sample-portable/gcpdev/gcp/default.tfstate
  locking:    GCS native locking
  encryption: backend server-side encryption
environment alidev
  backend:    oss
  location:   oss://example-hermes-state-ali/hermes/sample-portable/alidev/alibaba/terraform.tfstate
  locking:    Tablestore table hermes_state_lock
  encryption: backend server-side encryption
```

The GCS backend stores `<prefix>/default.tfstate` (its workspace naming), not `terraform.tfstate`. A plan or apply
for one environment can never read or lock another environment's state.

## 6. Planning and applying (not run here)

Each environment is planned and applied separately, with that cloud's credentials:

```bash
hermes plan  -c examples/multi-cloud/hermes.yaml --env gcpdev     # GCP credentials (ADC), project from cloud.account
hermes apply -c examples/multi-cloud/hermes.yaml --env gcpdev
```

Every gate from the [quickstart](quickstart.md#8-what-apply-will-do-and-how-it-refuses) applies per environment.
Saved plans live in `.hermes/plans/<env>/`, and `apply` also checks the environment recorded in the plan's
`meta.json` (`HCF511`), so a plan for `awsdev` can never be applied to `gcpdev`.
Before planning, each state bucket must exist (`hermes state bootstrap --env <env>`). On Alibaba, the bootstrap
also creates the Tablestore lock table.

## 7. What the application sees

The service receives the same variable **names** on every cloud ([binding contract](../architecture/data-contracts.md#application-binding-contract-environment-variables)):

| Variable | AWS | GCP | Alibaba |
|---|---|---|---|
| `HERMES_PROVIDER` | `aws` | `gcp` | `alibaba` |
| `HERMES_BUCKET_ASSETS` | S3 bucket name | GCS bucket name | OSS bucket name |
| `HERMES_TOPIC_EVENTS` | SNS topic ARN | Pub/Sub topic resource name | MNS topic name |
| `HERMES_SUBSCRIPTION_EVENTS_DEFAULT` (consumer) | SQS queue URL | Pub/Sub subscription name | MNS queue name |
| `HERMES_DB_AUTH` | `iam` | `iam` | `password` |
| `SIGNING_KEY` (secret) | injected by ECS from Secrets Manager | injected by Cloud Run from Secret Manager | **not injected**: read it from KMS Secrets Manager with the service's role |

Code that branches on anything other than `HERMES_DB_AUTH` and secret retrieval is a portability leak. See
[application portability](../architecture/application-portability.md).

## 8. Things that differ and that you must handle

- **Public HTTPS:** GCP gives `run.app` HTTPS with no configuration. AWS and Alibaba need a certificate extension
  (`acm_certificate_arn`, `certificate_id`), otherwise they serve plain HTTP, which is rejected in protected
  environments.
- **Database users:** on AWS, create user `app` with the `rds_iam` role. On GCP, GRANT the per-service IAM users.
  On Alibaba, create the `app` account and store its credentials out-of-band. See each provider guide's
  post-provision steps.
- **Alibaba manual steps:** RDS→KMS authorization, RAM role binding to SAE, alert e-mail activation
  ([providers/alibaba.md](../providers/alibaba.md#post-provision-steps-manual-documented-not-automated)).
- **Cost shape:** AWS has one shared ALB, GCP has no load balancer, and Alibaba has one CLB per public service plus a
  KMS instance. See [cost and sizing](../operations/cost-and-sizing.md).

## Status

Renders and validates offline for all three providers. The compositions pass their mock-provider tests. **Not
deployed to any real account.**
