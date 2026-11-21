# Configuration reference (`hermes.yaml`, `hermes/v1alpha1`)

The contract is [data-contracts §1](../architecture/data-contracts.md); the machine-readable
form is [`schemas/platform/v1alpha1.schema.json`](../../schemas/platform/v1alpha1.schema.json)
(JSON Schema 2020-12, embedded in the binary). This page documents how Hermes **interprets**
the spec: the pipeline, defaults, overlays and every rule it enforces.

## Pipeline

1. **Parse** (yaml.v3). Duplicate keys, YAML merge keys (`<<`), anchors and aliases (`&name`,
   `*name`) and specs larger than 1 MiB are errors (`HCF100`): an alias "bomb" of 1 KB would
   otherwise expand to gigabytes; environment overlays are the supported reuse mechanism. The
   same limits (1 MiB, no anchors/aliases) apply to every YAML file hermes reads: validation
   checks, cutover hooks, catalog/mapping/pricing overrides and `--policy-data` (migration plans,
   which hermes generates, are capped at 32 MiB). Line numbers are
   kept for every value, so diagnostics read `error HCF101 at workloads.compute.services[1].cpu (hermes.yaml:24): …`.
2. **Schema** validation of the file as written, including every overlay (`environments.<env>`)
   against the overlay schema. Unknown fields are errors, except inside `extensions.<provider>`
   (validated by the provider adapter), `labels` and `env`.
3. **Overlay**: `environments.<env>` is deep-merged onto the base: **maps merge, lists replace**,
   an explicit `null` replaces. The merged document is schema-validated again.
4. **Defaults** and **protected-environment** rules (below).
5. **Semantic** validation (cross-references, reserved features, ranges).
6. **Provider** validation (`internal/providers`): regions, sizing, extension keys, certificates.
7. **Plan-time** checks (only `hermes plan`/`destroy`): account, state bucket, GCP project,
   Alibaba state locking in protected environments.

`hermes render --print` shows the result: the **normalized spec**, with every field populated.

## Environments

* The selected environment is `--env`, else `project.environment`.
* Declared environments are `project.environment` and the keys of `environments`
  (`hermes validate` checks all of them). Any other name is an error (`HCF103`): a typo must
  never produce an unprotected environment. An empty overlay (`staging: {}`) declares an
  environment.
* Overlays may change everything except `project.name`, `project.environment` and
  `project.protected_environments` (only `owner` and `labels` under `project`).
* Environment names: `^[a-z][a-z0-9]{1,11}$`.

## Protected environments

`project.protected_environments` (default `[production]`). Protection is membership of this
list, so Hermes guards against name mismatches:

* Every name listed explicitly must be a declared environment; otherwise it protects nothing and
  the spec is invalid (`HCF121`, error). The implicit default `[production]` is exempt.
* A declared environment whose name looks like production (`prod`, `prd`, `production`, `live`,
  or `prod` followed by letters/digits such as `prodeu`) but is not in the list gets a warning
  (`HCF122`). Add it to the list, or rename it if it is not production. A custom list replaces
  the default, so `protected_environments: [live]` leaves `production` unprotected (and warned).

In a protected environment:

| Rule | Where |
|---|---|
| `state.backend: local` rejected | validate (`HCF112`) |
| `cloud.account` required (AWS, GCP) | warning at validate (`HCF113`), error at plan (`HCF206`) |
| `database.deletion_protection` and `security.customer_managed_keys` default to `true` | defaults |
| `:latest` or untagged images rejected | validate (`HCF114`) |
| public services need `extensions.aws.acm_certificate_arn` / `extensions.alibaba.certificate_id` | warning at validate, error at plan (`HCF207`) |
| OSS state needs both Tablestore keys (Alibaba) | warning at validate, error at plan (`HCF212`) |
| plan policies must run (conftest) unless `--skip-policy` (recorded) | plan (`HCF301`/`HCF302`) |
| `apply` needs `--approve <plan-id-prefix>`; `--yes` is ignored | gates (`HCF506`) |
| `destroy` needs `--approve` and `--confirm-environment <env>` | gates (`HCF506`, `HCF507`) |
| OpenTofu state encryption is rendered with `enforced = true` | render |

## Defaults

| Field | Default |
|---|---|
| `apiVersion` / `kind` | `hermes/v1alpha1` (warning `HCF102` when missing) / `Platform` |
| `project.owner`, `labels` | `""`, `{}`; a non-empty owner becomes the label `owner` (sanitized) |
| `cloud.account` | `""` |
| `engine.binary` | `auto` (OpenTofu, else Terraform) |
| `state.backend` | `auto` → `s3` (aws), `gcs` (gcp), `oss` (alibaba) |
| `state.prefix`, `state.region` | `hermes`, `cloud.region` |
| `network` | `cidr: 10.20.0.0/16`, `zones: 2`, `nat: true` |
| `security.customer_managed_keys` | `null` → protected |
| `workloads.compute.type` | `containerized` when the block exists, else `none` |
| service | `port 8080`, `cpu 0.5`, `memory_mb 1024`, `min_instances 1`, `max_instances max(3, min_instances)`, `public false`, `health_check_path /healthz`, empty `command`/`env`/`secret_env`/`access` |
| `database`, `storage`, `messaging`, `secrets` | **presence of the block implies `enabled: true`** unless `enabled: false` |
| `observability` | enabled unless `enabled: false` (logs retention is a secure default) |
| database | `engine postgresql`, `version "16"`, `size small`, `storage_gb 20`, `max_storage_gb max(100, storage_gb)`, `high_availability false`, `backup_retention_days 7`, `point_in_time_recovery true`, `deletion_protection null → protected`, `extensions []` |
| storage | buckets `[{name: data}]` when enabled and omitted; bucket `versioning true`, `retention_days 0` |
| messaging | topics `[{name: events, subscriptions: [{name: events-default}]}]` when enabled and omitted; `retention_hours 168`, `ack_deadline_seconds 30`, `max_delivery_attempts 5` |
| observability | `log_retention_days 30`, `metrics true`, `tracing true`, `alert_email ""` |
| `dns.enabled` | `false` |

Buckets, topics or secret names listed under a disabled capability are dropped with a warning
(`HCF119`).

## Semantic rules (provider-independent)

* Unique names: services, buckets, topics, secrets; subscription names are unique across all
  topics (`HCF104`).
* `secret_env` values must be declared in `workloads.secrets.names` with secrets enabled (`HCF105`);
  `access.buckets` keys must be declared buckets (`HCF106`); `access.publish` declared topics
  (`HCF107`); `access.consume` declared subscriptions (`HCF108`); `access.database` requires
  the database (`HCF109`).
* `env` and `secret_env` keys must not start with `HERMES_` or `OTEL_` (injected by the binding
  contract) and must not appear in both (`HCF110`).
* Reserved in v1alpha1 → error "not implemented": `compute.type: kubernetes`,
  `database.engine: mysql`, `dns.enabled: true` (`HCF111`).
* `max_instances ≥ min_instances`; `max_storage_gb ≥ storage_gb`; services require
  `compute.type: containerized` (`HCF115`).
* Images must be pinned (tag other than `latest`, or `@sha256:`): error when protected,
  warning elsewhere (`HCF114`).
* `network.cidr`: IPv4, `/20` or larger, no host bits; non-RFC 1918 ranges warn (`HCF118`). On AWS
  it must also be `/16` or narrower (the largest VPC AWS allows; `HCF204`).
* State: `lock_table` only with `s3`; `encryption.client_side` needs `kms_key` and OpenTofu
  (`HCF117`); a backend other than the provider default warns.
* PostgreSQL major < 14 is an error (past community end-of-life), 14 warns (`HCF120`).

## Provider rules (`internal/providers`)

| Provider | Rules |
|---|---|
| all | unknown region → warning; `cloud.account` format; PostgreSQL extension availability (`timescaledb` is unavailable on RDS and Cloud SQL) |
| aws | Fargate CPU/memory combinations (error with the valid list); CloudWatch log retention rounded up to an accepted value (warning); `point_in_time_recovery: false` has no effect (warning); `network.nat: false` leaves tasks without egress (warning) |
| gcp | Cloud Run sizing: CPU rounded up to 1/2/4/6/8 and memory to Cloud Run minimums (warning with the effective size); consumers without the tracing sidecar get CPU only during requests (warning); project from `cloud.account` or `GOOGLE_CLOUD_PROJECT` at plan time |
| alibaba | SAE sizes (min 0.5 vCPU / 1 GiB; rounded up, warning); `min_instances: 0` becomes 1; backup retention below 7 days becomes 7; PITR needs high availability; xlarge without HA needs `db_instance_type` (error); secrets need `kms_instance_id` (error); client-side state encryption unsupported (error) |

## Extension keys

Only `extensions.<cloud.provider>` is passed to the composition (as `var.extensions`).

| Provider | Keys | Unknown keys |
|---|---|---|
| aws | `acm_certificate_arn`, `alb_ingress_cidrs` (list), `cpu_architecture` (`X86_64`/`ARM64`), `db_instance_class`, `readonly_root_filesystem` (bool), `container_health_check` (bool) | error (the composition rejects them) |
| gcp | `vpc_egress` (`private-ranges-only`/`all-traffic`), `otel_collector_image` | error (the composition rejects them) |
| alibaba | `certificate_id`, `kms_instance_id`, `otlp_endpoint`, `otlp_protocol`, `zone_ids` (list), `db_instance_type`, `db_credentials_secret`, `oss_redundancy_type`, `mns_sse` (bool), `acr_instance_id`; read by hermes for the backend: `state_tablestore_endpoint`, `state_tablestore_table` | warning |

`hermes providers <name>` prints the current list with descriptions.
