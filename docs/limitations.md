# Limitations and known gaps (register)

This is the single register of what Hermes v0.1 (`hermes/v1alpha1`) does **not** do, or has not proven. It is
deliberately blunt. If another document disagrees with this page, treat that as a bug and report it.

Last reviewed: 2026-11-19, against the code in this repository. Sources: the provider guides
(`docs/providers/*.md`), the Go capability matrices (`internal/providers/*.go`), the lead integration notes, the
research report ([open-source-landscape §10 and §12](research/open-source-landscape.md)), code comments, and
running the CLI.

## Status labels

| Label | Meaning |
|---|---|
| **implemented & verified (offline)** | The code exists, and automated tests that need no cloud account exercise its behaviour: Go tests, `tofu test` with mock providers, conftest fixtures, or a local moto server |
| **implemented, not tested against a real cloud** | The code exists and passes offline checks, but no real cloud account has ever run it. Provider acceptance, IAM propagation, quotas and runtime behaviour are unproven |
| **designed, not implemented** | Documented or reserved in the spec, but no working code exists |
| **not supported** | Out of scope, or impossible with the current provider, engine or service |

## 0. The headline

- **Nothing has been deployed to a real cloud.** No `plan`, `apply`, `destroy`, `status --drift`, `bindings`,
  `state bootstrap --apply`, `inventory aws` (against real AWS), `adopt` or `cost` run has ever touched a real
  AWS, GCP or Alibaba Cloud account in this project. Every provider feature is at best
  *implemented, not tested against a real cloud*.
- **Real-cloud tests are opt-in, and none has ever run.** One exists: a read-only AWS discovery test in
  `tests/integration/aws-real/` (build tag `realaws`; it skips unless `HERMES_IT_ALLOW_REAL_AWS=1` and a
  discovery role are given). It has **not** been run. There are no real-cloud tests for provisioning
  (`plan`/`apply`) on any provider: *designed, not implemented*
  ([ADR 0013](decisions/0013-testing-without-cloud-credentials.md)).
- **Mock providers prove wiring, not acceptance.** `tofu test` with `mock_provider` shows that variables flow
  to the right arguments, conditional logic works, and IAM documents contain what we intend. It cannot show
  that the cloud API accepts the request, that an argument combination is valid at apply time, or that IAM
  propagates in time.
- **Capability matrices describe module design.** `hermes providers` and `hermes portability` report statuses
  of the Hermes modules as designed and mock-tested ("none is verified against a real cloud account", as the
  CLI prints). A score of 100 means "every requested feature is implemented in our module". It does not mean
  "works on that cloud".

## 1. CLI and platform

| Item | Status | Notes |
|---|---|---|
| `validate`, `portability`, `render`, `render --print`, `providers`, `version`, `doctor`, `init`, `state show` | implemented & verified (offline) | Go tests, plus manual runs captured in the tutorials |
| `plan`, `apply`, `destroy`, saved-plan gates, history | implemented & verified with a fake engine (`internal/execution/enginetest`); **not tested against a real cloud** | The gate logic is engine-independent and unit-tested. The engine invocations have never run against a real backend |
| `status --drift` | implemented, not tested against a real cloud | Runs `plan -refresh-only -detailed-exitcode` |
| `bindings` | implemented, not tested against a real cloud | Reads `output -json` of an applied root |
| `cost` | implemented, not tested | Infracost was not installed on the development machine, so the wrapper has only run against a fake runner. Alibaba: **not supported** (Infracost does not price Alibaba Cloud) |
| `state bootstrap` | implemented, not tested against a real cloud | The bootstrap root uses local state (chicken-and-egg); see [state management](operations/state-management.md) |
| Terraform as the engine | implemented; integration pass **not run** | The three compositions pass `terraform validate` and `terraform test` (mock providers). `make integration` was run with OpenTofu only |
| Region lists | static snapshot (November 2026) | Unknown regions produce warning `HCF200`, not an error. New regions may be missing |
| Cloud Run and SAE size rounding | mirrors module code | The Go rounding (`CloudRunEffectiveSize`, `SAEEffectiveSize`) duplicates HCL logic. A drift between the two would make `portability` findings wrong; there is no automated cross-check |
| `--workdir` and `-c` | by design | A relative `--config` is resolved against `--workdir`, not the shell's current directory |
| Engine environment | partially sanitized | `TF_CLI_ARGS` and `TF_CLI_ARGS_<command>` are removed from every engine invocation (with a one-time warning), so they cannot inject unrecorded arguments. Other variables pass through deliberately (credentials, proxies, `TF_LOG`, `TF_PLUGIN_CACHE_DIR`); `TF_WORKSPACE` and `TF_DATA_DIR` also pass through and would change which workspace or data directory the engine uses: do not set them when running Hermes |

## 2. Specification (`hermes/v1alpha1`)

| Item | Status | Notes |
|---|---|---|
| `compute.type: kubernetes` | designed, not implemented | Rejected with `HCF111` ([ADR 0005](decisions/0005-serverless-containers-default-compute.md)) |
| `database.engine: mysql` | designed, not implemented | Rejected with `HCF111` |
| `dns.enabled: true` | designed, not implemented | Rejected with `HCF111`. No custom domains on any provider |
| One database per environment | by design | No read replicas, no multiple databases |
| Overlays cannot change `project.name`, `project.environment` or `protected_environments` | by design | Prevents an overlay from un-protecting an environment |
| Undeclared `--env` | by design | An error (`HCF103`), never an implicit environment |
| `security.customer_managed_keys` | provider-dependent | See the CMK rows per provider below |

## 3. AWS (ECS Fargate, RDS, S3, SNS+SQS, Secrets Manager, CloudWatch)

Evidence (2026-11-21): 58/58 AWS `tofu test` runs across composition, modules and bootstrap, of which 11/11 on
the composition (mock providers); `terraform test` on the composition was recorded before remediation round 2 and
not re-run since; tflint (aws ruleset 0.50.0) clean; trivy and checkov 0 findings with justified inline skips. Details:
[providers/aws.md](providers/aws.md).

| Item | Status |
|---|---|
| Every feature in the matrix | implemented, not tested against a real cloud |
| `compute.scale_to_zero` | partial: only queue consumers scale from zero on SQS backlog. HTTP services at 0 instances return 503 until scaled |
| `database.postgis` | partial: `CREATE EXTENSION postgis` is a manual post-provision step |
| `database.pitr` | always on with automated backups. `point_in_time_recovery: false` cannot disable it (warning `HCF210`) |
| Log retention | rounded up to a CloudWatch-accepted value (warning `HCF202`) |
| KMS key policies for SNS→SQS and CloudWatch→SNS | unverified on a real cloud |
| ADOT collector with a read-only root filesystem | unverified |
| Queue-backlog scaling, `db.m7g` availability per region | unverified |
| `network.nat: false` | tasks have **no egress**: no VPC interface endpoints are created, so non-ECR image pulls and AWS API calls fail (warning `HCF210`) |
| Service discovery (Service Connect / Cloud Map) | not implemented. Private services have URL `""` |
| DNS, host-based routing | not implemented. Path routing keeps the `/<name>` prefix (not stripped) |
| ALB access logs, S3 server access logs, WAF, Fargate Spot, non-ECR private registries, cross-account ECR | not implemented |
| Task egress | TCP 443 only (plus 5432 to the database). Other ports need a module change |
| IAM role names | account-global: the same project and environment in two regions of one account collide |
| Partitions | only `aws`. China and GovCloud are not supported (absent from the region list) |
| Alarm thresholds | fixed (not configurable) |
| Reserved topic name | `hermes-alerts` is used by the alert topic |
| Final snapshot name | static (`<id>-final`). Recreating and destroying again needs the old snapshot removed or renamed |

## 4. Google Cloud (Cloud Run, Cloud SQL, GCS, Pub/Sub, Secret Manager, Cloud Logging/Monitoring/Trace)

Evidence (2026-11-21): 17/17 `tofu test` runs on the composition and 47/47 across GCP composition, modules and
bootstrap (mock providers); `terraform test` recorded before remediation round 2; trivy and checkov 0 findings with
inline ignores; tflint 0 findings (bundled terraform ruleset; google ruleset not installed).
Details: [providers/gcp.md](providers/gcp.md).

| Item | Status |
|---|---|
| Every feature in the matrix | implemented, not tested against a real cloud |
| `database.postgis` | partial: manual `CREATE EXTENSION` |
| CMEK overall | partial in practice: Cloud SQL, GCS, Pub/Sub and Secret Manager use the key. Cloud Run and the log bucket stay Google-managed. The Go matrix lists `database.customer_managed_keys` and `storage.customer_managed_keys` as implemented, which is accurate for those resources |
| `observability.logs_retention` | the `_Default` sink exclusion is not verified on a real cloud |
| Cloud SQL final backup on delete | not implemented (deletion protection is the safeguard) |
| Custom domains, Cloud Armor, Binary Authorization, VPC Service Controls | not implemented |
| Org policies (Domain Restricted Sharing, CMEK-required, resource locations) | not handled: they can block `allUsers` invoker bindings or resources |
| Service-to-service calls to internal services | not automated. Internal services expose `url = ""` |
| `bindings.database.user` | set only when exactly one service has database access, otherwise `""` |
| Cloud SQL backups | Google's default multi-region location |
| `network.zones` | has no effect on GCP (regional services) |
| Cloud Run CPU | rounded up to 1, 2, 4, 6 or 8 vCPU (warning `HCF202`); fractional CPU is never used |
| Consumers without the tracing sidecar | CPU is request-based, so background pulling may stall (warning `HCF211`) |
| Collector image tag and sidecar sizing | not verified on Cloud Run |
| Apply identity | inherently broad (about 14 admin roles); see [providers/gcp.md](providers/gcp.md#prerequisites) |

## 5. Alibaba Cloud (SAE, ApsaraDB RDS for PostgreSQL, OSS, MNS, KMS, SLS)

Evidence (2026-11-21): 12/12 `tofu test` runs on the composition and 39/39 across Alibaba composition, modules
and bootstrap (mock providers); `terraform test` recorded before remediation round 2; checkov 0 failed (about 43
Alibaba checks upstream); tflint/fmt/gitleaks clean. There is **no Trivy coverage and no TFLint ruleset** for Alibaba, so plan policies in
`policies/` are the only automated guardrails. Details: [providers/alibaba.md](providers/alibaba.md).

| Item | Status |
|---|---|
| `compute.scale_to_zero` | not supported: the SAE minimum is 1 instance (`min_instances: 0` becomes 1, warning `HCF202`) |
| `compute.otel_sidecar` | not implemented: no collector. Traces go to `extensions.alibaba.otlp_endpoint` |
| `database.iam_auth` | not supported: password authentication, with the account created out-of-band |
| `secrets.native_injection` | not implemented: SAE cannot inject KMS secrets with provider 2.2. Apps read secrets with their role, and a KMS 3.0 instance (a significant fixed cost) is required |
| `state.client_side_encryption` | not supported: OpenTofu has no Alibaba KMS key provider (`HCF208`) |
| `database.pitr` | partial: High-availability Edition only |
| `observability.alerts` | partial: an e-mail contact plus RDS CPU and disk alarms. No SAE or queue alarms. The RDS metric names were not re-verified for PostgreSQL |
| `observability.tracing` | partial: endpoint from the extension only |
| `identity.per_service` | partial: a RAM role is created per service, trusted by the SAE service principal `sae.aliyuncs.com` (override with `extensions.alibaba.workload_role_trusted_principals`; the account root is rejected). The SAE principal has not been verified against a real account, and RRSA/OIDC trust is designed, not implemented. **All service roles share one trust policy**: any SAE application in the account (or, with `workload_role_trusted_principals`, any listed principal) can be bound to or assume any service's role, so per-application isolation of service identities does not exist on Alibaba yet; per-app scoping needs RRSA (designed, not implemented). See [Alibaba provider](providers/alibaba.md) for mitigations. The provider cannot attach the role to the SAE application, so binding is manual, and until then the app has no cloud credentials |
| `database.postgis` | partial: manual `CREATE EXTENSION` |
| Manual post-provision steps | RDS→KMS authorization, database account creation, secret values, RAM role binding, alert e-mail activation. MNS SSE needs a support ticket (opt-in `mns_sse`) |
| Unverified on a real cloud | SAE comma-separated vSwitch IDs, KMS-instance keys for OSS and RDS, MNS RAM ARN formats, the SAE security-group rule for `100.64.0.0/10` |
| Logstore CMK (BYOK) | not implemented |
| Private services | no internal load balancer (`url = ""`) |
| Public services | one CLB per public service (cost), unlike the single shared ALB on AWS |
| OSS tags | at most 6 user `labels` (OSS allows 10 tags and Hermes uses 4) |
| State locking | requires `extensions.alibaba.state_tablestore_endpoint` and `_table`. A protected environment cannot be planned without it (`HCF212`; a warning at validate). How the OSS backend locks through Tablestore in current engine releases was not verified ([research §12](research/open-source-landscape.md#12-not-verified-in-this-pass)) |
| Unknown `extensions.alibaba` keys | warning only (the AWS and GCP compositions reject unknown keys, so those are errors there) |
| Cost estimation | not supported by Infracost. Use the pricing pages, and `PricingAssumptions` for migration estimates |

## 6. State, bootstrap and CI identity

| Item | Status |
|---|---|
| S3 / GCS / OSS backends, rendered config | implemented & verified (offline, golden tests); no backend has ever been initialized for real |
| S3 native lockfile (`use_lockfile`) | implemented, not tested against a real cloud. Needs OpenTofu ≥ 1.10 or Terraform ≥ 1.11 |
| OpenTofu client-side state encryption (AWS KMS, GCP KMS) | implemented, not tested with a real KMS key. Not available with Terraform |
| Bootstrap modules (`infra/bootstrap/<p>/state-backend`) | implemented, plan-only mock tests (`prevent_destroy`) |
| CI OIDC identities (`infra/bootstrap/<p>/ci-identity`) | implemented, mock-tested; not tested against a real cloud. The apply permission set is inherently broad |
| Bootstrap state | kept locally under `.hermes/state/_bootstrap/` until you migrate it |

## 7. Policy, security tooling and supply chain

| Item | Status |
|---|---|
| Conftest plan policies (encryption, public storage, DB exposure, IAM wildcards, ingress, tags, protection, destructive, secrets) | implemented & verified against synthetic plan fixtures only |
| Conftest missing | warning in non-protected environments; error in protected environments unless `--skip-policy` (recorded in history) |
| checkov | **not run** by any provider workstream (not installed on the development machine) |
| Trivy | no Alibaba checks exist upstream. Trivy had a supply-chain incident in March 2026 ([research §7](research/open-source-landscape.md#7-lint-security-policy-and-cost-tooling-the-claudemd-toolchain)); pin by version and checksum |
| GitHub Actions workflows | written and SHA-pinned; **never run on GitHub** in this project |
| Threat model and security architecture | see [docs/security](security/security-architecture.md) (written in parallel) |

## 8. Discovery (`hermes inventory`)

| Item | Status |
|---|---|
| AWS collectors (27 service groups, read-only guard, IAM policy generated from the same registry) | implemented & verified with Go fakes and against a local moto server; **never run against real AWS** |
| moto demo evidence (`make demo-discovery`, recorded by the migration workstream on 2026-11-19) | A hand-built environment seeded through boto3 was discovered by assuming a role with an ExternalId: 58 resources of 28 types, 78 relationships. All 81 distinct API operations called were on the allowlist, and no secret values appeared in the outputs. In a second run with moto IAM enforcement on, 26 access-denied gaps were recorded (including the 2 seeded denies) and discovery continued. CloudWatch CBOR operations are unsupported by moto and recorded as `unsupported` gaps. `go test -tags moto` passes. I did not re-run the demo for this page |
| GCP or Alibaba as a discovery source | not supported |
| `inventory import` | AWS Config advanced-query output only. Every resource is at most `partial`. Google Migration Center and AWS Application Discovery Service exports are not imported |
| moto coverage | moto has no Cloud Control or Resource Explorer backend, and covers EC2 and SSM only partially ([research C8](research/open-source-landscape.md#10-where-the-evidence-contradicts-or-strains-the-current-architecture)). AWS edge cases that moto does not emulate are untested |
| Data sizes | S3 sizes need `cloudwatch:GetMetricData`. Without it, resources are reported as unknown size (`HCF407`) |
| Inventory files | sensitive: they describe an estate. `--redact` replaces only account IDs |

## 9. Migration (`hermes migrate`) and adoption (`hermes adopt`)

| Item | Status |
|---|---|
| Assess, plan, generate, validate (`spec-coverage`), cutover dry-run | implemented & verified offline (Go tests plus a run on `examples/migration/sample-inventory.json`) |
| `http`, `tcp`, `dns`, `command` validation checks | implemented & verified with local test servers; never run against a real migration |
| Cutover `--execute` | implemented & verified with test hooks; **never run against real systems**. Hermes has no built-in action that touches DNS, databases or source resources. It runs only the hooks you declare |
| Data movement (DMS, Storage Transfer, DTS, Data Online Migration) | not performed by Hermes. Workflow steps name the tool (`automation: provider-tool`); the operator or a declared hook runs it ([ADR 0010](decisions/0010-delegate-data-movement.md)). The 13 workflow templates hold 130 step templates: 101 `manual`, 20 `hermes` and 9 `provider-tool`. The plan generated for the sample inventory (to GCP) has 218 steps: 175 `manual`, 34 `hermes` and 9 `provider-tool` |
| Source decommissioning | not supported, by design: never automated ([ADR 0014](decisions/0014-cutover-hooks-and-no-automatic-decommission.md)) |
| Draft target spec | a **draft**. It covers only what `hermes/v1alpha1` can provision. Everything else is listed in `UNSUPPORTED.md` (25 of 62 resources in the sample run) |
| Transfer cost | indicative range from hand-maintained `PricingAssumptions`, never a quote |
| Hybrid connectivity, VPN, firewall translation | not provisioned by Hermes ([network migration](migration/network-migration.md)) |
| Migration targets | GCP and Alibaba only. AWS as a target of a migration is not supported |
| `hermes adopt` (import-only gate) | implemented & verified with a fake engine; never run against real AWS. `-generate-config-out` is experimental in both engines and its output needs review (it can contain sensitive values) |
| Adopting resources already managed by Terraform or CloudFormation | refused unless `--include-managed`, which creates dual management |

## 10. Application portability

| Item | Status |
|---|---|
| Binding contract (`HERMES_*` environment variables, the same names on every provider) | implemented in all three compositions; verified by the shared contract runs (mock providers). No application has run on a real cloud |
| CloudEvents envelope | a **convention** for applications ([application portability](architecture/application-portability.md)); Hermes does not enforce it |
| Message semantics | at-least-once everywhere; ordering, size limits and retention differ per provider (retention capped at 7 days by MNS and Pub/Sub subscriptions) |
| Database authentication | IAM on AWS and GCP, password on Alibaba. Application code must handle both |

## 11. Testing

See the [testing guide](testing.md) for what each layer proves. Gaps:

- No real-cloud tests (see §0).
- `make integration` was run with OpenTofu, not Terraform.
- Contract runs check output **shape** and environment-variable names, not semantic equivalence of services.
- Policy tests use synthetic plans plus three regression fixtures derived from mock-provider plans of the
  real compositions (`policies/tests/fixtures/*_default`, see [policies/README.md](../policies/README.md#tests));
  none comes from real provider responses.
- `go test ./...` passes: 151 top-level tests (262 including subtests) in the 22 packages that have tests,
  with 0 failed and 0 skipped (measured 2026-11-21, after remediation round 2).
- Terraform compatibility (`make tf-compat`) was not re-run after remediation round 2 (disk space below the
  5 GB threshold): the AWS observability module and composition changed since the last Terraform run. Tests behind the `integration`, `moto` and
  `realaws` build tags are not part of that run.

## 12. Accepted risks and security-review residue

These came out of the internal security review (remediation round 1). Each one is either an accepted, documented
risk or designed but not implemented.

| Item | Status |
|---|---|
| IAM role names are account-wide, so the same project and environment deployed to two AWS regions of one account would collide | known limitation; deploy one region per account or vary `project` |
| Provider lock files are not committed. Rendered roots pin exact provider versions, but hashes are trusted on first use | accepted; commit `.hermes/build/<env>/.terraform.lock.hcl` where hash pinning is required |
| AWS task egress defaults to TCP 443 to `0.0.0.0/0` (needed for image pulls and AWS APIs through NAT); this is an exfiltration path | accepted risk; narrow with `extensions.aws.task_egress_cidrs`, VPC endpoints or an egress proxy |
| AWS public ALB ingress defaults to `0.0.0.0/0` for public services | by design; narrow with `extensions.aws.alb_ingress_cidrs` |
| AWS KMS key policies (environment CMK and the dedicated alert-topic key created without `customer_managed_keys`) rely on `aws:SourceAccount`/`aws:SourceArn` for CloudWatch → SNS; whether CloudWatch sends these keys has not been verified on a real account (if not, alarm e-mails to the encrypted alert topic fail) | not tested on real cloud |
| The GCP plan identity uses `roles/viewer`, which can read objects in buckets that keep default viewer bindings | accepted; documented in the CI identity module |
| The GCP apply identity holds project-wide Secret Manager Admin and Service Account User | accepted; the apply role is inherently broad (see `docs/security/iam-strategy.md`) |
| GCP Cloud SQL logging flags (`log_duration`, `log_hostname`, pgaudit) add log volume and per-connection latency; pgaudit also needs `CREATE EXTENSION pgaudit` | accepted for security posture; tune per workload |
| Alibaba OSS access logging (requires a dedicated log bucket) | designed, not implemented |
| Conftest policies cannot evaluate values that are unknown until apply (they warn instead) and cannot enforce customer-managed keys everywhere | limitation of plan-time policy |
| Cutover hook output and validation `command` check output are not persisted by Hermes (exit code, byte count and sha256 only), but they are still visible in the terminal and in CI logs | accepted; hooks and checks must not print secrets |
| Checkov and moto are pinned by version, not by hash | accepted; gitleaks, trivy and conftest are SHA-256 pinned in CI |

## 13. Design limitations and roadmap (independent QA review, 2026-11-20)

Design criticisms D-1 to D-9 from the independent review. None is a bug in the current behaviour; each limits
how far the design scales or how much the safety story can be trusted. Status: **design limitation /
roadmap** unless stated otherwise.

| Id | Limitation | Consequence today | Roadmap |
|---|---|---|---|
| D-1 | One monolithic composition and **one state per environment** (network, compute, database, storage and messaging together); `infra/contract/variables.tf` is copied into each composition | Every plan refreshes everything (slow at tens of services, large blast radius, one lock); no way to split state (for example network vs apps); adding a resource type touches three compositions, the Go capability matrices and the schema | Component splitting with cross-state references, or delegate stacking to Atmos/Terragrunt |
| D-2 | No module-evolution story: no `moved` blocks, no convention or test requiring them, and only `v1alpha1` with no spec conversion | A refactor of module internals shows up as delete/create; the only guard is `--allow-destructive`, which operators may learn to pass | A "no destroy on upgrade" contract test (plan old → new module versions with mock providers and fail on replace); `moved` blocks as a review rule; a spec conversion path before `v1beta1` |
| D-3 | Policies are tested beyond synthetic fixtures only by the composition-derived regression fixtures (mock-provider plans of the default configuration); IAM documents built from same-plan resources are unknown at plan time, so HCF-POL-005 warns instead of denying on first plans | Configurations other than the default are covered only by synthetic fixtures; real provider-computed values are not exercised | More generated fixtures (protected, CMK, every capability) and a real-plan smoke test |
| D-4 | The portability score is a **maintained lookup**, not a measurement: the capability matrices in `internal/providers` are hand-maintained, all features weigh the same, and "100" means "our module implements it" | A drift between matrix and modules goes unnoticed; scores compare implementations, not cloud behaviour | Generate the matrix from module tests; cross-check the Go size rounding against `tofu test` outputs |
| D-5 | **Mock-only testing** leaves the expensive risks untested: discovery IAM action names (no IAM evaluator), KMS key-policy principals/conditions, the SAE trust principal, queue scale-from-zero alarm timing, Cloud Run/SAE rounding, the ADOT read-only filesystem | These fail only on a real account | One opt-in real-cloud smoke per provider (plan-only, read-only identity) |
| D-6 | **Approval granularity invites rubber-stamping**: the sample migration plan has 218 steps and 132 approval gates for 62 resources | Reviewers approve many low-value gates | Fewer, higher-value gates bound to content digests (plans, hooks and policy bundles already are; validation reports are recomputed from their results but not signed) |
| D-7 | **Local-file trust model**: `meta.json`, validation reports, cutover state and history are unsigned JSON in `.hermes/` | Anyone who can write `.hermes/` can forge them (partly mitigated: plan id = sha256 of the plan file, future `created_at` refused, report pass/fail recomputed, hooks digest approval); fine on a laptop, weaker for shared CI runners | One append-only audit trail with the digest of every input, verified at apply/execute; optional signing |
| D-8 | **No DNS, custom domains, TLS automation or multi-region**: HTTPS needs a pre-made certificate, private services have no URL, IAM role names collide across regions | Traffic cutover, the riskiest step, is entirely operator-written hook scripts: Hermes orchestrates your scripts (gates, ordering, state, audit) but does not provide the cutover mechanism | DNS/certificate capabilities in the spec; region-scoped names |
| D-9 | **No central redaction layer**: secret handling is per path (collectors keep env names only, the Cloud Control fallback stores identifiers and tags only, adoption excludes secret-bearing types, hook and check output is digested) | Each new persisted artefact must get its own treatment; the AWS Config import still stores a sanitized property bag | One redaction layer applied to every persisted artefact, with canary tests |

## 14. Research caveats

The research report lists what it could not verify ([§12](research/open-source-landscape.md#12-not-verified-in-this-pass)).
The ones that matter for Hermes are the Terraform support policy, Pub/Sub retention maximums, OSS-backend
locking through Tablestore, breaking changes in Alibaba provider v2.0.0, and SAE coverage in third-party policy
tools. These §10 corrections are visible in the code:

- C1/C2: the engine floor is OpenTofu ≥ 1.10 and Terraform ≥ 1.11, with a version check in `internal/execution`.
- C4: client-side state encryption is rejected on Alibaba (`HCF208`).
- C5: topic retention is capped at 1–168 h in `infra/contract/variables.tf`.
- C6: `max_delivery_attempts` must be 0 or 5–100.

C3 (credentials out of state) is handled per provider rather than through write-only arguments. AWS uses the
RDS-managed master-user secret. On GCP and Alibaba, credentials are set out-of-band. None of this is verified on a
real cloud. The engine floor (1.10) is below the 1.11 the report suggested for write-only arguments. C11
(Terragrunt and Atmos interoperability) remains **designed, not tested**.

## 15. Hermes UI

| Item | Status |
|---|---|
| Connect AWS and Google Cloud, live checks, discovery to plan as a job (`hermes ui`) | implemented. AWS checks verified on a real sandbox account; Google checks verified against a fake Google API only |
| Deploy a new service from a form, with validation and a rendered OpenTofu preview | designed (ADR 0016 phase 2), not implemented |
| Apply, destroy and approvals from the page | designed (phase 3), not implemented. Applies would stay saved-plan only with approval by plan id |
| Migration execution from the page: provisioning the target, data movement (for S3 through Storage Transfer Service with a role ARN), validation, gated cutover | designed (phase 4), not implemented |
| Hosted, multi-user version | not supported. It would hold cross-account access to customer environments and needs authentication, tenant isolation, audit and its own threat model |
| Automated browser (end-to-end) tests | not implemented. Views are covered by render smoke tests; the server by unit tests with the race detector; layout was checked by hand |
| Google Cloud permission preflight | a list of 14 permissions checked with `testIamPermissions`. It is a preflight, not a guarantee that apply will succeed |
