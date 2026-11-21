# Architecture Decision Records

Each ADR records one significant decision: the context, the options considered, the decision, and its
consequences. ADRs are immutable once accepted; a change of direction is a new ADR that supersedes the old one.
Evidence references point to [`docs/research/open-source-landscape.md`](../research/open-source-landscape.md)
(section numbers in brackets) and to external sources.

| ADR | Title | Status |
|---|---|---|
| [0001](0001-thin-cli-over-the-engine.md) | A thin CLI over the IaC engine — no Atmos, Terragrunt or Crossplane dependency | Accepted |
| [0002](0002-opentofu-default-engine.md) | OpenTofu as the default engine; Terraform supported | Accepted |
| [0003](0003-capability-contracts-and-provider-compositions.md) | Capability contracts with per-provider compositions, not a universal cloud API | Accepted |
| [0004](0004-json-rendering-and-embedded-modules.md) | Render root modules as JSON; ship version-locked modules inside the CLI | Accepted |
| [0005](0005-serverless-containers-default-compute.md) | Serverless containers as the default compute; Kubernetes reserved | Accepted |
| [0006](0006-application-portability-via-standards.md) | Application portability through a binding contract and open standards | Accepted |
| [0007](0007-saved-plan-apply-and-approval-gates.md) | Saved-plan-only apply with destructive-change and protected-environment gates | Accepted |
| [0008](0008-state-isolation-and-backends.md) | One state per project, environment and provider | Accepted |
| [0009](0009-agentless-read-only-discovery.md) | Agentless, IaC-independent, read-only discovery through official APIs | Accepted |
| [0010](0010-delegate-data-movement.md) | Delegate data movement to managed migration services | Accepted |
| [0011](0011-policy-as-code-on-plan-json.md) | Policy as code with Conftest on plan JSON | Accepted |
| [0012](0012-secret-values-outside-iac.md) | Secret values never pass through IaC, state or discovery | Accepted |
| [0013](0013-testing-without-cloud-credentials.md) | Test without cloud credentials; real-cloud tests are opt-in | Accepted |
| [0014](0014-cutover-hooks-and-no-automatic-decommission.md) | Cutover runs only user-declared hooks; decommissioning is never automated | Accepted |
| [0015](0015-measurable-portability.md) | Portability is measured, not promised | Accepted |
