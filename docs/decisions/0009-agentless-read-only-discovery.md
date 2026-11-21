# ADR 0009 — Agentless, IaC-independent, read-only discovery through official APIs

- Status: Accepted (2026-11-18)

## Context

Most real estates that need migrating were not built with Terraform. They were built in the console, with
scripts, SDKs, configuration management, or a mixture. Discovery must work with **no IaC state at all**, must
not install agents, and must never change the source.

Evidence [§5]:

- **Terraformer** is archived.
- **Google Cloud Migration Center**'s AWS discovery is in Preview (only EC2 and RDS are GA) and needs long-lived
  AWS access keys.
- **AWS Application Discovery Service** targets servers and VMs.
- **Steampipe**'s CLI is AGPL-3.0.
- **CloudQuery** now needs a vendor login and charges for AWS tables.
- Among the AWS-native read paths: Cloud Control `GetResource` returns full models; Resource Explorer returns
  only ARNs, types and tags; AWS Config advanced query covers only recorded types. moto, the local emulator,
  implements the per-service APIs but not Cloud Control.

## Decision

- Hermes discovers through the **per-service Describe/List/Get APIs** of the official AWS SDK for Go v2. It uses
  Cloud Control as an optional fallback for additional types, and AWS Config exports as an optional import path
  with lower completeness.
- Access is a **cross-account role with an ExternalId** condition and a one-hour session. The role is created by
  the source owner from a CloudFormation template or a plain HCL module (`migration/discovery/`). This avoids
  assuming Terraform in the source organisation.
- A **read-only guard middleware** on every SDK client rejects, before anything is sent, any operation that is
  not on an explicit allowlist. Secret values and object data are explicitly denied: `GetSecretValue`, SSM
  `GetParameter*`, `GetObject`, `GetFunction`, `ReceiveMessage`. The published IAM policy is generated from the
  same operation registry, and a test keeps the two identical.
- Access denials, opt-in regions and throttling are recorded as **gaps** with their impact. Gaps are carried
  into the assessment as risks: undiscovered scope is never reported as "nothing there".
- Terraform state, CloudFormation stacks and tags are used **only to annotate** `managed_by`.
- Google Migration Center and AWS ADS are documented as **complementary** sources of utilisation data for
  sizing. They are not dependencies.

## Consequences

- Hermes maintains collectors for the most important AWS services. Coverage is explicit in the inventory (types
  collected, gaps recorded), and uncollected types are listed in `docs/limitations.md`.
- The discovery demo runs against moto locally. Real-account integration tests are opt-in (ADR 0013).
