# Migration commands

The migration toolkit keeps five activities separate. Each has its own commands, and none of them
modifies source resources:

| Activity | Commands |
|---|---|
| Discovery (read-only) | `hermes inventory aws`, `inventory import`, `inventory graph`, `inventory policy` |
| IaC adoption (source, import-only) | `hermes adopt aws`, `adopt plan`, `adopt verify`, `adopt apply` ([iac-adoption.md](iac-adoption.md)) |
| Destination provisioning | `hermes migrate generate`, then the platform commands `hermes validate`, `render`, `plan`, `apply --approve` |
| Data migration | Plan steps delegating to managed services (Google DMS, Storage Transfer Service, Alibaba DTS, Data Online Migration), tracked by `migrate plan`, `migrate validate` |
| Application migration and cutover | `hermes migrate validate`, `hermes migrate cutover` (gated; runs only hooks you declare) |

Conventions:
- The global `-o/--output text|json` controls what is printed to stdout.
- Documents are written with `--out <path>`. Without `--out`, the document itself goes to stdout and
  the human summary to stderr.
- Exit codes: 0 ok, 1 error, 2 validation failed or gate refused.
- Every engine-free command works offline.

Status: everything below is implemented and covered by Go tests (fakes, `httptest`, local listeners),
plus the moto demo for `inventory aws`. Nothing has been run against a real cloud by the
maintainers.

## hermes inventory aws

```bash
hermes inventory aws --role-arn arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly \
  --external-id <id> --regions eu-north-1 --out inventory.json [--redact] \
  [--services ec2,rds,s3] [--terraform-state prod.tfstate] [--cloudcontrol-types AWS::IoT::Thing,AWS::Timestream::Database] \
  [--concurrency 4] [--max-attempts 5] [--operations-out ops.json] [--endpoint-url http://127.0.0.1:5055]
```

```text
Discovered 58 resource(s) in eu-north-1 (collector aws-api, read-only).
  AWS::EC2::Instance                               3
  ...
Relationships: 78. Gaps: 2.
  gap: cloudwatch eu-north-1 GetMetricData: unsupported (S3 bucket sizes unknown; transfer cost estimates exclude these buckets)
Inventory written to inventory.json. Next: hermes migrate assess --inventory inventory.json --target gcp|alibaba
```

Credentials come from the default chain or `--profile`. `--external-id` without `--role-arn` is
refused. A role without an ExternalId prints a warning. See [discovery-engine.md](discovery-engine.md).

## hermes inventory import

```bash
aws configservice select-resource-config --expression "SELECT resourceId, resourceType, resourceName, awsRegion, arn, accountId, tags, configuration, relationships" > config.json
hermes inventory import --format aws-config config.json --out inventory.json
```

The result has `collector: aws-config`, completeness at most `partial`, and a `not-enabled` gap for
unrecorded types.

## hermes inventory graph

```bash
hermes inventory graph --inventory inventory.json --format mermaid|dot|json [--focus <resource-id>] [--out graph.mmd]
```

`--focus` prints only the blast radius: dependents and dependencies, and to stderr
`Blast radius of <id>: N dependent(s), M dependenc(ies).` JSON output includes the waves.

## hermes inventory policy

Prints the generated least-privilege IAM policy. It is identical to
`migration/discovery/aws-readonly-discovery-policy.json`.

## hermes migrate catalog

```bash
hermes migrate catalog [--catalog file] [--target gcp|alibaba] --format markdown|json|text
hermes migrate catalog --format markdown > docs/migration/service-catalog.md   # make docs-generate
```

The markdown output is deterministic, grouped by category, and starts with a
"generated, do not edit" comment. Columns: source service, CFN types (with match keys), target,
target service, compatibility, complexity, data migration, Hermes support, lock-in.

## hermes migrate assess

```bash
hermes migrate assess --inventory inventory.json --target gcp|alibaba [--catalog f] [--pricing f] \
  [--project fleet-platform] [--env production] --out assessment.json --markdown assessment.md
```

```text
Assessed 62 resource(s) for gcp: portability score 29.7%, 1 blocker(s), 8 risk(s), 4 discovery gap(s), 6 wave(s).
Data to migrate: 1953597383680 bytes required; indicative transfer cost USD 97.68-332.11 (range from pricing assumptions, not a quote).
```

**Classification** follows the catalog `match` semantics (data-contracts §4). Unknown types become
`other`, `unknown`, `manual-review`.

The **portability score** is the complexity-weighted share (low 1, medium 2, high 3) of resources whose
mapping is equivalent or compatible **and** `hermes_support: implemented`, which means within the
Hermes v1alpha1 provisioning scope.

**Findings** carry `HCF4xx` codes:

| Code | Finding |
|---|---|
| HCF401 | unmapped type |
| HCF402 | no target |
| HCF403 | re-architect |
| HCF404 | discovery gaps |
| HCF405 | partially inspected |
| HCF406 | high lock-in |
| HCF407 | data without size |
| HCF408 | resource risks: public DB, internet-open SG, FIFO, network appliance/VPN relay, non-PostgreSQL engine |

**Transfer cost** is a **range, never a quote**:
- low = required bytes × the cheapest listed AWS internet egress rate;
- high = (required + optional bytes) × (the highest AWS egress rate + the highest target transfer or
  replication fee).

The rate ids and sources used are listed, and exit-fee waiver programmes are listed as "may apply",
never subtracted.

**Waves** come from the dependency graph. `draft_spec`, `spec_mapping` and `unsupported` feed
`migrate generate`.

## hermes migrate generate

```bash
hermes migrate generate --assessment assessment.json --env production [--project name] --out target/
```

Writes:
- `target/hermes.yaml`: a draft Platform spec. Its header says `# DRAFT — requires human review`
  and lists the items flagged for review;
- `MAPPING.md`: source → spec names and notes, plus the container images to copy: an image in the
  source registry (Amazon ECR) is never kept in the draft (the target would keep depending on the
  source, and Cloud Run / SAE cannot pull from ECR); it is replaced by
  `REPLACE-WITH-TARGET-REGISTRY/<repo>:<tag>`, flagged for review in `hermes.yaml`, and listed with
  its source reference so you can copy it (for example with `crane copy`) and fill in the target registry;
- `UNSUPPORTED.md`: what Hermes cannot generate, with the recommended target service and a suggested
  module or provider resource to verify.

The draft is derived as follows:
- **Network:** CIDR the same size as the main source VPC, chosen **not to overlap** any source CIDR so
  hybrid connectivity can route; zones from the subnets' AZs; NAT if the source has one.
- **Database:** PostgreSQL from RDS PostgreSQL. Major version, HA from Multi-AZ, storage, backup
  retention, deletion protection, and `postgis` when the parameter group name says so.
- **Buckets:** from S3. Names are sanitized to the spec pattern with a hash suffix.
- **Topics and subscriptions:** from SNS plus SQS subscriptions (ack deadline from visibility timeout,
  dead-lettering from the redrive policy, retention capped at 7 days). A standalone queue becomes a
  topic with one subscription; DLQs are absorbed into dead-lettering.
- **Secrets:** names from Secrets Manager. Service-owned secrets, such as RDS master passwords, are
  excluded.
- **Services:** from ECS (image, CPU/memory snapped to supported sizes, port, `secret_env` mapped to
  secret names, `public` from an internet-facing LB, health check path, database access from
  security-group references). EC2 container hosts appear as placeholder services flagged
  **REVIEW**.

Then run `hermes validate -c target/hermes.yaml` and `hermes render -c target/hermes.yaml`. The
platform renderer produces OpenTofu. The sample's draft passes `hermes validate`, with warnings for
the missing `cloud.account` and `state.bucket`.

## hermes migrate plan

```bash
hermes migrate plan --assessment assessment.json --env production [--workflows dir] --out migration-plan.yaml --markdown plan.md
```

```text
Plan 1d31942b6a41: 223 step(s) in 8 phase(s); 134 approval gate(s), 21 validation gate(s), 62 step(s) touch the source.
```

Steps are instantiated from the workflows:
- **once:** foundation, hybrid connectivity, traffic cutover, decommission;
- **per resource:** relational and NoSQL databases;
- **per (workflow, wave):** everything else.

Each step carries `gate`, `source_impact`, `reversible`, `rollback`, `validation`, `resource_refs` and
`depends_on`. Dependencies:
- steps run in order within a workflow instance;
- everything waits for the foundation;
- synchronization waits for hybrid connectivity;
- wave k cuts over after wave k-1;
- traffic cutover waits for every resource's validation;
- decommission waits for traffic cutover and is always `manual`.

Steps that modify the source (`modifies-source-config`, `modifies-source-data`), and cutover or
decommission steps, always carry `gate: approval`.

`plan_id` is the sha256 of the canonical body. Any edit makes `validate` and `cutover` refuse the
plan as tampered.

## hermes migrate validate

```bash
hermes migrate validate --plan migration-plan.yaml --checks checks.yaml --out validation-report.json
```

```text
PASS  spec-coverage  target-spec-coverage         all 27 in-scope source resources are covered
FAIL  command        row-counts-match             exit code 1
1/2 passed. Report written to validation-report.json (plan 1d31942b6a41).
Error: validation failed: 1 failed, 0 error(s)       (exit 2)
```

Check types (see [`examples/migration/checks.yaml`](../../examples/migration/checks.yaml)):

| Type | What it checks |
|---|---|
| `spec-coverage` | Offline: the target spec has an element for every source resource in scope (plan `coverage`) |
| `bindings` | Reads `hermes bindings -o json` output and expects bucket, topic, subscription, secret and service keys and a database endpoint |
| `http` | Status and optional body text. TLS is required unless `allow_insecure: true`; `ca_file` sets a private CA; redirects are not followed |
| `tcp` | Connect |
| `dns` | The name resolves to the expected values (A/AAAA, or CNAME) |
| `command` | A declared `argv` (never a shell string) with a timeout; exit 0 passes. Output is streamed to stderr only; the evidence keeps exit code, output byte count and sha256 (never the output, which may contain secrets) |

The report has `plan_id`, timestamps and per-check evidence.

## hermes migrate cutover

Dry-run (default) shows the gates and the runbook of cutover, monitor and decommission steps:

```text
Cutover for plan 1d31942b6a41 (target environment production) — status planned. DRY-RUN: nothing is executed.

Gates for --execute:
  [ok] validation-report: passed 2/2 checks, 1s old
  [MISSING] approval: --approve <plan-id-prefix> (at least 8 characters) is required
  [MISSING] confirm-target: --confirm-target production is required
  [ok] hooks-file: hooks file sha256 55337a855e83 matches the dry-run of 2026-11-18T12:00:00Z
Runbook:
   1. [pending] nosql-database.device-state.switch-reads-writes (cutover, gate approval, source impact modifies-source-config)
      action: manual; record with --complete-step ... (needs --approve-source-step ...)
```

```bash
hermes migrate cutover --plan migration-plan.yaml --report validation-report.json --hooks cutover-hooks.yaml \
  --execute --approve 1d31942b --approve-hooks 5f0c27e1 --confirm-target production [--approve-source-step <id>]... [--step <id>]
hermes migrate cutover --plan migration-plan.yaml --complete-step <id> [--approve <prefix>] --note "done in console"
hermes migrate cutover --plan migration-plan.yaml --rollback --approve <prefix> --confirm-target production
hermes migrate cutover --plan migration-plan.yaml --status
```

`--execute` requires:
- a validation report that **passed**, for the **same plan_id**, younger than `--max-report-age`
  (default 4h). "Passed" is recomputed from the report's per-check `results[]` (at least one
  result, every status `pass`); the top-level `passed` and `summary` fields are never trusted, so
  editing them does not unlock a cutover. The report is still an unsigned local file (see
  [limitations](../limitations.md)): someone who can edit `results[]` can forge it;
- `--approve` with at least 8 characters of the plan id;
- `--confirm-target` equal to the plan's target environment;
- with `--hooks`: the **same hooks file the dry-run showed**. The dry-run (not `--status`) records the
  hooks file's sha256 in `state.json` and prints it. `--execute` and `--rollback` refuse (exit 2) when the
  file's digest differs, when no dry-run was recorded, or without `--approve-hooks <at least 8 hex
  characters of that sha256>` (case-insensitive). Re-running the dry-run on an edited file is not
  enough: the new digest must be approved explicitly, so the earlier approval never covers changed
  hooks. **Only the hooks file (the argv lists) is hashed; the scripts it invokes are not**, so review
  them (and keep them under version control) before approving.

It then runs, in order, **only the hook argv declared** for each step in the hooks file
([example](../../examples/migration/cutover-hooks.yaml)). It stops at the first manual step, unmet
dependency or failure. Steps that touch the source also need `--approve-source-step <id>`.

Hermes has no built-in action that touches DNS, databases or source resources. Decommission steps can
never have hooks. They are recorded with `--complete-step`, which needs `--approve` for
approval-gated steps.

`--rollback` runs the declared rollback hooks of completed reversible steps in reverse order. It
lists reversible steps without a rollback hook as manual, and irreversible ones separately.

State lives in `.hermes/migrations/<plan_id[:12]>/state.json` (atomic writes), with an append-only
`audit.jsonl` that records every dry-run, execution, completion, rollback and refusal, with the user
and time.

**Hook output is never persisted.** A hook's combined stdout/stderr is streamed to your terminal
(stderr) only. `state.json` and `audit.jsonl` record a `hook` object per run: `action`, `exit_code`
(-1 when the command could not start or hit its timeout), `duration_ms`, `output_bytes` and
`output_sha256`. Keep your own terminal log if you need the output: its sha256 matches the record.

**Hooks inherit your environment.** Hooks run as you, with your full environment (plus
`HERMES_PLAN_ID`, `HERMES_STEP_ID`, `HERMES_ACTION`), because they need credentials for the tools they
call (DNS, database, traffic manager). Run cutovers with **narrowly scoped, short-lived credentials**:
a dedicated cutover role or session that can do only the declared steps for this migration, issued for
the cutover window (for example an STS session of an hour or less). Do not run them from a shell that
holds broad administrator credentials. Review every hook argv in the dry-run before approving.

## hermes adopt

See [iac-adoption.md](iac-adoption.md). In short:
- `adopt aws` writes import blocks for a separate source-adoption state;
- `adopt plan` runs init and plans with `-generate-config-out`;
- `adopt verify` refuses anything but pure imports (exit 2, with the offending addresses);
- `adopt apply --approve <prefix>` applies only the verified saved plan.
