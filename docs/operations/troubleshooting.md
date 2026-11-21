# Troubleshooting

Find the code or message you see, then read the cause and the fix. The full code list is in
[diagnostics](../reference/diagnostics.md). Exit codes: `0` ok, `1` unexpected or engine error, `2` validation
failed or a gate refused, `3` changes or drift present (only with `--detailed-exitcode` / `--drift`).

Every diagnostic prints `severity code at path (file:line): message (hint: …)`. Add `-o json` to get
`{"severity","code","path","file","line","message","hint"}` objects for tooling.

## Before anything else

```bash
hermes doctor                 # engine and version, tools, credential-source names, spec, .gitignore, disk
hermes validate --env <env>   # the rules plan will enforce first
hermes render --print         # the normalized spec, with every default filled in
```

## Spec and validation (HCF1xx)

| Code | Typical cause | Fix |
|---|---|---|
| HCF100 | YAML syntax error, duplicate key, `<<` merge key, YAML anchor/alias (`&name`/`*name`), a spec over 1 MiB, or the file is not a mapping | Fix the line reported; Hermes rejects merge keys, anchors/aliases and duplicates on purpose (use environment overlays for reuse) |
| HCF101 | A field is unknown, misspelled, the wrong type or out of range (JSON Schema) | Check the path against the [configuration reference](../reference/configuration.md). `got null, want object` usually means an empty block such as `state:` with nothing under it |
| HCF103 | `--env` names an environment that is not under `environments:` | Declare it (`environments: {staging: {}}`). Undeclared environments are always an error |
| HCF105–HCF109 | `secret_env`, `access.buckets`, `publish`, `consume` or `database` refer to something not declared, or to a disabled capability | Declare the secret, bucket, topic or subscription, or remove the reference |
| HCF110 | An `env` key starts with `HERMES_` or `OTEL_` | Those prefixes are reserved for the binding contract; rename the key |
| HCF111 | `compute.type: kubernetes`, `database.engine: mysql` or `dns.enabled: true` | Not implemented in v1alpha1 ([limitations](../limitations.md#2-specification-hermesv1alpha1)) |
| HCF112 | `state.backend: local` in a protected environment | Use the provider's remote backend; bootstrap it with `hermes state bootstrap` |
| HCF113 / HCF116 | `cloud.account` or `state.bucket` missing (warning at validate) | They become errors at plan time (`HCF206`, `HCF212`); set them now |
| HCF114 | Image is `:latest` or untagged (error when protected) | Pin a tag, or better, a `@sha256:` digest |
| HCF117 | `lock_table` without the s3 backend; client-side encryption without `kms_key`, or with Terraform | Fix `state.*`. Client-side encryption is OpenTofu-only |
| HCF118 | `network.cidr` invalid, smaller than /20, has host bits set, or is public (warning) | Use a private /16–/20, for example `10.20.0.0/16` |
| HCF120 | PostgreSQL major version past or near community end-of-life | Raise `database.version` |

## Provider rules (HCF2xx)

| Code | Typical cause | Fix |
|---|---|---|
| HCF200 | The region is not in Hermes' static list (warning) | Check the spelling. A new region may simply be missing; see [onboarding](../tutorials/onboarding.md#4-your-first-change-add-a-region-the-aws-adapter-does-not-know-yet) |
| HCF201 | AWS: the Fargate CPU/memory pair does not exist. Alibaba: Basic Edition `xlarge` | Use a value from the hint, or set `high_availability` / `db_instance_type` |
| HCF202 | The provider rounds a value: Cloud Run or SAE size, SAE minimum 1 instance, RDS backup ≥ 7 days on Alibaba, CloudWatch retention | Set the effective value from the hint to make it explicit |
| HCF203 | Unknown `extensions.<provider>` key (error on AWS and GCP) | Use a key from `hermes providers <name>` |
| HCF204 | Extension value has the wrong type or pattern, or is required but missing (`kms_instance_id` for Alibaba secrets) | Fix the value |
| HCF205 | `cloud.account` has the wrong format (12 digits on AWS; GCP project ID pattern; numeric on Alibaba) | Fix the account or project ID |
| HCF206 | Plan time: no account in a protected environment, or GCP without `cloud.account` and `GOOGLE_CLOUD_PROJECT` | Set `cloud.account` |
| HCF207 | A public service in a protected environment has no certificate (warning at validate, error at plan) | Set `extensions.aws.acm_certificate_arn` or `extensions.alibaba.certificate_id` before planning that environment |
| HCF208 | Client-side state encryption on Alibaba | Not supported (no OpenTofu key provider); rely on OSS server-side encryption |
| HCF209 | PostgreSQL extension unavailable (error, for example `timescaledb`) or unknown (warning) | Remove it, or verify that it is available before relying on it |
| HCF210 | The spec relies on a capability that is not implemented (warning) or partial (info) on this provider, or a caveat (AWS PITR, `nat: false`) | Read the note. See the [comparison matrix](../providers/comparison-matrix.md) |
| HCF211 | GCP: a service consumes a subscription without the tracing sidecar | Enable `observability.tracing` (CPU stays allocated), or consume via push |
| HCF212 | `state.bucket` missing at plan time; Alibaba OSS backend without Tablestore locking (warning at validate, error at plan when protected) | `hermes state bootstrap`, then set `state.bucket` (and `extensions.alibaba.state_tablestore_*`) |

Example (a real capture):

```text
$ hermes plan -c nobucket.yaml
error HCF212 at state.bucket: state.bucket is required to plan with the s3 backend (hint: run `hermes state bootstrap` to create one, then set state.bucket)
Error: environment dev cannot be planned
```

## Policy (HCF3xx)

| Code | Cause | Fix |
|---|---|---|
| HCF301 | conftest is not installed (an error in protected environments) | Install conftest ([installation](../tutorials/installation.md)), or pass `--skip-policy`. The skip is recorded in history and visible to reviewers |
| HCF302 | No policies bundled and no `--policy-dir` | Use an official build, or pass `--policy-dir policies/plan` |
| HCF303 | A `deny` rule fired. The plan is saved but can never be applied | Fix the spec or module and re-plan. The message names the rule and resource |
| HCF304 | A `warn` rule fired | Review it; it does not block |
| HCF305 | `--skip-policy` was used | Expected when deliberate; it is visible in history |

## Safety-gate refusals (HCF5xx)

Printed as `Refused: HCF5xx: reason (hint)`, exit 2, and recorded in `.hermes/history/<env>.jsonl`. The order of
evaluation is in the [detailed design](../architecture/detailed-design.md#3-safety-gate-state-machine).

| Code | Cause | Fix |
|---|---|---|
| HCF501 | No saved plan for this environment (`Refused: HCF501: no saved plan in …/.hermes/plans/dev (run `hermes plan` first)`) | `hermes plan --env <env>` |
| HCF502 | Stale: the spec, the rendered root or the modules changed since the plan, or the extracted modules directory differs from the binary's | Re-plan. After upgrading `hermes`, re-plan (and use `--refresh-modules` if asked) |
| HCF503 | The plan is older than `--max-plan-age` (24 h), or its `created_at` is more than 5 minutes in the future (an edited `meta.json` or a wrong clock) | Re-plan; do not raise the age to skip a review. For a future `created_at`, check the machine clock and investigate `.hermes/plans/` |
| HCF504 | The plan file is missing, or its SHA-256 differs from `plan_id` | Someone or something modified `.hermes/plans/`. Investigate, then re-plan |
| HCF505 | The plan deletes or replaces resources | Read the `!` lines in the plan summary. Pass `--allow-destructive` only if intended |
| HCF506 | Approval missing, shorter than 8 characters, or not a prefix of the plan id | `--approve <first 8–12 chars of the plan id printed by plan>` |
| HCF507 | Destroying a protected environment without `--confirm-environment <env>` | Repeat the environment name |
| HCF508 | The policy check failed for this plan | Fix the findings and re-plan |
| HCF509 | Not confirmed: declined at the prompt, or non-interactive without `--yes`/`--approve` | In CI, pass `--yes` (non-protected) or `--approve` |
| HCF510 | The plan was already applied | Re-plan |
| HCF511 | Wrong plan: apply versus destroy, or a different environment | Use the matching command and environment |
| HCF512 | Protected environment and the policy check could not run | Install conftest, pass `--policy-dir`, or `--skip-policy` |

## HCF4xx — migration assessment

These are findings in `hermes migrate assess` output (severity `blocker`, `risk` or `info`), not command
failures ([migration commands](../migration/migration-commands.md#hermes-migrate-assess)).

| Code | Meaning | What to do |
|---|---|---|
| HCF401 | Resource type not in the service catalog (`manual-review`) | Review it; extend `migration/mappings/catalog.yaml` if the type recurs |
| HCF402 | No equivalent target service | Keep it on the source with hybrid connectivity, replace it, or retire it |
| HCF403 | Re-architecture needed | Plan application changes |
| HCF404 | Discovery gaps: parts of the source were not inspected | Grant the missing read-only permission and re-discover, or accept the gap explicitly |
| HCF405 | Resources only partially inspected | Same as HCF404 |
| HCF406 | High lock-in source service (info) | Expect changes beyond configuration |
| HCF407 | Data-bearing resource without a size; excluded from the transfer estimate | Allow `cloudwatch:GetMetricData`, or measure manually |
| HCF408 | Resource risk (public DB, internet-open security group, FIFO, VPN relay, non-PostgreSQL engine) | Read the recommendation |

`hermes migrate cutover --execute` refusals print the failing gate: `validation-report` (missing, failed,
another plan, or older than `--max-report-age`), `approval`, or `confirm-target`. The dry-run (no `--execute`)
lists every gate as `[ok]` or `[MISSING]`.

## Engine errors (exit 1)

Hermes prints the engine's own message. Common ones:

| Message | Cause | Fix |
|---|---|---|
| `no infrastructure engine found: install OpenTofu … or Terraform` | Neither `tofu` nor `terraform` is on `PATH` | [Install](../tutorials/installation.md) one |
| `tofu X is older than the minimum supported 1.10.0` (Terraform: 1.11.0) | Old engine | Upgrade. `doctor` also warns below OpenTofu 1.12 |
| `unknown engine "…" (want auto, tofu or terraform)` | Bad `engine.binary` | Fix the spec |
| `modules directory does not match the embedded modules … (re-run with --refresh-modules to replace it)` | `.hermes/modules/<version>/` was edited (including an added symbolic link or special file), or a different build wrote it | Do not edit extracted modules. Re-run with `--refresh-modules` |
| Backend `init` errors (access denied, bucket not found) | Missing credentials or permissions for the state bucket, or the bucket does not exist | `hermes state show` for the location, then `hermes state bootstrap` |
| `Error acquiring the state lock` | Another run holds the lock, or a crashed run left one behind | Confirm that no run is active, then `tofu force-unlock <id>` in `.hermes/build/<env>/` ([state management](state-management.md#moving-state)) |
| Provider errors at plan or apply (quota, API not enabled, invalid argument) | Real-cloud behaviour that mock tests cannot cover | Check the provider guide's prerequisites and [limitations](../limitations.md). Please report what you find |

## Disk, plugins and builds

| Symptom | Cause | Fix |
|---|---|---|
| `make: *** unterminated call to function 'shell': missing ')'` | GNU Make < 4.3 (macOS ships 3.81) | `go build -o bin/hermes ./cmd/hermes`, or use GNU Make ≥ 4.3 (`gmake`) |
| Disk fills up during `make tf-validate` / `tf-test` | Every module directory downloads its own providers (hundreds of MB each) | `export TF_PLUGIN_CACHE_DIR=$HOME/.cache/hermes-tf-plugins`, then remove the per-directory `.terraform/` dirs (`make clean`) |
| `doctor` warns about low disk (under 5 GiB) | Same | Same |
| `.hermes/ is not in .gitignore` (doctor) | Saved plans, history and state paths could be committed | `hermes init` adds it; or add `.hermes/` yourself |
| `-c` path not found when using `--workdir` | A relative `--config` is resolved against `--workdir` | Pass an absolute `-c`, or a path relative to the workdir |

## Still stuck

Run with `-v`, keep the `.hermes/history/<env>.jsonl` lines (they contain no secrets), and open an issue with the
command, its exit code and the diagnostic codes. Report security issues privately ([SECURITY.md](../../SECURITY.md)).
