# Documentation index

Hermes v0.1 (`hermes/v1alpha1`) is **mock-tested only**. No document here describes a real-cloud deployment,
because none has been performed. Status claims are collected in the [limitations register](limitations.md).

Start with the [project README](../README.md) or the [executive overview](overview/executive-overview.md).

## By audience

### Application and platform developers (using Hermes)

| Document | What it covers |
|---|---|
| [Installation](tutorials/installation.md) | Tools, versions, checksum-verified installs |
| [Quickstart](tutorials/quickstart.md) | `init` → `validate` → `portability` → `render`, and what `plan` and `apply` will do |
| [Multi-cloud tutorial](tutorials/multi-cloud.md) | One spec, three clouds, isolated state |
| [Configuration reference](reference/configuration.md) | Every `hermes.yaml` field and default |
| [CLI reference](reference/cli.md) | Commands, flags, exit codes, the `.hermes/` directory |
| [Diagnostic codes](reference/diagnostics.md) | `HCF1xx`–`HCF5xx` |
| [Application portability](architecture/application-portability.md) | The `HERMES_*` binding contract, OTLP, CloudEvents, PostgreSQL |
| [Examples](../examples/README.md) | What each example demonstrates and how to run it |

### Contributors (changing Hermes)

| Document | What it covers |
|---|---|
| [Developer onboarding](tutorials/onboarding.md) | Repository tour, make targets, your first change |
| [Architecture overview](architecture/overview.md) | Layers A–E and migration, key flows, decisions |
| [Detailed design](architecture/detailed-design.md) | Package-by-package design and sequence diagrams |
| [Data contracts](architecture/data-contracts.md) | `Platform`, module contract, `Inventory`, catalog, workflows, pricing, plans |
| [Module contract outputs](../infra/contract/outputs.md) | `bindings`, `service_environment`, `secret_environment` |
| [Testing guide](testing.md) | Every test layer: how to run it, and what it proves and does not prove |
| [Implementing a provider](providers/implementing-a-provider.md) | Step-by-step guide to adding a cloud |
| [Architecture decisions (ADRs)](decisions/README.md) | ADRs 0001–0015 |
| [Contributing](../CONTRIBUTING.md), [Changelog](../CHANGELOG.md), [Code of conduct](../CODE_OF_CONDUCT.md) | Process |

### Platform engineers and operators (running Hermes)

| Document | What it covers |
|---|---|
| [First production environment](tutorials/first-production-environment.md) | State bootstrap, protected environments, approvals, CI with OIDC |
| [Provider comparison matrix](providers/comparison-matrix.md) | Feature × provider, with status |
| [AWS](providers/aws.md), [GCP](providers/gcp.md), [Alibaba Cloud](providers/alibaba.md) | Provider guides: architecture, sizes, extensions, post-provision steps |
| [State management](operations/state-management.md) | Backends, locking, encryption, bootstrap, recovery |
| [Networking](operations/networking.md) | Address plans, NAT, egress, private DB access, hybrid during migration |
| [Cost and sizing](operations/cost-and-sizing.md) | Footprints from a small startup to enterprise, cost drivers, estimation |
| [Drift and day-2 operations](operations/drift-and-day2.md) | `status --drift`, upgrades, pin bumps |
| [Troubleshooting](operations/troubleshooting.md) | Diagnostic codes, engine errors, gate refusals, disk and plugin issues |
| [Monitoring and alerting](operations/monitoring-and-alerting.md) | What each composition alerts on, the OTel pipeline |
| [DR strategy](disaster-recovery/strategy.md), [backup and restore](disaster-recovery/backup-and-restore.md) | RPO/RTO tiers, restore procedures |
| [Limitations register](limitations.md) | What is not done or not proven |

### Security reviewers

| Document | What it covers |
|---|---|
| [Security architecture](security/security-architecture.md) | Controls by layer |
| [Threat model](security/threat-model.md) | STRIDE per component, abuse cases, residual risks |
| [IAM strategy](security/iam-strategy.md) | Humans, CI, workloads, discovery, break-glass |
| [CI/CD authentication](security/ci-cd-authentication.md) | OIDC per provider |
| [State security](security/state-security.md) | What state contains, encryption, access |
| ADRs [0007](decisions/0007-saved-plan-apply-and-approval-gates.md), [0011](decisions/0011-policy-as-code-on-plan-json.md), [0012](decisions/0012-secret-values-outside-iac.md), [0013](decisions/0013-testing-without-cloud-credentials.md) | Gates, policy, secrets, testing |
| [Safety-gate state machine](architecture/detailed-design.md#3-safety-gate-state-machine) | Every refusal path of `apply` |

### Migration engineers

| Document | What it covers |
|---|---|
| [Migration lifecycle](migration/migration-lifecycle.md) | Stages, what Hermes automates, what stays manual |
| [Discovery engine](migration/discovery-engine.md) | Read-only guard, permissions, gaps, graph |
| [IaC adoption](migration/iac-adoption.md) | Import-only adoption and its gate |
| [Migration commands](migration/migration-commands.md) | Every `inventory`, `migrate` and `adopt` command |
| [Service mapping](migration/service-mapping.md) | The catalog: AWS → GCP and Alibaba |
| [AWS to GCP](migration/aws-to-gcp.md), [AWS to Alibaba](migration/aws-to-alibaba.md) | Target-specific guides |
| [Database](migration/database-migration.md), [object storage](migration/object-storage-migration.md), [network](migration/network-migration.md) | Per-domain guides |
| [Tooling integration](migration/tooling-integration.md) | Which managed tools are used and why |
| Runbooks: [PostgreSQL cutover](../migration/runbooks/postgresql-cutover.md), [object storage](../migration/runbooks/object-storage-cutover.md), [DNS/traffic](../migration/runbooks/dns-traffic-cutover.md), [hybrid network](../migration/runbooks/hybrid-network.md), [rollback](../migration/runbooks/rollback.md), [decommission](../migration/runbooks/source-decommission.md) | Step-by-step procedures |

### Decision makers

| Document | What it covers |
|---|---|
| [Executive overview](overview/executive-overview.md) | Problem, approach, what is delivered, risks, costs, roadmap |
| [Open-source landscape](research/open-source-landscape.md) | Evidence behind the build-versus-reuse choices |
| [Reference architecture: autonomous logistics](architecture/autonomous-logistics-reference-architecture.md), [workload analysis](research/autonomous-logistics-workload-analysis.md) | A worked, generic example domain |
