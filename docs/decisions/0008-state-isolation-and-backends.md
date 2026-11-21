# ADR 0008 — One state per project, environment and provider

- Status: Accepted (2026-11-18)

## Context

Shared state couples blast radius. During a migration, the source estate and the target estate are different
resources in different clouds. Terraform state and cloud resource identity are not interchangeable, so a single
state spanning both would be both wrong and dangerous.

## Decision

- The state location is `<prefix>/<project>/<environment>/<provider>/`: the object is `terraform.tfstate` for the
  S3 and OSS backends, and `default.tfstate` for GCS (the gcs backend names the object after the workspace and has
  no key setting). Each environment and provider
  has its own state, plan and history. The same spec deployed to two clouds therefore yields two independent
  states.
- Each provider uses its native backend with locking:
  - **S3**: `use_lockfile`; DynamoDB locking is deprecated and remains only as a legacy option.
  - **GCS**: built-in locking.
  - **OSS**: Tablestore locking.

  Buckets are versioned, encrypted, private and deletion-protected. They are created by the
  `infra/bootstrap/<provider>/state-backend` modules (`hermes state bootstrap`).
- `local` state is rejected for protected environments.
- OpenTofu client-side state encryption is available for AWS (KMS) and GCP (Cloud KMS) when the engine is
  OpenTofu. It is enforced for protected environments when enabled. It is not available on Alibaba (no key
  provider) or with Terraform.
- IaC adoption of source resources (ADR 0009) uses a **separate "source adoption" state**. It is never mixed
  with the target platform state.

## Consequences

- Cross-environment references go through explicit outputs and bindings, never `terraform_remote_state` between
  platform states.
- Bootstrapping the backend is a separate, small root module with local state. Its state is uploaded or
  documented afterwards; `docs/operations/state-management.md` describes the procedure.
