# Application portability (layer E)

Hermes makes the **infrastructure** description portable. Applications stay portable only if
they depend on stable contracts instead of provider names. This page states those contracts,
what differs between providers, and how to measure it. Status: the contracts are
implemented in the three compositions and checked by the shared contract test
(`tests/contracts`, mock providers); no application has been run on a real cloud here.

## 1. The binding contract: environment variables

Every service receives the same variable **names** on every provider (data-contracts §2);
only values differ. `hermes bindings --env <env> -o env --service <name>` prints them.

| Variable | aws | gcp | alibaba |
|---|---|---|---|
| `HERMES_PROVIDER`, `HERMES_REGION` | `aws`, region | `gcp`, region | `alibaba`, region |
| `HERMES_PROJECT`, `HERMES_ENVIRONMENT`, `HERMES_SERVICE` | identifiers | identifiers | identifiers |
| `HERMES_BUCKET_<NAME>` | S3 bucket name | GCS bucket name | OSS bucket name |
| `HERMES_TOPIC_<NAME>` | SNS topic ARN | `projects/…/topics/…` | MNS topic name |
| `HERMES_SUBSCRIPTION_<NAME>` | SQS queue URL | `projects/…/subscriptions/…` | MNS queue name |
| `HERMES_DB_HOST/PORT/NAME/USER` | RDS endpoint, 5432, `app`, `app` | Cloud SQL private IP, 5432, `app`, per-service IAM user | RDS endpoint, 5432, `app`, `app` |
| `HERMES_DB_AUTH` | `iam` | `iam` | `password` |
| `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_PROTOCOL` | sidecar `http://localhost:4317`, `grpc` | sidecar `http://localhost:4317`, `grpc` | `extensions.alibaba.otlp_endpoint` (no sidecar) |

`<NAME>` is the spec name upper-cased with `-` → `_`. Variables appear only for what the
service's `access` block grants: a service that does not list a bucket neither sees its name
nor has permission to it (least privilege per service identity).

Rules for application code:

1. Read names and endpoints **only** from these variables; never build provider resource
   names yourself (native names are hashed and suffixed, data-contracts §2).
2. Select the provider client by `HERMES_PROVIDER` behind a small port in your code
   (`BlobStore`, `Publisher`, `Consumer`), with one adapter per cloud. Keep the port narrow:
   get/put/list/delete objects; publish; receive/ack with a deadline.
3. Use the platform identity (task role, service account, RAM role). No static keys:
   S3-compatible access to GCS or OSS needs HMAC/static keys, so it is not the recommended
   portability path.

## 2. Secrets

`secret_env` maps an env var to a secret **name**; values are set out-of-band and never pass
through IaC or state. AWS (ECS `secrets`) and GCP (Cloud Run `secret_key_ref`) inject the
value natively. Alibaba SAE has no native injection in the provider: the app reads the
reference from its configuration (`secret_environment` output) and fetches the value with its
RAM role from KMS Secrets Manager. Portable approach: a `SecretSource` port that reads the env
var first and falls back to the provider API.

## 3. Database: PostgreSQL

The only database engine in v1alpha1 is PostgreSQL (`mysql` is reserved). Portable code uses
plain SQL and standard drivers, requires TLS, and handles both auth modes:

* `HERMES_DB_AUTH=iam`: AWS — generate an RDS IAM auth token for `HERMES_DB_USER` (15-minute
  validity) and use it as the password; GCP — use the Cloud SQL connector or an IAM OAuth token
  for the per-service IAM user. Grants for IAM users are manual post-provision SQL.
* `HERMES_DB_AUTH=password`: Alibaba — read the credentials from the out-of-band secret
  (`extensions.alibaba.db_credentials_secret`).

Extensions are requested in the spec but installed by you (`CREATE EXTENSION postgis;`).
PostGIS, pg_trgm, pgcrypto and similar are available on all three; TimescaleDB is not on RDS
or Cloud SQL (reference architecture §5.2).

## 4. Messaging and events

Topics with durable per-subscription queues behave alike at the contract level (at-least-once
delivery, ack within `ack_deadline_seconds`, dead-letter after `max_delivery_attempts` when
> 0), but not identically:

* SQS uses a visibility timeout, Pub/Sub an ack deadline, MNS a visibility period; all may
  redeliver. Make handlers **idempotent** (dedupe on an event id).
* No ordering guarantee is part of the contract.
* Retention is capped at 7 days (the portable maximum; contract validation).
* Maximum message sizes differ widely between the three services (MNS is the smallest).
  Keep messages small and put large payloads in a bucket (claim-check pattern); verify the
  current limits in each provider's documentation.

Use **CloudEvents 1.0** (JSON structured mode) as the envelope: `id`, `source`, `type`,
`time`, `datacontenttype`. The envelope then survives a change of broker and of cloud.

## 5. Observability

Instrument with the OpenTelemetry SDK and export OTLP to `OTEL_EXPORTER_OTLP_ENDPOINT`; do not
link a vendor SDK. On AWS and GCP a collector sidecar forwards to X-Ray / Cloud Trace; on
Alibaba the endpoint comes from `extensions.alibaba.otlp_endpoint`. Log to stdout as JSON;
the platform ships logs with the configured retention.

## 6. Runtime conventions

Listen on `port`, answer `health_check_path` with 200 when ready, shut down gracefully on
SIGTERM, keep no local state (instances are replaced at any time), and expect
`min_instances: 0` to mean different things (GCP scales from zero on requests; AWS only for
queue consumers; Alibaba keeps one instance).

## 7. Measuring portability

`hermes portability` (or `hermes validate --portability`) lists the capability features your
spec relies on, the status of each on every provider, a weighted score
(implemented 1, partial 0.5, not-implemented 0) and the differences that matter, plus
provider-specific sizing findings. Example: `examples/multi-cloud` scores 100 on AWS and GCP and
65.4 on Alibaba Cloud with the current matrices (13 requested features), mostly because of
database IAM auth, secret injection, the tracing sidecar and per-service identity on Alibaba. The matrices describe the Hermes modules
(mock-tested), not real-cloud verification.

## Not portable in v1alpha1

Kubernetes, MySQL, DNS/custom domains, device-facing MQTT, WORM retention, cross-region DR
and provider-specific managed services outside the spec (see the reference architecture §4).
Use them out-of-band and treat each as a deliberate portability trade-off.
