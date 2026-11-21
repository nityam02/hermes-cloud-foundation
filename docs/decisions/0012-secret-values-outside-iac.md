# ADR 0012 — Secret values never pass through IaC, state or discovery

- Status: Accepted (2026-11-18)

## Context

Terraform state stores every attribute in plaintext, including generated passwords and secret versions.
Encryption at rest protects the stored object, but not the people and pipelines that can read it.

## Decision

- The spec contains only secret **names**. Provider modules create secret **containers** with no versions:
  - AWS Secrets Manager and GCP Secret Manager containers are created empty.
  - Alibaba KMS Secrets Manager requires an initial value at creation, so the module sets a fixed non-secret
    placeholder and ignores later changes. The real value is always set out-of-band.
- Database credentials avoid state entirely:
  - AWS: RDS-managed master secret (`manage_master_user_password`), and IAM authentication for applications.
  - GCP: Cloud SQL IAM database users per service.
  - Alibaba: accounts are created after provisioning and stored in the secret manager. Alibaba RDS has no IAM
    database authentication.
- Services receive secrets through the native injection mechanism: ECS `secrets`, Cloud Run secret environment
  variables, or fetched with the service's own identity on SAE. Secrets never appear as plaintext in task or
  service definitions.
- Discovery reads metadata only. `GetSecretValue` and the SSM `GetParameter*` operations are blocked by the
  read-only guard (ADR 0009).
- Migration never copies secrets through Hermes. Secrets are re-created at the target, and rotated where possible.

## Consequences

- There is a post-provisioning step to set secret values. It is documented per provider, and in CI it is
  scripted with the provider CLI under the deploy identity.
- Write-only arguments (OpenTofu/Terraform ≥ 1.11) are not needed for the current design. They may be adopted
  later for resources that cannot avoid credentials.
