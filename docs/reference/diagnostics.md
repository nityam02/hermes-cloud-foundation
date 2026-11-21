# Diagnostic codes

Every finding has a stable code, a severity (`error`, `warning`, `info`), the dotted spec path
and, when it comes from the spec file, `file:line`. With `-o json` diagnostics are objects:
`{"severity", "code", "path", "file", "line", "message", "hint"}`. Errors make the command exit
with code 2. Ranges: `HCF1xx` configuration, `HCF2xx` provider adapter, `HCF3xx` execution and
policy, `HCF4xx` migration (see docs/migration), `HCF5xx` safety gates.

## HCF1xx — configuration (`internal/config`, `internal/schema`)

| Code | Severity | Meaning |
|---|---|---|
| HCF100 | error | YAML syntax error, duplicate key, merge key, anchor or alias (`&name`/`*name`), spec larger than 1 MiB, or the document is not a mapping |
| HCF101 | error | JSON Schema violation (unknown field, type, pattern, range, enum) |
| HCF102 | warning | `apiVersion` missing; `hermes/v1alpha1` assumed |
| HCF103 | error | no environment selected, invalid environment name, or environment not declared |
| HCF104 | error | duplicate service, bucket, topic, subscription or secret name |
| HCF105 | error | `secret_env` refers to a secret not declared (or secrets disabled) |
| HCF106 | error | `access.buckets` refers to an undeclared bucket (or storage disabled) |
| HCF107 | error | `access.publish` refers to an undeclared topic (or messaging disabled) |
| HCF108 | error | `access.consume` refers to an undeclared subscription |
| HCF109 | error | `access.database: true` but the database is disabled |
| HCF110 | error | `env`/`secret_env` key uses the reserved `HERMES_`/`OTEL_` prefix, or is in both maps |
| HCF111 | error | reserved feature: `compute.type: kubernetes`, `database.engine: mysql`, `dns.enabled: true` — not implemented in v1alpha1 |
| HCF112 | error | `state.backend: local` in a protected environment |
| HCF113 | warning | `cloud.account` missing (AWS guard, GCP project, protected environments) |
| HCF114 | error / warning | image is `:latest` or untagged (error when protected) |
| HCF115 | error | range rules: `max_instances < min_instances`, `max_storage_gb < storage_gb`, services with `compute.type: none` |
| HCF116 | warning | `state.bucket` missing (required at plan time for remote backends) |
| HCF117 | error / warning | state configuration: `lock_table` without s3, client-side encryption without `kms_key` or with Terraform; unusual backend for the provider |
| HCF118 | error / warning | `network.cidr` invalid, smaller than /20, host bits set; non-private range (warning) |
| HCF119 | warning | settings ignored because the capability is disabled |
| HCF120 | error / warning | PostgreSQL major version past (error) or near (warning) community end-of-life, or unknown |
| HCF121 | error | a name in `project.protected_environments` is not a declared environment (`environments` keys or `project.environment`), so it would protect nothing |
| HCF122 | warning | a declared environment looks like production (`^(prod\|prd\|production\|live\|prod[0-9a-z]*)$`) but is not in `project.protected_environments` |

## HCF2xx — provider adapters (`internal/providers`)

| Code | Severity | Meaning |
|---|---|---|
| HCF200 | warning | region not in the provider's known list |
| HCF201 | error | CPU/memory combination not supported (Fargate) or database size without a class (Alibaba Basic edition xlarge) |
| HCF202 | warning | the provider rounds a value: Cloud Run / SAE size, SAE minimum instances, RDS backup retention, CloudWatch log retention |
| HCF203 | error / warning | unknown `extensions.<provider>` key (error where the composition rejects unknown keys: aws, gcp) |
| HCF204 | error | invalid extension value (type, pattern), or a required extension is missing (`kms_instance_id` for Alibaba secrets) |
| HCF205 | error | `cloud.account` has the wrong format for the provider |
| HCF206 | error | plan time: account/project required (protected environments; GCP without `GOOGLE_CLOUD_PROJECT`) |
| HCF207 | warning / error | public service in a protected environment without an HTTPS certificate extension (warning at validate, error at plan: the environment cannot be planned) |
| HCF208 | error | client-side state encryption requested on Alibaba Cloud (no OpenTofu key provider) |
| HCF209 | error / warning | PostgreSQL extension unavailable (error) or unknown (warning) on the provider |
| HCF210 | warning / info | the spec relies on a capability that is not implemented (warning) or partial (info) on the provider, or a provider-specific caveat (PITR, NAT) |
| HCF211 | warning | Cloud Run consumer without always-allocated CPU |
| HCF212 | error / warning | state: bucket missing at plan time; OSS backend without Tablestore locking (warning at validate; error at plan when protected) |

## HCF3xx — execution and policy (`internal/policy`)

| Code | Severity | Meaning |
|---|---|---|
| HCF301 | warning / error | conftest not installed (error in protected environments unless `--skip-policy`) |
| HCF302 | warning / error | this build bundles no plan policies and no `--policy-dir` was given |
| HCF303 | error | a policy denied the plan (`deny` rule); the plan is saved but cannot be applied |
| HCF304 | warning | a policy warning (`warn` rule) |
| HCF305 | warning | the policy check was skipped with `--skip-policy` (recorded in history) |
| HCF306 | warning | a policy exception in a `--policy-data` file has expired and is ignored |

Engine failures (init/plan/apply errors) are reported with the engine's own message and exit 1.

## HCF5xx — safety gates (`internal/execution/gates.go`)

Printed as `Refused: HCF5xx: reason (hint)`, exit code 2, recorded in `.hermes/history/<env>.jsonl`.

| Code | Gate |
|---|---|
| HCF501 | no saved plan for the environment |
| HCF502 | stale plan: the rendered root or the modules changed since planning |
| HCF503 | plan older than `--max-plan-age` (default 24h), or `created_at` more than 5 minutes in the future |
| HCF504 | plan file missing or not matching its plan id (modified after planning) |
| HCF505 | plan deletes or replaces resources and `--allow-destructive` was not given |
| HCF506 | approval missing, shorter than 8 characters, or not a prefix of the plan id |
| HCF507 | protected destroy without a matching `--confirm-environment` |
| HCF508 | the policy check failed for this plan |
| HCF509 | not confirmed: declined at the prompt, or non-interactive without `--yes`/`--approve` |
| HCF510 | the plan was already applied |
| HCF511 | wrong plan: different kind (apply vs destroy) or environment |
| HCF512 | the policy check could not run in a protected environment |

## HCF4xx — migration and discovery (`internal/migration`, `internal/inventory`)

These codes appear as findings in `hermes migrate assess` output (`assessment.json` → `blockers[]` and resource
notes). They describe risks of the **source estate**, not errors in the CLI invocation, and use the assessment's own
severity scale: `blocker` (must be resolved before migrating that resource), `risk`, or `info`. See
[`docs/migration/migration-commands.md`](../migration/migration-commands.md) for how each is produced.

| Code | Assessment severity | Meaning |
|---|---|---|
| HCF401 | risk | Resource type is not in the service catalog (classified `other`, workflow `manual-review`) |
| HCF402 | blocker | The catalog has no target service for the selected target provider |
| HCF403 | risk | The mapping requires re-architecture (`compatibility: re-architect`) |
| HCF404 | risk | Discovery recorded gaps: part of the estate could not be inventoried or inspected |
| HCF405 | risk | Resource was only partially inspected (`completeness: partial` or `identifier-only`) |
| HCF406 | info | The source service has high lock-in |
| HCF407 | risk | The resource holds data but its size is unknown, so the transfer estimate is incomplete |
| HCF408 | risk or blocker (per finding) | Resource-level risk: public database, internet-open security group, FIFO queue semantics, network appliance or VPN relay, or a non-PostgreSQL database engine |
