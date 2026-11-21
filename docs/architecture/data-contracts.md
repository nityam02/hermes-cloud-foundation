# Hermes data contracts (v1alpha1)

This document is the single source of truth for every machine-readable document that
Hermes reads or writes. Code (Go structs, JSON Schemas under `schemas/`) and data files
(`migration/`, `examples/`) MUST conform to it. Changes to a contract require an update to
this document, the JSON Schema, and the contract tests in the same change.

All documents share the envelope:

```yaml
apiVersion: hermes/v1alpha1   # required in generated documents; optional (defaulted, with a warning) in user-authored Platform specs
kind: <Kind>
```

| Kind | Author | File (convention) | JSON Schema |
|---|---|---|---|
| `Platform` | developer | `hermes.yaml` | `schemas/platform/v1alpha1.schema.json` |
| `Inventory` | `hermes inventory` | `inventory.json` | `schemas/inventory/v1alpha1.schema.json` |
| `ServiceCatalog` | maintainers | `migration/mappings/catalog.yaml` | `schemas/migration/catalog.schema.json` |
| `MigrationWorkflow` | maintainers | `migration/workflows/*.yaml` | `schemas/migration/workflow.schema.json` |
| `PricingAssumptions` | maintainers / user override | `migration/pricing/assumptions.yaml` | `schemas/migration/pricing.schema.json` |
| `Assessment` | `hermes migrate assess` | `assessment.json` (+ `.md`) | `schemas/migration/assessment.schema.json` |
| `MigrationPlan` | `hermes migrate plan` | `migration-plan.yaml` (+ `.md`) | `schemas/migration/plan.schema.json` |
| `ValidationChecks` / `ValidationReport` | user / `hermes migrate validate` | `checks.yaml` / `validation-report.json` | `schemas/migration/validation.schema.json` |

---

## 1. `Platform` — the cloud-independent specification

Design rules:

* The spec describes **capabilities**, never provider resource types.
* Provider-specific needs go in `extensions.<provider>` and are only read by that provider adapter.
* Lists replace, maps deep-merge when environment overlays are applied.
* Secret **values** never appear in the spec — only secret *names*.
* Unknown fields are errors (`additionalProperties: false`), except inside `extensions.*` values
  (validated by the provider adapter) and `labels`/`env` maps.

```yaml
apiVersion: hermes/v1alpha1          # optional; defaults to hermes/v1alpha1 with a warning
kind: Platform                       # optional; defaults to Platform

project:
  name: sample-logistics-platform    # ^[a-z][a-z0-9-]{1,28}[a-z0-9]$  (3–30 chars)
  environment: dev                   # default environment when --env is not given; ^[a-z][a-z0-9]{1,11}$
  owner: platform-team               # optional, free text (becomes a label)
  labels: {cost-center: logistics}   # optional; keys ^[a-z][a-z0-9_-]{0,62}$, values ≤63 chars [a-z0-9_-]
  protected_environments: [production]   # default [production]; see "Protected environments" below

cloud:
  provider: aws                      # aws | gcp | alibaba   (required)
  region: eu-north-1                 # required; validated per provider
  account: "123456789012"            # AWS account ID / GCP project ID / Alibaba Cloud account ID.
                                     #   gcp: required (or GOOGLE_CLOUD_PROJECT at plan time).
                                     #   aws: used as allowed_account_ids guard (strongly recommended; required for protected envs).
                                     #   alibaba: recorded as label/guard where supported.

engine:                              # optional
  binary: auto                       # auto | tofu | terraform   (auto prefers tofu, falls back to terraform)

state:                               # optional; per provider defaults
  backend: auto                      # auto | s3 | gcs | oss | local   (auto => aws:s3, gcp:gcs, alibaba:oss)
                                     #   local is rejected for protected environments.
  bucket: example-hermes-state       # required unless backend=local
  prefix: hermes                     # optional; key = <prefix>/<project>/<environment>/<provider>/terraform.tfstate
  region: eu-north-1                 # optional; defaults to cloud.region
  lock_table: ""                     # aws only, legacy DynamoDB locking; default uses S3 native lockfile
  encryption:
    client_side: false               # OpenTofu-only client-side state encryption (engine must be tofu).
                                     #   Key providers exist for aws_kms and gcp_kms only; alibaba → validation error.
    kms_key: ""                      # key id/ARN/resource name for client-side state encryption

network:                             # optional; defaults shown
  cidr: 10.20.0.0/16
  zones: 2                           # 2 | 3
  nat: true                          # private egress via NAT

security:                            # optional
  customer_managed_keys: null        # null => true in protected environments, false otherwise

workloads:
  compute:
    type: containerized              # containerized | kubernetes | none
                                     #   kubernetes is RESERVED in v1alpha1 → validation error "not implemented"
    services:                        # optional; [] allowed (creates the container runtime only)
      - name: api                    # ^[a-z][a-z0-9-]{0,18}[a-z0-9]$ (2–20 chars), unique
        image: ghcr.io/example/api:1.4.2   # tag or @sha256 digest; ":latest"/untagged → error in protected envs, warning elsewhere
        port: 8080                   # 1–65535
        cpu: 0.5                     # vCPU: 0.25 | 0.5 | 1 | 2 | 4
        memory_mb: 1024              # 512–16384; provider adapter validates cpu/memory combinations
        min_instances: 1             # ≥0 (0 = scale to zero where supported)
        max_instances: 3             # ≥ min_instances, ≤ 100
        public: false                # public HTTPS ingress
        health_check_path: /healthz
        command: []                  # optional entrypoint override
        env: {LOG_LEVEL: info}       # non-secret env vars; keys ^[A-Z_][A-Z0-9_]*$; must not start with HERMES_ or OTEL_
        secret_env:                  # ENV_NAME -> secret name from workloads.secrets.names
          DATABASE_PASSWORD: db-password
        access:                      # least-privilege grants; default: nothing
          buckets: {artifacts: read-write, images: read}   # read | read-write
          publish: [telemetry]       # topic names
          consume: [telemetry-ingest]   # subscription names
          database: true             # network + auth path to the database

  database:                          # presence of the block implies enabled: true unless enabled: false
    enabled: true
    engine: postgresql               # postgresql (implemented) | mysql (RESERVED → "not implemented")
    version: "16"                    # major version string
    size: small                      # small | medium | large | xlarge (provider adapters map to instance classes)
    storage_gb: 20                   # ≥10
    max_storage_gb: 100              # ≥ storage_gb (autoscaling ceiling where supported)
    high_availability: false
    backup_retention_days: 7         # 1–35
    point_in_time_recovery: true
    deletion_protection: null        # null => true in protected environments, false otherwise
    extensions: [postgis]            # requested PostgreSQL extensions (adapter validates support)

  storage:
    enabled: true
    buckets:                         # default when enabled and omitted: [{name: data}]
      - name: artifacts              # same pattern as service names
        versioning: true             # default true
        retention_days: 0            # 0 = keep forever; >0 = expire current objects after N days

  messaging:
    enabled: true
    topics:                          # default when enabled and omitted: [{name: events, subscriptions: [{name: events-default}]}]
      - name: telemetry
        retention_hours: 168         # 1–168 (portable maximum: Pub/Sub subscriptions and MNS queues keep ≤ 7 days)
        subscriptions:
          - name: telemetry-ingest   # unique across all topics
            ack_deadline_seconds: 30 # 10–600
            max_delivery_attempts: 5 # 0 = no dead-letter; otherwise 5–100 (Pub/Sub bounds), dead-letter after N attempts

  secrets:
    enabled: true
    names: [db-password, maps-api-key]   # secret containers only; values are set out-of-band

  observability:
    enabled: true
    log_retention_days: 30           # 1–3650
    metrics: true
    tracing: true                    # injects an OpenTelemetry Collector sidecar / OTLP endpoint
    alert_email: ""                  # optional notification target

  dns:
    enabled: false                   # RESERVED in v1alpha1: true → validation error "not implemented"

environments:                        # optional per-environment overlays (deep-merged onto the base)
  dev:
    workloads: {database: {high_availability: false}}
  production:
    network: {zones: 3}
    workloads: {database: {high_availability: true, size: medium}}

extensions:                          # provider-specific escape hatches, read only by that adapter
  aws:
    acm_certificate_arn: ""          # required for public services in protected environments
  gcp: {}
  alibaba:
    certificate_id: ""
```

The developer's minimal example from the project brief is valid as written:

```yaml
project: {name: sample-logistics-platform, environment: production}
cloud: {provider: gcp, region: europe-north1}
workloads:
  compute: {type: containerized}
  database: {engine: postgresql, high_availability: true}
  storage: {enabled: true}
  messaging: {enabled: true}
  secrets: {enabled: true}
  observability: {enabled: true}
```

(It validates with warnings: no `apiVersion`, no `cloud.account` — which becomes an **error at
plan time** for GCP unless `GOOGLE_CLOUD_PROJECT` is set — and no `state.bucket`.)

### Protected environments

An environment listed in `project.protected_environments` (default `[production]`):

* rejects `state.backend: local`;
* requires `cloud.account` (AWS/GCP);
* defaults `deletion_protection` and `customer_managed_keys` to `true`;
* rejects `:latest`/untagged images;
* requires `hermes apply --approve <plan-id-prefix>` and never auto-approves;
* requires `hermes destroy --approve <plan-id-prefix> --confirm-environment <env>`.

### Normalization

`hermes` resolves an environment by: load file → apply defaults → deep-merge
`environments.<env>` → apply environment-dependent defaults (protected rules) → validate →
produce the **normalized spec**, which is what the renderer consumes (`hermes render --print`
shows it). The normalized spec always has every field populated.

---

## 2. Provider module contract (normalized spec → HCL)

Every provider composition module `infra/providers/<provider>/platform` declares **exactly**
the variables in `infra/contract/variables.tf` (copied verbatim) and **exactly** the outputs
listed in `infra/contract/outputs.md`. The renderer passes the normalized spec as those
variables. Contract tests enforce this (`tests/contracts`).

### Application binding contract (environment variables)

The composition injects these into **every** service, identically on every provider. Applications
depend on these names — never on provider resource names.

| Variable | Value |
|---|---|
| `HERMES_PROVIDER` | `aws` \| `gcp` \| `alibaba` |
| `HERMES_REGION` | cloud region |
| `HERMES_PROJECT`, `HERMES_ENVIRONMENT`, `HERMES_SERVICE` | identifiers |
| `HERMES_BUCKET_<NAME>` | native bucket name for each bucket the service has access to (`<NAME>` = upper-case, `-`→`_`) |
| `HERMES_TOPIC_<NAME>` | native topic identifier (ARN / full resource name / topic name) for each topic in `access.publish` |
| `HERMES_SUBSCRIPTION_<NAME>` | native subscription identifier (SQS queue URL / full subscription name / MNS queue name) for each in `access.consume` |
| `HERMES_DB_HOST`, `HERMES_DB_PORT`, `HERMES_DB_NAME`, `HERMES_DB_USER`, `HERMES_DB_AUTH` | when `access.database: true`; `HERMES_DB_AUTH` is `iam` or `password` |
| `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_PROTOCOL` | when observability is enabled |
| secrets in `secret_env` | injected by the native secret mechanism (never as plaintext in task/service definitions) |

### Native naming convention

`<project>-<environment>-<name>`; when a provider limit would be exceeded, truncate and append
`-<6 hex chars of sha1(full name)>`. Globally-unique names (buckets) append a stable
`random_id` suffix. Every resource carries labels/tags:
`hermes-project`, `hermes-environment`, `hermes-managed-by=hermes`, `hermes-component`, plus `project.labels`.

---

## 3. `Inventory` — discovery output (provider-neutral envelope, provider-specific types)

Discovery is **read-only, agentless and independent of any IaC state**. Terraform state,
CloudFormation stacks and tags are used only to *annotate* `managed_by`, never as the source
of the inventory.

```yaml
apiVersion: hermes/v1alpha1
kind: Inventory
metadata:
  provider: aws
  generated_at: "2026-11-18T10:00:00Z"
  generator: hermes/0.1.0
  collector: aws-api              # aws-api | aws-config | resource-explorer | fixture
  account_id: "123456789012"      # replaced by "REDACTED" with --redact
  regions: [eu-north-1]
  read_only: true                 # always true; a writer that cannot guarantee it must refuse
  identity:                       # who performed discovery (never credentials)
    principal_arn: arn:aws:sts::123456789012:assumed-role/HermesDiscoveryReadOnly/hermes
    role_arn: arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly
    external_id_used: true
resources:
  - id: arn:aws:rds:eu-north-1:123456789012:db:orders-db   # ARN when available, else provider-unique id
    type: AWS::RDS::DBInstance    # CloudFormation / Cloud Control resource type name
    name: orders-db
    region: eu-north-1
    tags: {team: orders}
    managed_by: unmanaged         # unmanaged | terraform | cloudformation | unknown
    managed_by_evidence: ""       # e.g. "tag aws:cloudformation:stack-name=..." or "found in terraform state <file>"
    completeness: full            # full | partial | identifier-only  (partial when some describe calls were denied)
    attributes:                   # normalized, non-secret configuration
      engine: postgres
      engine_version: "15.5"
      instance_class: db.m6g.large
      multi_az: true
      allocated_storage_gb: 200
      storage_encrypted: true
      publicly_accessible: false
    size_bytes: 214748364800      # optional data-volume hint used for transfer estimates
relationships:
  - from: arn:aws:rds:eu-north-1:123456789012:db:orders-db
    to: arn:aws:ec2:eu-north-1:123456789012:vpc/vpc-0abc
    type: in-vpc                  # in-vpc | in-subnet | uses-security-group | allows-ingress-from | routes-to
                                  # | encrypted-by | assumes-role | uses-secret | publishes-to | subscribes-to
                                  # | targets | reads-from | writes-to | attached-to | depends-on
    evidence: api                 # api | inferred
gaps:                             # what could NOT be discovered or fully inspected
  - service: kinesis
    region: eu-north-1
    operation: ListStreams
    reason: access-denied         # access-denied | not-enabled | opt-in-region-disabled | throttled | unsupported | error
    message: "AccessDeniedException: ..."
    impact: "Kinesis data streams were not inventoried"
summary:
  resource_count: 42
  by_type: {"AWS::RDS::DBInstance": 1}
  gap_count: 1
```

Secrets Manager / SSM SecureString **values are never read**. Only metadata (name, ARN,
KMS key, rotation, tags) is collected. Environment variables of task definitions / Lambda
functions are collected as **names only**.

---

## 4. `ServiceCatalog` — source → target service mapping

```yaml
apiVersion: hermes/v1alpha1
kind: ServiceCatalog
metadata:
  reviewed_at: "2026-11-18"
  disclaimer: "Mappings describe capability equivalence, not operational equivalence. Verify against current provider documentation."
entries:
  - id: aws-rds-postgresql                 # unique kebab-case
    category: relational-database          # see category enum below
    capability: database                   # hermes capability: network | compute | database | storage | messaging | secrets | observability | identity | dns | none
    source:
      provider: aws
      service: "Amazon RDS for PostgreSQL"
      resource_types: ["AWS::RDS::DBInstance"]
      match:                               # optional: attribute -> allowed values (all keys must match). Entries with match are evaluated before entries without.
        engine: ["postgres"]
    targets:
      gcp:
        service: "Cloud SQL for PostgreSQL"
        alternatives: ["AlloyDB for PostgreSQL"]
        compatibility: compatible          # equivalent | compatible | partial | re-architect | none | unknown
        complexity: medium                 # low | medium | high
        data_migration: required           # required | optional | none
        workflow: relational-database-postgresql   # MigrationWorkflow id
        tooling: ["Google Database Migration Service (continuous, logical replication)", "pg_dump/pg_restore"]
        lock_in: low                       # lock-in of the SOURCE service: low | medium | high
        hermes_support: implemented        # implemented | partial | not-implemented  (can Hermes provision the target?)
        differences: ["..."]
        issues: ["..."]
        references: ["https://cloud.google.com/database-migration/docs/postgres/overview"]
      alibaba: { ... same shape ... }
```

Category enum: `compute, container-orchestration, serverless-container, kubernetes,
serverless-function, container-registry, object-storage, block-storage, file-storage,
relational-database, nosql-database, cache, analytics, streaming, messaging-queue,
messaging-pubsub, iot, geospatial, time-series, networking, hybrid-connectivity,
load-balancing, cdn, dns, identity, secrets, key-management, certificates, monitoring,
logging, tracing, api-gateway, workflow, notification, backup, iac, other`.

**`match` semantics.** Each key names a field of `resources[].attributes`. The attribute value is converted to a
string (booleans become `"true"`/`"false"`, numbers use their shortest decimal form) and compared
case-insensitively against the listed values. An entry matches when **every** key matches one of its values.
When several entries share a resource type, entries with `match` are evaluated before entries without one, and
among those the entry with the most keys wins. Ties are an error at catalog load. The attribute keys that
collectors MUST emit for matching are listed in `docs/migration/service-mapping.md`. Examples: ELBv2 `type`,
RDS `engine`, ElastiCache `engine`, SQS `fifo_queue`, EC2 `source_dest_check`.

Resources whose type is not in the catalog are classified `category: other`,
`compatibility: unknown`, workflow `manual-review`, and flagged in the assessment.

---

## 5. `MigrationWorkflow` — reusable migration step templates

```yaml
apiVersion: hermes/v1alpha1
kind: MigrationWorkflow
metadata:
  id: relational-database-postgresql
  title: "PostgreSQL migration with continuous replication"
  applies_to: [relational-database]
spec:
  steps:
    - id: enable-logical-replication
      phase: prepare          # foundation | prepare | provision | synchronize | validate | cutover | monitor | decommission
      title: "Enable logical replication on the source"
      description: "..."
      automation: manual      # manual | hermes | provider-tool
      gate: none              # none | approval | validation
      reversible: true
      rollback: "Revert parameter group change; requires instance reboot."
      risks: ["Parameter change requires a reboot of the source instance"]
      validation: ["rds.logical_replication = 1 on source"]
      source_impact: modifies-source-config   # none | read-only-load | modifies-source-config | modifies-source-data
```

`source_impact` makes every step that touches the source explicit. `modifies-source-data` covers steps that
change source data or its flow, such as draining queues, enabling dual writes, or making the source read-only.
Any step other than `none` or `read-only-load` must have `gate: approval`. `applies_to: [other]` marks
workflows that apply to any category (for example `manual-review`).

Reserved workflow ids: `foundation`, `hybrid-connectivity`, `stateless-rebuild`,
`container-workload`, `relational-database-postgresql`, `relational-database-mysql`,
`object-storage`, `messaging`, `secrets`, `nosql-database`, `traffic-cutover`,
`decommission`, `manual-review`.

---

## 6. `PricingAssumptions` — cost estimation inputs (never presented as facts)

```yaml
apiVersion: hermes/v1alpha1
kind: PricingAssumptions
metadata:
  retrieved_at: "2026-11-18"
  disclaimer: "Indicative list prices for estimation only; verify with the provider's current pricing and your agreements."
rates:
  - id: aws-internet-egress-standard
    provider: aws
    kind: egress-internet        # egress-internet | egress-inter-region | transfer-service | replication-service
    usd_per_gb: 0.09
    applies_to: "First 10 TB/month, most regions"
    source: "https://aws.amazon.com/ec2/pricing/on-demand/"
notes:
  - "..."
```

---

## 7. `Assessment` and `MigrationPlan`

`Assessment` (produced from an `Inventory` + `ServiceCatalog` + target provider) contains:
`metadata` (source, target, inventory digest, catalog digest, generated_at), `summary`
(resource counts by compatibility/complexity, **portability score** = weighted share of
resources whose mapping is equivalent/compatible and `hermes_support: implemented`,
data volume, estimated transfer cost range with the rates used), `resources[]` (id, type,
category, mapping entry id, compatibility, complexity, data_migration, workflow, hermes_support,
notes), `blockers[]`, `gaps[]` (copied from inventory: undiscovered scope is a risk),
`dependencies` (graph-derived migration waves), and `draft_spec` (a `Platform` document
for the target provider generated from the inventory, always requiring human review).

`MigrationPlan` contains `metadata.plan_id` (sha256 of the canonical plan body), source/target,
inventory/assessment digests, and ordered `phases[]` → `steps[]` (instantiated from workflows,
each with `id`, `resource_refs`, `automation`, `gate`, `source_impact`, `reversible`,
`rollback`, `validation`, `depends_on`). It never contains credentials.
