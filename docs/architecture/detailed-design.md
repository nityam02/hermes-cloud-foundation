# Detailed design

Package-by-package design of the `hermes` CLI, the data flow from spec to rendered root, the safety-gate state
machine, the provider adapter and composition model, the migration engine internals, and sequence diagrams for
the main flows. Read the [architecture overview](overview.md) first.

Everything here is derived from the code in this repository. Behaviour that involves a real cloud is
**implemented, not tested against a real cloud** ([limitations](../limitations.md)).

## 1. Packages

| Package | Responsibility | Key types and functions | Depends on |
|---|---|---|---|
| `cmd/hermes` | Entry point; build info via `-ldflags` | `main` → `cli.Execute` | `cli` |
| `internal/cli` | cobra commands. Each command group calls `Register` from its own `init()`, so command files never edit a shared list. Maps errors to exit codes (`ExitError`, `GateError` → 2) | `App` (global flags plus injectable hooks: `DetectEngine`, `Getenv`, `LookPath`, `Now`, `RunTool`), `load()` → `loaded{doc, p, adapter, diags}`, `makePlan`, `applySaved` | everything below |
| `internal/config` | Parse YAML (yaml.v3, keeping line numbers), validate against the embedded schema, overlay one environment, apply defaults and protected rules, run semantic rules | `Document`, `Load`/`Parse`, `Document.Resolve(env)` → `*Platform`, `Validate`, codes `HCF100`–`HCF120` | `schema`, `diag`, `schemas` |
| `internal/schema` | JSON Schema 2020-12 (santhosh-tekuri/jsonschema/v6) → diagnostics with dotted paths | `HCF101` | `schemas` |
| `internal/diag` | Diagnostic model shared by every layer | `Diagnostic{Severity, Code, Path, File, Line, Message, Hint}`, `Diagnostics` | — |
| `internal/providers` | The Go side of each provider: capability matrix, regions, extension keys, workload and plan rules, provider blocks, pins | `Adapter`, `CapabilityMatrix`, `Feature`, `Status` (weight 1 / 0.5 / 0), `Requested(p)`, `Portability(p)` → `Report`, `PinAWS`… | `config`, `diag` |
| `internal/render` | Normalized `Platform` → deterministic JSON root module; extract the embedded modules with a digest manifest; bootstrap roots | `Render`, `Build` (no write; used by apply to detect staleness), `ExtractModules`, `DigestFS`, `ErrModulesChanged`, `RenderBootstrap` | `providers`, `state`, `infra` (embed) |
| `internal/state` | Backend configuration and the OpenTofu `terraform.encryption` block | `ForPlatform`, `Key` (`<prefix>/<project>/<env>/<provider>/terraform.tfstate`), `Encryption` | `config` |
| `internal/execution` | Run the engine binary; create and apply saved plans behind gates; history | `Engine` (`Init`, `Plan`, `PlanRefreshOnly`, `ShowJSON`, `OutputJSON`, `ApplySaved`), `Detect`, `CreatePlan`, `LoadPlan`, `CheckApply`, `Apply`, `PlanMeta`, `HistoryEvent`, codes `HCF501`–`HCF512` | `tfplan` |
| `internal/execution/enginetest` | Fake engine for tests: records invocations, writes plan files, returns canned JSON. It never executes a binary | — | — |
| `internal/tfplan` | The subset of the JSON plan format used by gates and adoption; classifies changes | `Parse`, `Summarize`, `Summary.Destructive()`, `NonImportChanges()` | — |
| `internal/policy` | Run conftest on `plan.json` with embedded or explicit policies; pass `data.hermes.*` via `--data` | `Check` → `PolicyRecord`, codes `HCF301`–`HCF305` | `execution`, `render`, `policies` |
| `internal/diagnostics` | `hermes doctor`: engine version, tools, credential-source names, spec, `.gitignore`, disk. No cloud calls | `LowDiskBytes` (5 GiB) | `config`, `execution` |
| `internal/inventory` | Provider-neutral `Inventory` (data-contracts §3): normalization, digests, redaction, `managed_by` annotation from Terraform state or CloudFormation, AWS Config importer | `Inventory`, `Resource`, `Relationship`, `Gap` | — |
| `internal/inventory/aws` | Read-only AWS collectors (SDK v2) over an **operation registry**. A guard middleware rejects, before sending, any operation that is not registered, not read-named, or on the secret/data-read denylist. The IAM policy is generated from the same registry | `registry.go`, `guard.go`, `policy.go`, `collect_*.go`, `gaps.go` | `inventory` |
| `internal/graph` | Dependency graph; cycle-safe topological waves (SCCs collapsed); blast radius; Mermaid/DOT/JSON | `Wave` | `inventory` |
| `internal/migration` | Catalog classification, assessment, draft spec, plan, validation checks, gated cutover | `Assess`, `Assessment`, `Plan` (`ComputeID`), `ValidationReport`, `Controller` (cutover), codes `HCF401`–`HCF408` | `inventory`, `graph`, `migration` (embed) |
| `internal/migration/adopt` | Import blocks from an Inventory into a separate adoption workspace; import-only gate | `Generate`, `Plan`, `Verify`, `Apply`, `ErrNotImportOnly` | `inventory`, `tfplan` |
| `internal/ui` | Tables, JSON, diagnostic printing, terminal detection, y/N prompt | — | `diag` |

Embedded data: `infra/embed.go` (contract, compositions, modules, bootstrap), `schemas/embed.go`,
`policies/embed.go`, `migration/embed.go` (catalog, workflows, pricing). Every migration command accepts a file
override for its data.

## 2. Data flow: spec → rendered root

```mermaid
flowchart LR
  y[hermes.yaml] -->|config.Load| doc[Document<br/>raw map + line index]
  doc -->|schema: platform/v1alpha1| doc
  doc -->|Resolve env| ov[deep-merge environments.env<br/>maps merge, lists replace]
  ov --> def[defaults +<br/>protected rules]
  def --> sem[config.Validate<br/>refs, ranges, images, state]
  sem --> plat[*config.Platform<br/>normalized]
  plat -->|adapter.Validate| pd[provider diagnostics<br/>region, sizing, extensions,<br/>certificates, capabilities]
  plat -->|render.Build| root
  subgraph root[.hermes/build/env/main.tf.json]
    tfb[terraform: required_version,<br/>required_providers exact pins,<br/>backend, encryption*]
    pb[provider blocks<br/>adapter.ProviderConfigs]
    mod[module platform<br/>source ../../modules/version/providers/p/platform<br/>contract variables]
    out[outputs: bindings,<br/>service_environment,<br/>secret_environment]
  end
  emb[(embedded infra/)] -->|ExtractModules + digest manifest| mods[.hermes/modules/version/]
  mods --> mod
```

\* `terraform.encryption` is emitted only when the engine is OpenTofu and `state.encryption.client_side` is true
(AWS KMS or GCP KMS key provider, `aes_gcm`). It is rejected on Alibaba (`HCF208`).

Properties:

- **Deterministic.** Maps are encoded with sorted keys. The render digest is the SHA-256 of the rendered files.
  The modules digest (`DigestFS`) covers the extracted tree.
- **No silent module replacement.** An extracted modules directory that differs from the embedded set returns
  `ErrModulesChanged` unless `--refresh-modules` is given. At apply time, this surfaces as a stale plan (`HCF502`).
- **Relative module source.** It is computed between real paths (symlinks resolved), because the engine resolves
  sources from the real directory (for example, macOS `/var` → `/private/var`).
- **Only the selected provider's extensions are passed** to the composition (`extensions.<provider>`).

## 3. Safety-gate state machine

### Saved-plan lifecycle

```mermaid
stateDiagram-v2
  [*] --> Rendered: hermes plan (plan checks pass)
  Rendered --> Saved: init, plan -out, show -json, classify, policy → meta.json written last
  Saved --> Unappliable: policy failed (HCF508 on apply)
  Saved --> Stale: spec or modules changed (HCF502)
  Saved --> Expired: older than --max-plan-age (HCF503)
  Saved --> Tampered: plan file hash ≠ plan_id (HCF504)
  Saved --> Applied: apply passes every gate
  Applied --> [*]: plan file deleted, meta.applied_at set (HCF510 if reused)
  Unappliable --> [*]: re-plan
  Stale --> [*]: re-plan
  Expired --> [*]: re-plan
  Tampered --> [*]: re-plan
```

`meta.json` is written **last** (and removed first when a new plan starts), so an interrupted plan never looks
applicable.

### Apply and destroy gates (`execution.CheckApply`, evaluated in this order)

```mermaid
flowchart TD
  start(["hermes apply / destroy"]) --> load{meta.json exists?}
  load -- no --> r501[HCF501 no saved plan]
  load -- yes --> build{re-render: modules dir<br/>matches embedded?}
  build -- no --> r502a[HCF502 stale]
  build -- yes --> kind{kind and environment<br/>match?}
  kind -- no --> r511[HCF511 wrong plan]
  kind -- yes --> done{already applied?}
  done -- yes --> r510[HCF510]
  done -- no --> dig{render + modules digests<br/>equal the plan's?}
  dig -- no --> r502[HCF502 stale]
  dig -- yes --> age{age ≤ max-plan-age?}
  age -- no --> r503[HCF503 expired]
  age -- yes --> sha{sha256 of plan.tfplan<br/>= plan_id?}
  sha -- no --> r504[HCF504 tampered]
  sha -- yes --> pol{policy ran and failed?}
  pol -- yes --> r508[HCF508]
  pol -- no --> des{"apply: deletes or replaces<br/>without --allow-destructive?"}
  des -- yes --> r505[HCF505]
  des -- no --> appr{"protected or destroy<br/>or --approve given?"}
  appr -- yes --> pfx{"--approve ≥ 8 chars<br/>and a prefix of plan_id?"}
  pfx -- no --> r506[HCF506]
  pfx -- yes --> conf{"destroy of protected env:<br/>--confirm-environment = env?"}
  conf -- no --> r507[HCF507]
  conf -- yes --> ok([engine apply plan.tfplan])
  appr -- no --> yes{"--yes, or y at an<br/>interactive prompt?"}
  yes -- no --> r509[HCF509]
  yes -- yes --> ok
```

At plan time, a protected environment whose policy check cannot run (no conftest, no policies) is refused with
`HCF512`, unless `--skip-policy` is given, which is recorded. `--yes` is ignored in protected environments.
Every refusal and every apply is appended to `.hermes/history/<env>.jsonl`. Tests:
`internal/execution/gates_test.go`, `internal/cli/cli_test.go`.

## 4. Provider adapter and composition model

A provider has two halves that must agree ([ADR 0003](../decisions/0003-capability-contracts-and-provider-compositions.md)):

| Go adapter (`internal/providers/<p>.go`) | HCL composition (`infra/providers/<p>/platform`) |
|---|---|
| `Capabilities()`: 21 features with status and note | implements the features with `infra/modules/<p>/*` |
| `Regions()`, `ExtensionKeys()`, `Validate`, `CheckWorkloads` (sizing, DB extensions, capability warnings), `PlanChecks` | `variables.tf` copied **verbatim** from `infra/contract/variables.tf`, plus preconditions (for example, HTTPS required when protected, unknown extension keys rejected on AWS and GCP) |
| `Providers()` (exact pins, primary first) + `SharedProviders()` (`random`) | `versions.tf` with `~>` constraints |
| `ProviderConfigs(p)`: provider block bodies (region, `allowed_account_ids`, default tags/labels) | no provider block of its own; it is configured by the root |
| — | outputs `bindings`, `service_environment`, `secret_environment` ([outputs.md](../../infra/contract/outputs.md)) |

The contract is enforced by `tests/contracts`. It checks that each composition's `variables.tf` is identical to
the contract and that each `tests/contract.tftest.hcl` contains the canonical contract runs byte for byte.
`tofu test` then runs those contract runs against every composition with mock providers. Where Go mirrors HCL
logic (Cloud Run and SAE size rounding, CloudWatch retention rounding), the Go function is named after the
module it mirrors. There is no automated cross-check between the two copies.

Adding a provider: [implementing a provider](../providers/implementing-a-provider.md).

## 5. Migration engine internals

```mermaid
flowchart LR
  inv[Inventory<br/>resources, relationships,<br/>gaps, managed_by] --> cls[classify<br/>ServiceCatalog entry<br/>per source type + target]
  cls --> asm[Assessment<br/>score, waves, blockers HCF4xx,<br/>data volume, transfer-cost range]
  asm --> gen[generate<br/>draft Platform spec,<br/>MAPPING.md, UNSUPPORTED.md]
  asm --> pln[MigrationPlan<br/>workflow steps per resource,<br/>phases, gates, deps,<br/>plan_id = sha256 of body]
  pln --> val[ValidationReport<br/>spec-coverage, bindings,<br/>http, tcp, dns, command]
  val --> cut[cutover Controller<br/>state.json + audit.jsonl]
  pln --> cut
```

- **Classify.** Each inventory type is matched to a `ServiceCatalog` entry for the target (`compatibility`:
  equivalent, compatible, partial, re-architect, none, unknown; `hermes_support`). Unknown types go to the
  `manual-review` workflow (`HCF401`).
- **Assess.** The score is the complexity-weighted share of resources whose mapping is equivalent or compatible
  **and** inside the v1alpha1 provisioning scope. Waves come from `internal/graph` (topological, SCC-collapsed).
  Every discovery gap becomes risk `HCF404`. Transfer cost is a range computed from `PricingAssumptions`, never a
  quote.
- **Plan.** `MigrationWorkflow` templates (`migration/workflows/*.yaml`) are instantiated per resource or wave.
  Each step carries `phase`, `automation` (`manual`, `hermes` or `provider-tool`), `gate` (`none`, `approval` or
  `validation`), `reversible` and `source_impact`. `plan_id` is the SHA-256 of the canonical body, so
  `LoadPlan` detects edits.
- **Validate.** Checks are argv-only (never a shell string). `http` requires TLS unless `allow_insecure`. The
  report records the `plan_id` it was run for.
- **Cutover.** A persisted state machine over the `cutover`, `monitor` and `decommission` phases, in
  `.hermes/migrations/<plan_id[:12]>/`. Step states are `pending`, `done`, `failed` and `rolled-back`; the plan
  status moves `planned` → `in-progress` → `completed`. `--execute` requires three gates: a passing validation
  report for **this** plan that is fresher than `--max-report-age` (4 h), `--approve <prefix ≥ 8>`, and
  `--confirm-target <env>`; with `--hooks`, also the hooks file the dry-run showed and `--approve-hooks <prefix ≥ 8
  of its sha256>` (scripts the hooks invoke are not hashed). It runs declared hooks in plan order and stops at the first manual step, unmet
  dependency, failure, or source-impacting step without `--approve-source-step`. Decommission steps are never
  executed, only recorded ([ADR 0014](../decisions/0014-cutover-hooks-and-no-automatic-decommission.md)).
- **Adopt.** Separate from migration. See [IaC adoption](../migration/iac-adoption.md) and §6.4.

## 6. Sequence diagrams

### 6.1 Normal provisioning (init → validate → plan → apply)

```mermaid
sequenceDiagram
  autonumber
  actor Dev as Engineer
  participant H as hermes
  participant C as config + providers
  participant R as render
  participant E as tofu / terraform
  participant P as conftest
  participant B as state backend
  participant Cl as cloud APIs
  Dev->>H: hermes init --name demo --provider aws
  H-->>Dev: hermes.yaml, .gitignore += .hermes/
  Dev->>H: hermes validate
  H->>C: load, schema, overlay, defaults, rules, adapter.Validate
  C-->>H: diagnostics (exit 2 on errors)
  Dev->>H: hermes plan --env dev
  H->>C: adapter.PlanChecks (account, state bucket, locking)
  H->>E: detect engine (≥ OpenTofu 1.10 / Terraform 1.11)
  H->>R: render root + extract modules (digests)
  H->>E: init (backend config)
  E->>B: read state, acquire lock
  H->>E: plan -out plan.tfplan
  E->>Cl: refresh / read (your credentials)
  H->>E: show -json plan.tfplan
  H->>H: classify changes (create/update/replace/delete/import)
  H->>P: conftest test plan.json (+ data.hermes.*)
  H->>H: write plan.json, then meta.json (plan_id = sha256)
  H-->>Dev: summary, plan id, "Next: hermes apply ..."
  Dev->>H: hermes apply --env dev [--approve id] [--allow-destructive]
  H->>R: re-render without writing (digests)
  H->>H: CheckApply gates (section 3)
  alt any gate refuses
    H-->>Dev: Refused: HCF5xx (exit 2), recorded in history
  else all pass
    H->>E: apply plan.tfplan
    E->>Cl: create / update
    E->>B: write state, release lock
    H->>H: meta.applied_at, delete plan.tfplan, history
    H-->>Dev: "apply of plan … complete", bindings hint
  end
```

### 6.2 Multi-cloud deployment (same spec, per-environment provider, isolated state)

```mermaid
sequenceDiagram
  autonumber
  actor Dev as Engineer
  participant H as hermes
  participant A as AWS (awsdev)
  participant G as GCP (gcpdev)
  participant L as Alibaba (alidev)
  Dev->>H: hermes portability
  H-->>Dev: per-provider score and differences (no cloud calls)
  par one environment per cloud, each with its own credentials
    Dev->>H: hermes plan/apply --env awsdev
    H->>A: s3://…/sample-portable/awsdev/aws/terraform.tfstate (lockfile)
    H->>A: ECS Fargate, RDS, S3, SNS+SQS, Secrets Manager
  and
    Dev->>H: hermes plan/apply --env gcpdev
    H->>G: gs://…/sample-portable/gcpdev/gcp/default.tfstate
    H->>G: Cloud Run, Cloud SQL, GCS, Pub/Sub, Secret Manager
  and
    Dev->>H: hermes plan/apply --env alidev
    H->>L: oss://…/sample-portable/alidev/alibaba/ (Tablestore lock)
    H->>L: SAE, ApsaraDB RDS, OSS, MNS, KMS
  end
  Note over H: plans saved per environment (.hermes/plans/env/), so a plan for one env can never apply to another (HCF511)
  Dev->>H: hermes bindings --env gcpdev -o env --service api
  H-->>Dev: same HERMES_* names, provider-specific values
```

### 6.3 Existing AWS discovery (assumed role, read-only guard, gaps)

Architecture-level view. The detailed sequence, including the credential flow, is in
[discovery engine](../migration/discovery-engine.md#sequence-discovery-through-an-assumed-role).

```mermaid
sequenceDiagram
  autonumber
  actor Op as Operator
  participant H as hermes inventory aws
  participant G as read-only guard (operation registry)
  participant STS as AWS STS
  participant API as AWS service APIs
  participant TF as Terraform state / CloudFormation (optional)
  Op->>H: --role-arn … --external-id … --regions …
  H->>STS: AssumeRole with ExternalId (1 h session)
  loop service × region (worker pool)
    H->>G: Describe*/List*/Get* (metadata)
    alt not registered, not read-named, or secret/data read
      G-->>H: refused before sending
    else allowed
      G->>API: signed request
      API-->>H: metadata, or AccessDenied / Throttling / unsupported
      H->>H: normalize, or on error record a gap (resource stays partial)
    end
  end
  opt --terraform-state / CloudFormation
    H->>TF: read only to annotate managed_by (never a dependency)
  end
  H-->>Op: inventory.json (read_only: true, gaps, relationships), --operations-out proof
```

### 6.4 Terraform resource adoption (import blocks → generate-config-out → verify → apply)

Detailed view: [IaC adoption](../migration/iac-adoption.md).

```mermaid
sequenceDiagram
  autonumber
  actor Op as Operator
  participant H as hermes adopt
  participant E as tofu / terraform
  participant S as separate source-adoption state
  participant AWS as source AWS account
  Op->>H: adopt aws --inventory inventory.json --types … --out adopt/
  H->>H: skip resources already managed (unless --include-managed)
  H-->>Op: providers.tf, imports.tf, backend.tf, README.md, adoption.json
  Op->>H: adopt plan --dir adopt/
  H->>E: init, then plan -out adopt.tfplan -generate-config-out=generated.tf (first run only)
  E->>AWS: read the resources being imported
  H->>E: show -json adopt.tfplan
  H->>H: Verify: every change is a pure import of an address in adoption.json
  alt any create/update/replace/delete, unexpected import, or errored plan
    H-->>Op: refused: ErrNotImportOnly (offending addresses)
  else import-only
    H-->>Op: plan id, N imports
    Op->>Op: review generated.tf (it may contain sensitive values)
    Op->>H: adopt apply --dir adopt/ --approve PREFIX (8+ chars)
    H->>H: re-verify the saved plan, check the approval prefix
    H->>E: apply adopt.tfplan
    E->>S: write adoption state (never the target state)
  end
```

### 6.5 Database migration (continuous replication → lag → freeze → promote)

These steps come from `migration/workflows/relational-database-postgresql.yaml`. Hermes runs none of them by
itself. Each step is manual, a provider tool, or a declared hook. Provider-specific detail is in
[AWS to GCP](../migration/aws-to-gcp.md#database-migration-sequence), [database migration](../migration/database-migration.md)
and the [PostgreSQL cutover runbook](../../migration/runbooks/postgresql-cutover.md).

```mermaid
sequenceDiagram
  autonumber
  actor Op as Operator
  participant H as hermes migrate
  participant Src as source RDS PostgreSQL
  participant Svc as replication service (DMS / DTS)
  participant Tgt as target PostgreSQL
  participant App as applications
  Op->>Src: configure-source-parameters: logical replication (approval, modifies source config)
  Op->>Src: create-migration-user, open-network-path (approval)
  Op->>H: provision-target via hermes plan/apply (approval)
  Op->>Svc: start-replication: initial load + continuous CDC (provider-tool, approval)
  loop ddl-freeze-and-monitor
    Svc-->>Op: replication lag
  end
  Op->>H: migrate validate (validate-data: row counts, checks) → report
  Op->>Op: rehearse-cutover (approval)
  Op->>H: migrate cutover --execute --approve … --confirm-target …
  H->>App: freeze-writes hook (approval, modifies source)
  H->>Svc: drain-and-fix-sequences (lag → 0, sequences synced)
  H->>Tgt: promote-target (provider-tool, NOT reversible)
  H->>App: switch-application (HERMES_DB_* now point at the target)
  Op->>Src: keep-source-read-only for the rollback window
```

### 6.6 Traffic cutover (gated hooks)

```mermaid
sequenceDiagram
  autonumber
  actor Op as Operator
  participant H as hermes migrate cutover
  participant St as .hermes/migrations/id/
  participant Hk as declared hook (argv)
  participant DNS as DNS / traffic manager
  Op->>H: --plan p.yaml --report r.json --hooks hooks.yaml (dry-run)
  H-->>Op: gates [ok]/[MISSING], runbook with step states, hooks sha256
  Op->>H: --execute --approve PREFIX --approve-hooks HOOKS-PREFIX --confirm-target production
  H->>H: CheckGates: report passed (recomputed from its results) for the same plan_id and under 4 h old, approval, target, hooks digest approved
  alt a gate is missing
    H-->>Op: refused (exit 2), nothing runs
  else gates pass
    loop pending cutover/monitor steps in plan order
      alt manual step, or unmet dependency
        H-->>Op: stop: record with --complete-step ID --approve PREFIX
      else source-impacting step without --approve-source-step
        H-->>Op: stop: needs --approve-source-step ID
      else hook declared
        H->>Hk: run argv (HERMES_PLAN_ID, HERMES_STEP_ID, HERMES_ACTION=run)
        Hk->>DNS: e.g. weighted-shift target=10
        Hk-->>H: exit code + output tail
        H->>St: step done/failed → state.json, audit.jsonl
      end
    end
  end
```

### 6.7 Rollback

```mermaid
sequenceDiagram
  autonumber
  actor Op as Operator
  participant H as hermes migrate cutover --rollback
  participant St as state.json / audit.jsonl
  participant Hk as declared rollback hooks
  Op->>H: --rollback --approve PREFIX --confirm-target production
  H->>H: approval and target gates
  H->>St: load completed steps
  loop completed reversible steps, in reverse order
    alt rollback hook declared
      H->>Hk: run rollback argv (HERMES_ACTION=rollback)
      Hk-->>H: result
      H->>St: step rolled-back
    else no rollback hook
      H-->>Op: listed as manual rollback
    end
  end
  H-->>Op: irreversible steps listed (e.g. promote-target): restore from the source, which was kept intact
```

The operational procedure is in the [rollback runbook](../../migration/runbooks/rollback.md). After the target
database is promoted, rolling back to the source depends on the source having been kept read-only and intact
(`keep-source-read-only`), or on the optional reverse replication.
