# ADR 0006 — Application portability through a binding contract and open standards

- Status: Accepted (2026-11-18)

## Context

Portable infrastructure does not make applications portable. An application that calls the S3 API, reads SQS,
and fetches secrets with the AWS SDK is still bound to AWS. Hermes must not assume an application language, and
must not impose a heavyweight runtime.

## Decision

1. **Binding contract (language-independent).** Every service receives the same environment variable *names* on
   every provider. Examples: `HERMES_BUCKET_<NAME>`, `HERMES_TOPIC_<NAME>`, `HERMES_SUBSCRIPTION_<NAME>`,
   `HERMES_DB_*`, `OTEL_*`. The full list is in `docs/architecture/data-contracts.md` §2. Applications look
   resources up by role, never by provider resource name.
2. **Open standards at the interfaces:**
   - OCI images for packaging.
   - PostgreSQL wire protocol (with PostGIS) for relational data.
   - OpenTelemetry (OTLP to a local endpoint) for traces, metrics and logs.
   - CloudEvents as the recommended message envelope.
   - OIDC for human and service authentication.
   - Native workload identity (IAM role, service account, RAM role) for credentials, so no static keys.
3. **Thin adapters in the application, not a mandatory SDK.** Each team keeps a small port/adapter per
   capability: object store, publisher, subscriber, secret reader. These are implemented with the native SDK
   behind an interface, or with a portable library where one is mature (for example Go CDK `gocloud.dev` for
   blob and pubsub on AWS/GCP). Hermes documents the pattern rather than shipping SDKs in every language.
4. **Native capabilities stay available.** Teams may use provider-specific features deliberately, behind their
   adapter, and the documentation names where this is required. Examples: Pub/Sub ordering keys, S3 Object Lock,
   and IAM database authentication.

## Alternatives considered

- **Dapr as the mandatory portability runtime**: it is language-agnostic and mature, but it adds a sidecar and a
  control plane. Its Alibaba coverage is incomplete: there is no maintained pub/sub component for MNS [§9]. Dapr
  remains a supported option for teams already running it.
- **S3-compatible API everywhere**: GCS and OSS offer S3-compatible endpoints, but they need HMAC/static keys and
  have semantic gaps. This is documented as an option for legacy code, not the default.
- **A Hermes SDK per language**: this is a large maintenance burden and would become a lowest common denominator.

## Consequences

- The contract tests assert identical environment variable names across providers.
- Message semantics still differ: SQS visibility timeouts versus Pub/Sub acknowledgement deadlines, and ordering
  and duplicates. The portability guide tells applications to be idempotent, tolerate redelivery, and avoid
  depending on ordering unless they use a provider extension.
