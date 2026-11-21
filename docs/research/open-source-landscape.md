# Open-source and ecosystem landscape

| | |
|---|---|
| Status | Research input, not a decision. Decisions go in `docs/decisions/`. |
| Evidence retrieved | 2026-11-18 |
| Method | Public documentation and release notes, read through web search and fetch. Public GitHub repository metadata from the GitHub REST API. No cloud API calls, no git operations, no credentials used. |
| Scope | Tools Hermes could reuse, compete with, or depend on: IaC engines and orchestrators, multi-cloud spec projects, discovery and inventory, data-migration services, lint/security/cost tooling, local AWS emulation, and application portability runtimes. Also lists where the evidence contradicts `CLAUDE.md`, `docs/architecture/data-contracts.md` and `infra/contract/variables.tf`. |

## Evidence labels

Every claim carries one label, matching the honesty rule in `CLAUDE.md`:

- **[V] verified**: read in official docs, an official API reference, or the project's own repository during this pass.
- **[S] secondary**: only from blogs, aggregators or search-result summaries. Probably right; confirm before you depend on it.
- **[U] unverified**: from prior knowledge, or the sources conflict. Treat it as a lead, not a fact.

Product and project names are used only to identify tools. This document contains no customer- or employer-specific information.

---

## 1. Bottom line

1. **No existing project covers Hermes's whole scope.** That scope is a capability-level spec deployed to AWS, GCP *and* Alibaba Cloud managed services, with no Kubernetes control plane, plus a read-only, gated migration workflow. The closest projects (Score, Radius, Crossplane, KubeVela) are workload-centric or Kubernetes-centric, and none of them has first-class Alibaba Cloud targets (§4). This supports rule 7 ("don't build a wrapper for its own sake"), as long as Hermes stays thin.
2. **The engine floor in `CLAUDE.md` (OpenTofu ≥ 1.8, Terraform ≥ 1.8) is too low for features the contracts already assume.** These are S3 native locking, write-only arguments for keeping secrets out of state, and ephemeral resources. OpenTofu 1.8 left security support on 2025-12-09 (§2, C1–C3).
3. **Alibaba Cloud is the weakest-served target in every tool category.** It has no official TFLint ruleset and no Trivy check library. Checkov has 43 checks for it versus 366 for AWS. It has no Infracost pricing, no OpenTofu state-encryption key provider, no Terragrunt backend bootstrap, no Dapr pub/sub component, and only a young Crossplane provider (§7, §9, C4, C9, C11).
4. **The AWS-native discovery paths differ sharply in fidelity.** Cloud Control `GetResource` returns full models. `ListResources` sometimes returns only partial properties, and some types need parent identifiers. Resource Explorer can add only `tags` to its results. AWS Config advanced query returns only recorded resources and only top-level scalars for `SELECT *`. The `Inventory.completeness` field needs per-collector semantics (C7).
5. **Bulk import discovery is now engine-specific.** Terraform ≥ 1.14 has `terraform query` with `list` blocks. OpenTofu, the Hermes default, does not; its feature request is still open. Generating import blocks from the Hermes `Inventory` is therefore real added value, not duplication (§5.3, C13).
6. **Several tools are now archived or have new access conditions:**
   - archived: `localstack/localstack` (2026-03-23; its image now needs an auth token), CDK for Terraform (2025-12-10), Terraformer
   - login or subscription now required: CloudQuery (login for new releases, paid AWS tables), Crossplane official providers (subscription for versions published after 2025-03-25), and Trivy GitHub Actions, which had a supply-chain compromise in March 2026 (§5, §7, §8).
7. **moto, the planned discovery-demo backend, has no Cloud Control or Resource Explorer backend.** In AWS Config it implements single-account `SelectResourceConfig` but not the aggregate variant. The demo can exercise only the `aws-api` collector and single-account Config (C8).

---

## 2. IaC engines

| | OpenTofu | Terraform |
|---|---|---|
| License | MPL-2.0 [V] | BUSL-1.1 [U] (the GitHub API reports `NOASSERTION`) |
| Governance | Linux Foundation project, CNCF **Sandbox** since 2025-04-23; no incubation move found [S] | Single vendor |
| Latest line | 1.13 (released 2026-09-30, latest 1.13.9 on 2026-11-05) [S] | 1.17.0, around 2026-10-21 [S] |
| Stable releases, last 12 months (GitHub) | 35 [V] | not measured reliably (the API call failed) |

**OpenTofu support windows** (endoflife.date, [S]):

| Line | Released | Security support ends |
|---|---|---|
| 1.13 | 2026-09-30 | 2027-08-01 |
| 1.12 | 2026-05-14 | 2027-02-01 |
| 1.11 | 2025-12-09 | listed as 2026-08-19 (conflicts with the usual pattern; treat as [U]) |
| 1.10 | 2025-06-24 | 2026-09-30 |
| 1.9 | 2025-01-09 | 2026-05-14 |
| **1.8** | 2024-07-29 | **2025-12-09** |

**Features relevant to Hermes, by engine:**

| Feature | OpenTofu | Terraform | Relevance |
|---|---|---|---|
| `mock_provider` in tests | 1.8 [U] | 1.7 [U] | `make tf-test` |
| S3 native locking (`use_lockfile`) | 1.10 [S] | experimental in 1.10, GA in 1.11; DynamoDB locking deprecated [S] | `state.lock_table` default (C2) |
| Ephemeral resources | 1.11 [V] | 1.10 [S] | secret handling (C3) |
| Write-only arguments (`*_wo`, `*_wo_version`) | 1.11 [V] | 1.11 [S] | secrets kept out of state (C3) |
| `enabled` meta-argument | 1.11 [S] | not found | engine-specific; keep out of modules |
| `lifecycle { destroy = false }`, dynamic `prevent_destroy` | 1.12 [S] | not found | engine-specific; useful for handing resources between stacks |
| Complete multi-platform lock file on first `init` | 1.12 [S] | — | supports exact provider pins |
| Client-side state encryption | yes; key providers `pbkdf2`, `aws_kms`, `gcp_kms`, `azure_vault`, `openbao`, `external` [V] | no | `state.encryption` (C4) |
| `-generate-config-out` | since 1.6, experimental [S] | experimental [S] | migration import |
| `query` command + `list` blocks (bulk discovery and import) | **no**; open issue opentofu/opentofu#3787 (2026-02-20) [V] | 1.14+ [S] | C13 |
| Experimental `-lint` | 1.13 [S] | — | possible addition to `make tf-validate` |
| Native policy (`-policies`) | — | "Terraform Policy" GA in 1.17 [S] | engine-specific; conftest on plan JSON stays engine-neutral |
| Variables in `required_providers` | — | 1.17 [S] | engine-specific |

Breaking changes worth tracking:
- OpenTofu 1.13 removed WinRM from provisioners and changed `base64gzip` output bytes. It is also the last line with 32-bit builds [S].
- The Alibaba Terraform provider jumped from 1.x to **v2.0.0 on 2026-10-12** (v2.2.0 on 2026-11-06). v2.0.0 added an ephemeral resource that needs Terraform ≥ 1.10 [V]. Module constraints written as `~> 1.x` will not pick up 2.x. The full list of 2.0 breaking changes was not reviewed [U].

**Assessment:** OpenTofu as the default engine fits the ecosystem. Terragrunt also defaults to invoking `tofu` [S]. The open question is the compatibility floor (§10).

---

## 3. Orchestration layers over the engine

| Tool | License | Model | Multi-cloud reality | Notes |
|---|---|---|---|---|
| **Terragrunt** | MIT [V] | HCL units and stacks (`terragrunt.stack.hcl`); `remote_state` and `generate` blocks keep backends and providers DRY | Works with any provider. `remote_state` bootstrap (bucket creation) supports only `s3`, `gcs` and `azurerm` (experimental); there is **no OSS** [S] | 1.0 shipped 2026-03-30 with a backwards-compatibility guarantee for flags, HCL and output formats. Stacks GA since v0.78.0. Bootstrap is opt-in (`--backend-bootstrap`). Its commercial CI layer charges for drift detection [S]. 38 stable releases in 12 months [V]. |
| **Atmos** | Apache-2.0 [V] | YAML stacks with imports, inheritance (`metadata.inherits`, abstract components), vendoring, workflows, and JSON Schema / OPA validation of component config via `settings.validation` [S]. Generates `backend.tf.json` and `providers_override.tf.json` into component directories [V] | The tool is cloud-neutral and its docs cover S3, GCS and Azure backends [V]. Its component library is AWS-only: 183 `aws-*` repos and no GCP or Alibaba ones [V] | Paid SaaS add-on for ordered deployments and drift detection, from a free tier up to custom enterprise pricing [S]. 47 stable releases in 12 months [V]. |
| **CDK for Terraform** | MPL-2.0 | Programmatic IaC | — | **Archived and deprecated 2025-12-10** [V repo archived; date S]. Community forks exist [S]. Do not adopt. |
| **Pulumi** | Apache-2.0 [V] | General-purpose-language IaC engine | Broad | A different engine, not a wrapper. Outside the "OpenTofu or Terraform does all the work" model. Not evaluated further. |

**Overlap with Hermes:** Terragrunt and Atmos solve DRY configuration and environment layering, which Hermes's overlay and normalization step also does. Neither has a capability-level spec, provider selection, plan safety gates, or a migration toolkit. The `variables.tf` header says the modules are usable directly from Terragrunt or Atmos. That claim is plausible from the evidence but untested (C11).

---

## 4. "Describe once, deploy anywhere" projects

| Project | Status | What it abstracts | Targets | Fit against the Hermes spec |
|---|---|---|---|---|
| **Score** (`score-spec`) | CNCF Sandbox since 2024-07 [S]; Apache-2.0 [V] | A workload: containers plus *resource dependencies* resolved by provisioners | `score-compose` (Docker Compose) and `score-k8s` (Kubernetes manifests) [S]; no first-party cloud managed-service targets found [U] | Closest in spirit to `workloads.compute.services`. It is a candidate *interop format* (import or export of the service section), not a replacement. |
| **Radius** | CNCF Sandbox [S]; Apache-2.0 [V] | Applications plus "recipes" written in Terraform or Bicep | AWS, Azure and Kubernetes. GCP Terraform recipes are **conflicting** in its own docs: the FAQ says unsupported, the overview says any provider. Alibaba is not found [S] | Needs a Kubernetes-hosted control plane. |
| **Crossplane v2** | Apache-2.0 [V]; v2 adds namespaced managed resources, compositions of any Kubernetes resource, and "Operations"; upgrade only from v1.20 [S] | Kubernetes CRDs as the cloud API | AWS and GCP via upjet providers. The Alibaba upjet provider dates from 2025-03, has about 27 API groups (including `oss`, `rds`, `vpc`, `ram`, `kms`, `messageservice`, `fcv3`, `sls`) and roughly 5 regular contributors [V] | A different engine (a Kubernetes reconciler instead of plan/apply). Since 2025-03-25, official provider builds need a subscription for new versions; free `crossplane-contrib` builds are the alternative [S]. |
| **KubeVela** | Apache-2.0 [V] | OAM applications | Kubernetes-centric | Not evaluated in depth. |

**Assessment:** None of these provides a non-Kubernetes, capability-level spec that renders to managed services on all three Hermes targets. The differentiation in `CLAUDE.md` holds, but only while Hermes stays a thin renderer plus gates and does not grow into a reconciler.

---

## 5. Discovery and inventory

### 5.1 AWS-native read paths (what a read-only collector can rely on)

| Path | What it returns | Limits that matter for `Inventory` |
|---|---|---|
| **Cloud Control API** `ListResources` / `GetResource` | `ListResources`: primary identifier plus "part or all" of the properties, depending on type (for example, Kinesis streams return only `Name`). `GetResource` returns the full current state [V] | About 40 types need `ResourceModel` parent properties to list, for example `AWS::EKS::AddOn` (ClusterName), `AWS::ElasticLoadBalancingV2::Listener` (LoadBalancerArn), `AWS::ECS::TaskSet`, `AWS::SSO::PermissionSet` and `AWS::WAFv2::WebACL` (Scope) [V]. Only types whose ProvisioningType is FULLY_MUTABLE or IMMUTABLE are supported [S]. Type names match the `Inventory.type` convention. |
| **AWS Config advanced query** (`SelectResourceConfig`, `SelectAggregateResourceConfig`) | SQL-subset queries over current configuration items; aggregators give multi-account, multi-region views [V] | Only *recorded* resource types (`ResourceNotRecorded` items are excluded). Not every Config type is queryable. `SELECT *` returns only top-level scalars (`accountId`, `arn`, `resourceType` and so on), so `configuration` must be selected explicitly. No `JOIN`, `DISTINCT` or `FROM`. No deleted resources. Suffix wildcards only [V]. |
| **Resource Explorer** | ARN, type, region, service, and tags | A view's `IncludedProperty` accepts **only `tags`** [V]. Multi-account search needs AWS Organizations trusted access and a delegated administrator [S]. Free-text search is capped at 1,000 results [S]. Uses its own type naming (for example `ec2:instance`), which must be mapped to CloudFormation names [U]. |

### 5.2 Third-party inventory tools

| Tool | License | Status | Fit |
|---|---|---|---|
| CloudQuery | CLI MPL-2.0 [V] | Login or API key required for integration versions released after a cut-off; free quota of 1M rows applies to premium tables; paid AWS tables [S] | A vendor account in the loop conflicts with "agentless, no external dependency" discovery. Not recommended as a dependency. |
| Steampipe | CLI **AGPL-3.0**; plugins Apache-2.0 [V] | Active; has an AWS plugin and a small Alibaba plugin (15 stars) [V] | Usable as an optional external cross-check. The AGPL CLI is a concern for embedding, not for side-by-side use. |
| Cartography | Apache-2.0, `cartography-cncf` org [V] | Active | Graph of relationships; a reference for the `relationships[]` vocabulary. |
| Terraformer | Apache-2.0 | **Archived** [V] | Do not depend on it. |
| former2 | MIT, AWS-only, browser-based [V] | Active (2026-07) [V] | Reference only. |
| Prowler, Cloud Custodian | Apache-2.0 [V] | Active | Posture and policy tools, not migration inventories. Not evaluated in depth. |
| Komiser | Repo moved; license reported as `NOASSERTION` [V] | — | License unclear; skip. |

### 5.3 Engine-native discovery and import
- Terraform ≥ 1.14 has `list` blocks in `.tfquery.hcl` files. `terraform query -generate-config-out=…` writes `resource` and `import` blocks. Coverage depends on the provider implementing list resources; the AWS provider is still expanding its coverage [S].
- OpenTofu has no `query` or `list` support (issue #3787, open) [V].
- `-generate-config-out` exists in both engines but is **experimental**. It may emit invalid configuration for complex schemas and write sensitive values in plaintext. In OpenTofu it is reported as unsupported with `for_each` on import blocks [S].

### 5.4 Cloud-vendor discovery that targets AWS
GCP Migration Center "AWS discovery" is **Preview**; only EC2 and RDS are GA. It authenticates with an **AWS access key ID and secret**, stored in Secret Manager, and its docs mention no role assumption. It needs the Cloud Run Admin API and a service account with `migrationcenter.admin`. Metrics cover 30 days, except database memory, which covers 7 [V]. This is useful for sizing data, but the Hermes `Inventory` contract (role assumption with ExternalId) is the stronger security posture.

---

## 6. Data-migration services (used by `MigrationWorkflow` steps as `automation: provider-tool`)

| Path | Service | Mechanism and limits | Source credential |
|---|---|---|---|
| RDS/Aurora PostgreSQL → Cloud SQL / AlloyDB | Google Database Migration Service | Continuous migration via **pglogical**. DDL is not replicated unless run through `pglogical.replicate_ddl_command`. In standard mode, UPDATE/DELETE on tables without a primary key are not migrated. No generated columns on PG ≥ 12. Major-version upgrades go through `pg_restore` [S]. Amazon RDS PostgreSQL 9.6.10+ through 18 listed as sources [S] | Database user |
| RDS/Aurora MySQL → Cloud SQL | Google Database Migration Service | RDS and Aurora 5.6, 5.7, 8.0 and 8.4 listed [S] | Database user |
| RDS PostgreSQL → ApsaraDB RDS PostgreSQL | Alibaba DTS | Schema, full and incremental migration. Source must be ≥ 10.4 with `rds.logical_replication = 1`. Creates `dts_sync_*` replication slots. No DDL on source objects before incremental sync starts. A source failover can break the task [S] | Database user |
| RDS MySQL → ApsaraDB RDS MySQL | Alibaba DTS | Full copy, then binlog-based incremental [S] | Database user |
| S3 → Cloud Storage | Google Storage Transfer Service | Agentless. Supports a **federated identity role ARN** as well as access keys. Event-driven mode reads S3 event notifications from SQS; source deletions are not propagated [S] | Role (no long-lived key) |
| GCS → S3 (reverse direction) | AWS DataSync Enhanced mode | Agentless since 2025-05 for GCS↔S3; agents still needed for EFS/FSx or Basic mode [S] | — |
| S3 → OSS | Alibaba Data Online Migration | **One bucket per task**, no whole-account migration. Needs an **IAM user access key pair**. S3 IP allowlists are not supported. S3 charges internet egress [S] | **Long-lived access key** |
| S3 → OSS (offline tool) | ossimport | Free and read-only on the source; does not verify data after copying [S] | Access key |

This supports the existing workflow design. `enable-logical-replication` with `source_impact: modifies-source-config` and "requires reboot" matches both the Google and the Alibaba requirements.

---

## 7. Lint, security, policy and cost tooling (the `CLAUDE.md` toolchain)

| Tool | License | AWS | GCP | Alibaba | Notes |
|---|---|---|---|---|---|
| TFLint | MPL-2.0 [V] | official ruleset | official ruleset | **none**: official rulesets are only aws, azurerm, google, terraform and opa; one 0-star community repo [V] | |
| Checkov | Apache-2.0 [V] | 366 checks | 129 checks | **43 checks** [V] (count of Terraform resource check files) | |
| Trivy (`trivy config`) | Apache-2.0 [V] | yes | yes | **no `alicloud` folder** in trivy-checks; clouds covered are aws, azure, cloudstack, digitalocean, github, google, kubernetes, nifcloud, openstack, oracle [V] | **Supply-chain compromise, 2026-03-19/20** (CVE-2026-33634): a malicious v0.69.4 binary, and 75–76 of 76–77 `trivy-action` tags plus all `setup-trivy` tags repointed [S]. |
| conftest / OPA | conftest: license `NOASSERTION` in the API [V] | engine-neutral on plan JSON | | | The only route to Alibaba policy parity. |
| gitleaks | MIT [V] | n/a | | | |
| Infracost | Apache-2.0 [V] | yes | yes | **no**; AWS, Azure and Google only [S] | Hand-maintained `PricingAssumptions` is the right choice for Alibaba. Infracost could optionally cross-check AWS and GCP rendered plans. |

---

## 8. Local AWS emulation (for `make demo-discovery` and tests)

- **LocalStack:** the `localstack/localstack` repo is **archived** (2026-03-23) [V]. The single image now needs `LOCALSTACK_AUTH_TOKEN`, and the vendor no longer commits to regular community updates [S]. This supports the choice of moto.
- **moto** (Apache-2.0, active) [V]. Coverage by operation count from `IMPLEMENTATION_COVERAGE.md`, retrieved 2026-11-18 [V]:

| Service | Implemented | | Service | Implemented |
|---|---|---|---|---|
| ec2 | 233/809 (28%) | | sqs | 20/23 (86%) |
| rds | 85/168 (50%) | | sns | 33/42 (78%) |
| s3 | 77/118 (65%) | | secretsmanager | 21/23 (91%) |
| ecs | 45/88 (51%) | | kms | 40/54 (74%) |
| iam | 119/180 (66%) | | route53 | 30/71 (42%) |
| sts | 7/11 (`assume_role` ✓) | | elbv2 | 33/51 (64%) |
| lambda | 46/88 (52%) | | dynamodb | 39/58 (67%) |
| logs | 52/118 (44%) | | ssm | 41/152 (26%) |
| config | 38/102 (37%): `select_resource_config` ✓, `select_aggregate_resource_config` ✗ | | resourcegroupstaggingapi | 5/9 (`get_resources` ✓) |
| organizations | 41/63 (65%) | | cloudformation | 33/90 (36%) |
| **cloudcontrol** | **not present** | | **resource-explorer-2** | **not present** |

---

## 9. Application portability runtimes

**Dapr** (Apache-2.0) [V]. Cloud-specific components in `components-contrib`, read from the repo directory listing [V]:

| Building block | AWS | GCP | Alibaba |
|---|---|---|---|
| Pub/sub | `snssqs` | `pubsub` | **none** |
| State | `dynamodb` | `firestore` | `tablestore` |
| Secret stores | `parameterstore`, `secretmanager` | `secretmanager` | `parameterstore` (OOS) only; no KMS secrets manager |
| Bindings | dynamodb, kinesis, s3, ses, sns, sqs | bucket, pubsub | oss, sls, tablestore, dingtalk |

**External Secrets Operator** paused releases in 2025-08 and later resumed, with 2.x lines now current [S]. Its Alibaba KMS provider was "alpha" in an older stability matrix, and the current level is unverified [U]. It matters only if `compute.type: kubernetes` is ever implemented.

**Implication:** the binding contract's choice of native identifiers plus OTLP, CloudEvents and PostgreSQL is sound. Do not assume Dapr gives messaging portability on Alibaba.

---

## 10. Where the evidence contradicts or strains the current architecture

> **Status note.** This table records what the initial engineering brief said when the research was done. The findings were acted on: `CLAUDE.md` and ADR 0002 now set the engine floor to OpenTofu ≥ 1.10 / Terraform ≥ 1.11 (S3 native locking). References to the earlier "≥ 1.8" floor below describe the original brief, not the current repo.

Severity: **High** means it will break a stated guarantee or produce invalid deployments. **Medium** means a contract gap that will surface during implementation. **Low** means documentation or hygiene.

| # | Sev | Where | What the repo says | What the evidence says | Suggested action |
|---|---|---|---|---|---|
| C1 | High | `CLAUDE.md` Conventions | Modules compatible with OpenTofu ≥ 1.8 and Terraform ≥ 1.8 | OpenTofu 1.8 lost security support on 2025-12-09 and 1.9 on 2026-05-14 [S]. 1.12 and 1.13 are supported now [S]. | Raise the floor to at least OpenTofu 1.11 / Terraform 1.11 (see C2, C3), and pin rendered roots to supported lines. Add an engine version check in `internal/execution`. |
| C2 | High | data-contracts §1 `state.lock_table` | "default uses S3 native lockfile" | `use_lockfile` needs OpenTofu ≥ 1.10 [S] and Terraform ≥ 1.11 (experimental in 1.10) [S]. On a ≥ 1.8 floor, the default would fail or run unlocked. | Raise the floor, or have the renderer emit `dynamodb_table` when the detected engine predates support. |
| C3 | High | `CLAUDE.md` rule 2; contract `database` | No secrets in state; secret values never managed by IaC | Keeping DB credentials out of state through IaC needs write-only arguments (`password_wo` with `password_wo_version`; OpenTofu 1.11 [V], Terraform 1.11 [S]) or provider-managed credentials. Write-only support is per provider resource [S]. With a ≥ 1.8 floor, modules cannot use either, and any `random_password` lands in state. | Raise the floor to 1.11. For each provider, document how the master or app credential is created without passing through state, and record it in `docs/limitations.md` until tested. |
| C4 | Medium | data-contracts §1 `state.encryption.kms_key` | A provider-agnostic "key id/ARN/resource name" | OpenTofu key providers are `pbkdf2`, `aws_kms`, `gcp_kms`, `azure_vault`, `openbao` and `external`; there is **no Alibaba KMS** [V]. On Alibaba, client-side encryption would need a passphrase (a secret, so rule 2 applies) or a custom `external` key-provider program. | Make `kms_key` invalid for `provider: alibaba` (error HCF2xx), or design and test an `external` key provider. |
| C5 | Medium | data-contracts §1 messaging; `variables.tf` `messaging` | `retention_hours: 1–336` | 336 h equals SQS's 14-day maximum. Alibaba MNS/SMQ queue `MessageRetentionPeriod` is 60–604,800 s, a **7-day** maximum [S]. Pub/Sub subscription retention also tops out at 7 days, though topic retention can be longer [U]. | Validate per provider, or cap the contract at 168 h. There is no validation in `variables.tf` today. |
| C6 | Medium | data-contracts §1; `variables.tf` | `max_delivery_attempts`: 0 means no dead-letter, otherwise N; no range | A Pub/Sub dead-letter policy needs **5–100** [S]. Values 1–4 or above 100 fail on GCP at apply time. | Add `validation` (0, or 5–100) to the contract, or make it adapter-specific. |
| C7 | Medium | data-contracts §3 `collector`, `completeness` | `aws-config` and `resource-explorer` collectors alongside `aws-api`; `completeness: full\|partial\|identifier-only` | Resource Explorer can add only `tags` [V], so it can never be `full`. Config `SELECT *` returns only top-level scalars, excludes unrecorded types, and not every type is queryable [V]. Cloud Control lists partial properties for some types and needs parent identifiers for about 40 child types [V]. | Define a maximum `completeness` per collector: resource-explorer gives at most identifier-only plus tags; aws-config gives partial unless `configuration` is selected and the type is recorded. Map Resource Explorer type names to CloudFormation names. Record unrecorded types as `gaps[]` with `reason: not-enabled`. |
| C8 | Medium | `CLAUDE.md` commands (`make demo-discovery`, moto) | A moto-backed AWS discovery demo | moto has no Cloud Control or Resource Explorer backend, and in Config only the single-account select [V]. EC2 is 28% and SSM 26% covered [V]. | Scope the demo to the `aws-api` collector. Test the other collectors with the `fixture` collector and Go fakes, labelled "implemented, not tested against real cloud". Treat moto `NotImplemented` errors as `gaps[].reason: unsupported`, never as an empty result. |
| C9 | Medium | `CLAUDE.md` toolchain (`tflint`, `checkov`, `trivy config`) | One toolchain for all providers | Alibaba has no TFLint ruleset and no Trivy checks, and Checkov has 43 checks against 366 for AWS [V]. Trivy had a tag-repointing supply-chain attack in 2026-03 [S]. | Write Alibaba guardrails as conftest/OPA policies in `policies/` (encryption, public access, deletion protection, tags). Pin Trivy by version and checksum (not v0.69.4) and pin every GitHub Action by full commit SHA. |
| C10 | Medium | `CLAUDE.md` rule 2; data-contracts §5 | No secrets anywhere; source-touching steps need an approval gate | Alibaba Data Online Migration (S3 → OSS) and GCP Migration Center AWS discovery both need a **long-lived AWS IAM access key** [S/V]. Google Storage Transfer can use a federated role instead [S]. | In the Alibaba `object-storage` workflow, model "create a temporary read-only IAM user key" as `source_impact: modifies-source-config` with `gate: approval` and a matching revoke step in `decommission`. Set the key out-of-band; never persist it in Hermes artifacts. |
| C11 | Low | `infra/contract/variables.tf` header | Modules usable from plain OpenTofu/Terraform, Terragrunt or Atmos | Terragrunt cannot bootstrap an OSS backend (s3, gcs and azurerm only) [S]. The Atmos component library is AWS-only [V]. Nothing here has been tested. | Label the claim "designed, not tested". Point Alibaba users to `infra/bootstrap/alibaba`. Add one Terragrunt example per provider before keeping the claim. |
| C12 | Low | data-contracts §4 example `references` | `https://cloud.google.com/database-migration/docs/postgres/overview` | `cloud.google.com/...` docs now 301-redirect to `docs.cloud.google.com/...`. That `/postgres/overview` path returned 404 on the new host during this pass [V]. | Use `docs.cloud.google.com` URLs in the catalog and add a link check to CI. Use `.../postgres/known-limitations` as a confirmed live page [S]. |
| C13 | Low (informational) | `CLAUDE.md` "the engine does all … import work" | Import is delegated to the engine | Bulk discovery and import (`query` with `list` blocks) exists only in Terraform ≥ 1.14 [S]. OpenTofu, the default, lacks it [V]. `-generate-config-out` is experimental in both [S]. | Keep the engine doing the import itself, but have Hermes generate `import` blocks from the `Inventory`, which already carries ids and CloudFormation types. This is rule-7-compliant added value. Never rely on `-generate-config-out` output without review, because it can contain sensitive values. |

**Evidence that supports the current design:**
- `engine.binary: auto` preferring `tofu` matches the ecosystem default [S].
- Read-only, agentless discovery using role assumption with ExternalId is stronger than the vendor discovery tools, which need static keys [V].
- The PostgreSQL workflow's logical-replication prerequisite matches both target migration services [S].
- Hand-maintained `PricingAssumptions` is necessary for Alibaba [S].
- Choosing moto over LocalStack is right given LocalStack's archival [V/S].

---

## 11. Reuse versus build

| Need | Recommendation |
|---|---|
| Plan, apply, state, import | **Reuse** OpenTofu or Terraform (already decided). |
| Environment layering and DRY config | **Build, small.** It is already in the spec overlay. Interop with Terragrunt and Atmos stays optional and untested (C11). |
| Capability spec → provider rendering | **Build.** No project covers all three targets without Kubernetes (§4). Consider a Score import or export for the service section later. |
| Plan safety gates | **Build** on `show -json`, engine-neutral. Optionally run conftest on the plan JSON. Avoid engine-specific policy features. |
| AWS discovery | **Build** collectors on Describe/List/Get APIs and Cloud Control `GetResource`. Treat Config and Resource Explorer as accelerators with lower completeness (C7). Do not depend on CloudQuery or Terraformer. |
| Import generation | **Build** the Inventory → `import` block generation (C13). |
| Data movement | **Reuse** the provider migration services (§6) as `automation: provider-tool` steps behind gates. |
| Security and lint | **Reuse** TFLint, Checkov, Trivy, gitleaks and conftest. **Build** Alibaba OPA policies (C9). |
| Cost | **Reuse** Infracost optionally for AWS and GCP. **Build** the `PricingAssumptions` path (required for Alibaba). |
| Local AWS emulation | **Reuse** moto; fill gaps with fixtures (C8). |
| App portability | **Reuse** OTLP, CloudEvents and PostgreSQL. Do not promise Dapr-based portability on Alibaba (§9). |

---

## 12. Not verified in this pass
- Terraform's support policy and the official license file text. The 1.15–1.17 details came from secondary sources.
- Pub/Sub subscription and topic retention maximums (C5, GCP side).
- How the OSS backend locks (Tablestore) in current OpenTofu and Terraform releases. Whether OpenTofu still ships the OSS backend unchanged.
- Whether Resource Explorer's `ListResources` API lifts the 1,000-result search cap.
- Breaking changes in Alibaba Terraform provider v2.0.0.
- Alibaba serverless-container (SAE) coverage in Crossplane, Dapr and policy tools.
- CNCF maturity of Score, Radius and Cartography beyond the Sandbox announcements.

---

## Sources

IaC engines and orchestration
- OpenTofu 1.11 release post — https://opentofu.org/blog/opentofu-1-11-0
- OpenTofu write-only attributes — https://opentofu.org/docs/language/ephemerality/write-only-attributes/
- OpenTofu state encryption — https://opentofu.org/docs/language/state/encryption/
- OpenTofu 1.10 what's new (S3 native locking) — https://opentofu.org/docs/v1.10/intro/whats-new/
- OpenTofu 1.12 summary — https://spacelift.io/blog/opentofu-1-12
- OpenTofu 1.13 coverage — https://linuxiac.com/opentofu-1-13-iac-tool-brings-windows-arm64-support-smarter-planning/ , https://www.infoq.com/news/2026/10/opentofu-1-13-unknown-values/
- OpenTofu lifecycle — https://endoflife.date/opentofu , https://eosl.date/eol/product/opentofu/
- OpenTofu CNCF page — https://www.cncf.io/projects/opentofu/
- OpenTofu query/list feature request — https://github.com/opentofu/opentofu/issues/3787
- Terraform bulk import — https://developer.hashicorp.com/terraform/language/import/bulk
- Terraform query command — https://developer.hashicorp.com/terraform/cli/commands/query
- Terraform generating configuration — https://developer.hashicorp.com/terraform/language/import/generating-configuration
- Terraform 1.11 write-only arguments — https://hashicorp.com/blog/terraform-1-11-ephemeral-values-managed-resources-write-only-arguments
- Terraform S3 backend — https://developer.hashicorp.com/terraform/language/backend/s3
- Terraform 1.17.0 — https://newreleases.io/project/github/hashicorp/terraform/release/v1.17.0 , https://endoflife.ai/terraform/1.17
- CDK for Terraform — https://github.com/hashicorp/terraform-cdk
- Terragrunt 1.0 — https://www.gruntwork.io/blog/terragrunt-1-0-released
- Terragrunt state backend — https://docs.terragrunt.com/features/state-backend/
- Terragrunt stacks — https://docs.terragrunt.com/features/stacks/
- Atmos — https://atmos.tools/ ; Atmos docs source (providers/backend/generate) — https://github.com/cloudposse/atmos/tree/main/website/docs/stacks
- Atmos Pro pricing — https://atmos-pro.com/pricing
- Alibaba Terraform provider releases — https://github.com/aliyun/terraform-provider-alicloud/releases

Multi-cloud spec projects
- Score — https://github.com/score-spec/spec , https://www.cncf.io/blog/2024/08/08/score-accepted-as-a-cncf-sandbox-project/
- Radius — https://docs.radapp.io/concepts/faq , https://docs.radapp.io/guides/recipes/terraform/overview/
- Crossplane v2 — https://docs.crossplane.io/v2.0/whats-new/ , https://docs.crossplane.io/v2.3/guides/upgrade-to-crossplane-v2/
- Crossplane official-provider package policies — https://docs.upbound.io/providers/policies/
- Alibaba upjet provider — https://github.com/crossplane-contrib/provider-upjet-alibabacloud

Discovery
- Cloud Control ListResources — https://docs.aws.amazon.com/cloudcontrolapi/latest/userguide/resource-operations-list.html
- Cloud Control resource types — https://docs.aws.amazon.com/cloudcontrolapi/latest/userguide/resource-types.html
- AWS Config advanced query — https://docs.aws.amazon.com/config/latest/developerguide/querying-AWS-resources.html
- Resource Explorer IncludedProperty — https://docs.aws.amazon.com/resource-explorer/latest/apireference/API_IncludedProperty.html
- Resource Explorer and Organizations — https://docs.aws.amazon.com/organizations/latest/userguide/services-that-can-integrate-resource-explorer.html
- GCP Migration Center AWS import — https://docs.cloud.google.com/migration-center/docs/aws-import-data
- CloudQuery login and pricing — https://www.cloudquery.io/blog/mandatory-login , https://www.cloudquery.io/pricing

Data migration
- Google DMS supported databases — https://docs.cloud.google.com/database-migration/docs/supported-databases
- Google DMS PostgreSQL known limitations — https://docs.cloud.google.com/database-migration/docs/postgres/known-limitations
- Google DMS migration fidelity (AlloyDB) — https://docs.cloud.google.com/database-migration/docs/postgresql-to-alloydb/migration-fidelity
- Storage Transfer: S3 source access — https://docs.cloud.google.com/storage-transfer/docs/source-amazon-s3
- Storage Transfer: event-driven from S3 — https://docs.cloud.google.com/storage-transfer/docs/event-driven-aws
- AWS DataSync cross-cloud — https://aws.amazon.com/about-aws/whats-new/2025/05/aws-datasync-accelerates-cross-cloud-data-transfers , https://docs.aws.amazon.com/datasync/latest/userguide/tutorial_transfer-google-cloud-storage.html
- Alibaba DTS, RDS PostgreSQL incremental — https://www.alibabacloud.com/help/en/dts/user-guide/migrate-incremental-data-from-an-amazon-rds-for-postgresql-instance-to-an-apsaradb-rds-for-postgresql-instance
- Alibaba DTS, RDS MySQL — https://www.alibabacloud.com/help/en/dts/user-guide/migrate-data-from-an-amazon-rds-for-mysql-instance-to-an-apsaradb-rds-for-mysql-instance
- Alibaba Data Online Migration, S3 → OSS — https://www.alibabacloud.com/help/en/data-online-migration/user-guide/migrate-data-from-amazon-s3-to-oss
- ossimport — https://help.aliyun.com/en/data-online-migration/user-guide/ossimport-overview

Messaging limits
- Pub/Sub dead-letter topics — https://docs.cloud.google.com/pubsub/docs/dead-letter-topics ; DeadLetterPolicy reference — https://googleapis.dev/ruby/google-cloud-pubsub-v1/latest/Google/Cloud/PubSub/V1/DeadLetterPolicy.html
- Alibaba MNS queue attributes — https://www.alibabacloud.com/help/en/mns/developer-reference/api-mns-open-2022-01-19-setqueueattributes , https://www.alibabacloud.com/help/en/ros/developer-reference/aliyun-mns-queue

Tooling and testing
- TFLint rulesets — https://github.com/terraform-linters
- Checkov checks — https://github.com/bridgecrewio/checkov/tree/main/checkov/terraform/checks/resource
- Trivy checks — https://github.com/aquasecurity/trivy-checks/tree/main/checks/cloud
- Trivy supply-chain incident — https://www.aquasec.com/blog/trivy-supply-chain-attack/ , https://snyk.io/articles/trivy-github-actions-supply-chain-compromise/
- Infracost supported resources — https://infracost.io/docs/supported_resources/overview
- moto coverage — https://github.com/getmoto/moto/blob/master/IMPLEMENTATION_COVERAGE.md
- LocalStack — https://github.com/localstack/localstack , https://blog.localstack.cloud/the-road-ahead-for-localstack/

Portability
- Dapr components-contrib — https://github.com/dapr/components-contrib
- External Secrets Operator status — https://infisical.com/blog/external-secrets-operator-paused , https://endoflife.ai/external-secrets
