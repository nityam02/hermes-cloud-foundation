# Executive overview

**Hermes Cloud Foundation v0.1.** An open-source platform layer that lets a team describe its infrastructure once
and deploy it to AWS, Google Cloud or Alibaba Cloud, with a separate toolkit for planning a migration off an
existing AWS estate. **Status: mock-tested only. Nothing has been deployed to a real cloud.**

## The problem

Teams that must run on more than one cloud, or that expect to move between clouds, face three problems:

1. **Duplicate infrastructure code.** Each cloud gets its own Terraform, its own conventions and its own review
   habits. Portability claims are rarely measured.
2. **Unsafe change control.** Plain `terraform apply` can apply something other than what was reviewed. Deletes
   and production changes depend on discipline rather than gates.
3. **Migrations start blind.** Estates built by hand, or with several tools, have no reliable inventory.
   Vendor discovery tools often need long-lived keys, and the migration plan lives in spreadsheets.

## The approach

- **One spec, per-cloud implementations.** A small, cloud-independent `hermes.yaml` describes services,
  database, buckets, messaging, secrets and telemetry. Each cloud has a composition of native managed services
  (serverless containers, managed PostgreSQL, object storage, pub/sub). There is no lowest-common-denominator
  abstraction layer ([ADR 0003](../decisions/0003-capability-contracts-and-provider-compositions.md)).
- **Do not rebuild the engine.** OpenTofu (default) or Terraform does all planning, applying and state handling.
  Hermes adds what the engine does not: validation, provider selection, portability measurement, safety gates and
  the migration workflow ([ADR 0001](../decisions/0001-thin-cli-over-the-engine.md)).
- **Measure portability.** For a given spec, `hermes portability` scores each cloud, feature by feature, and
  lists the differences ([ADR 0015](../decisions/0015-measurable-portability.md)).
- **Gate every change.** Apply only a saved, reviewed plan. Destructive changes need an explicit flag.
  Production needs an approval tied to the exact plan id, plus a policy check
  ([ADR 0007](../decisions/0007-saved-plan-apply-and-approval-gates.md)).
- **Discover read-only, and never move data ourselves.** Discovery uses an assumed role with an ExternalId and
  an enforced read-only allowlist. Data moves through the clouds' own migration services. Cutover runs only
  commands the operator declares, behind approvals. Decommissioning is never automated
  ([ADR 0009](../decisions/0009-agentless-read-only-discovery.md), [0010](../decisions/0010-delegate-data-movement.md),
  [0014](../decisions/0014-cutover-hooks-and-no-automatic-decommission.md)).

## What is delivered (v0.1)

| Area | Delivered | Status |
|---|---|---|
| CLI | 17 commands: validate, portability, render, plan, apply, destroy, status/drift, bindings, state, doctor, init, providers, cost, version, inventory, migrate, adopt | implemented; verified offline with a fake engine |
| Provider compositions | AWS, GCP and Alibaba: 9 modules each, plus state-backend and CI-identity (OIDC) roots | implemented; **mock-tested only** |
| Coverage of 21 portable features | AWS 19 implemented / 2 partial; GCP 20 / 1; Alibaba 11 / 5 partial / 5 not implemented | from the Go capability matrices ([comparison](../providers/comparison-matrix.md)) |
| Safety | saved-plan apply, staleness, expiry and tamper checks, destructive and approval gates, policy-as-code (Conftest) for all three clouds, history | implemented; verified offline |
| Migration toolkit | read-only AWS discovery (27 service groups), dependency graph, a service catalog of 84 entries (to GCP and Alibaba), 13 workflow templates, assessment, draft target spec, gated plan and cutover, import-only adoption | implemented; verified offline and against a local AWS emulator (moto). Never run against real AWS |
| Documentation | architecture, 15 ADRs, provider guides, tutorials, operations, security, DR, migration guides, a [limitations register](../limitations.md) | — |

Evidence of quality: 125 Go tests and 132 module test runs (all against mock providers), 34 policy tests,
contract tests that keep the three clouds' interfaces byte-identical, and clean secret and IaC scans. checkov has
not been run. See the [testing guide](../testing.md).

## Risks

| Risk | Impact | Mitigation |
|---|---|---|
| **No real-cloud validation yet.** Mocks cannot prove that cloud APIs accept the configuration | First real applies may fail or need module fixes | First deployments in disposable test accounts; record the runs; add opt-in real-cloud tests (one read-only discovery test exists and has not been run) |
| **Alibaba is the least mature target** (few scanner rules, no Infracost, several manual steps) | More operator work and weaker guardrails | Conftest policies cover it; the gaps are documented per feature |
| Portability is partial by nature (IAM vs password DB auth, different messaging semantics) | Application changes in some areas | The gaps are measured and reported before deployment, never hidden |
| Broad deploy permissions on each cloud | Blast radius of the apply identity | Separate plan and apply identities, OIDC-only CI, environment approvals |
| Supply chain (providers, modules, actions, scanners) | Compromised tooling | Exact provider pins, embedded digest-locked modules, SHA-pinned actions, checksum-verified tools |
| Migration cutover errors | Downtime or data issues | Validation report gate, approvals, declared rollback hooks, the source kept intact; no automatic decommission |

## Costs

Hermes itself is free (Apache 2.0) and runs locally or in CI. Cloud costs are driven by NAT gateways, database
size and HA, always-on compute, load balancers, key management and log volume. An indicative AWS development
environment is roughly **$100–150/month** before data and logs. A protected production environment typically
costs several hundred dollars a month, dominated by the HA database and NAT. Running the same spec on two clouds
roughly doubles the fixed costs. Details, footprints and the expensive anti-patterns are in
[cost and sizing](../operations/cost-and-sizing.md). All figures are indicative list prices, not quotes.

## Roadmap (proposed, not committed)

1. **Prove it on real clouds.** Run plan, apply and destroy in disposable accounts on all three providers. Record
   the results, fix what breaks, and turn the steps into opt-in real-cloud tests. Run checkov and the Terraform
   integration pass.
2. **Close the documented gaps.** Service-to-service discovery and private URLs, custom domains and DNS, VPC
   interface endpoints on AWS, Alibaba automation gaps (SAE role binding, alarms), and cross-region DR options.
3. **Widen the scope.** Kubernetes and MySQL (already reserved in the spec), GCP or Alibaba as a migration
   **source**, and more catalog types.
4. **Operational polish.** Infracost cross-checks in CI, drift checks on a schedule, and published release
   binaries with checksums and signatures.

The authoritative list of what is not done is the [limitations register](../limitations.md).
