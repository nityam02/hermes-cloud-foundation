# State security

OpenTofu/Terraform state is the engine's record of every managed resource. Hermes keeps secret
values out of it by design, but state is **sensitive even without secrets**.

## 1. What state contains

- Every attribute of every managed resource: network layout, endpoints, IAM roles and their
  policies, bucket and queue names, KMS key ids, database instance settings.
- Values some providers generate (for example connection strings), and, if a module ever breaks
  the rules, generated passwords or secret values (HCF-POL-009 denies the common cases).
- Outputs and the dependency graph: a complete map for an attacker planning lateral movement.

Saved plans (`.hermes/plans/<env>/plan.tfplan` and `plan.json`) contain the same information
plus planned values, and `show -json` prints sensitive values in clear text. Treat plans exactly
like state: they stay in `.hermes/` (mode 0700/0600, gitignored). The one exception is the
`integration.yml` apply run, which uploads the reviewed saved plan as a 1-day artifact for the
approved apply job (private repositories only; see
[CI/CD authentication](ci-cd-authentication.md)).

## 2. Layout and isolation

Key: `<prefix>/<project>/<environment>/<provider>/terraform.tfstate` (data-contracts §1). One state
per project, environment and provider: a mistake in one environment cannot touch another's state,
and the CI apply identities can write only their own environment's prefix
([iam-strategy.md](iam-strategy.md)). `state.backend: local` is rejected for protected
environments.

## 3. Controls per backend

| Control | AWS S3 | GCP GCS | Alibaba OSS |
|---|---|---|---|
| Bootstrap module | `infra/bootstrap/aws/state-backend` | `infra/bootstrap/gcp/state-backend` | `infra/bootstrap/alibaba/state-backend` |
| Encryption at rest | SSE-KMS (CMK optional); `encrypt = true` in the backend | Google-managed or CMEK | SSE AES256 or KMS; `encrypt = true` |
| Encryption in transit | TLS-only bucket policy | TLS (Google front ends) | TLS-only bucket policy |
| Public access | public access block | uniform bucket-level access, public access prevention enforced | private ACL + public access block |
| Versioning / recovery | versioning + noncurrent-version retention | object versioning + noncurrent cleanup | versioning |
| Deletion protection | `prevent_destroy` | `prevent_destroy` | `prevent_destroy` |
| Locking | S3 native lock file (`use_lockfile`, OpenTofu ≥ 1.10 / Terraform ≥ 1.11); DynamoDB only if `state.lock_table` | built into the GCS backend | Tablestore table (required in protected environments) |
| Client-side encryption (OpenTofu only) | `aws_kms` key provider | `gcp_kms` key provider | **not supported** (no Alibaba KMS key provider; validation error) |

Status: bootstrap modules mock-tested by the provider workstreams; backends not tested against
real clouds.

## 4. Client-side encryption (OpenTofu)

With `state.encryption.client_side: true` (engine `tofu`), the renderer emits a
`terraform.encryption` block with an `aws_kms` or `gcp_kms` key provider and `aes_gcm`, applied to
both state and plans, `enforced = true` in protected environments so unencrypted state is never
written. This protects state from anyone who can read the bucket but not use the key; it does not
protect against an identity that can do both. Terraform has no equivalent; on Terraform rely on
server-side encryption and access control. Losing the key makes state unreadable: protect the key
with deletion protection and restrict `ScheduleKeyDeletion`.

## 5. Access control

- Humans: read access for operators who need `hermes status`; write access only through CI or a
  time-boxed elevated role. Nobody edits state by hand without a backup.
- CI plan identity: read state, create/delete lock files only.
- CI apply identity: read/write its own environment only.
- Audit: enable data-access logging on the state bucket (CloudTrail S3 data events, GCS Data
  Access audit logs, OSS access logging) and alert on access by unexpected principals.

## 6. Operations

- Locks: a crashed run can leave a lock. Confirm no run is active, then `tofu force-unlock <id>`.
- Rollback: restore a previous object version (see
  [backup-and-restore.md](../disaster-recovery/backup-and-restore.md#3-state-file-rollback)), then
  re-plan; never apply a plan made against the newer state.
- Migration between backends: `tofu init -migrate-state` with both backends locked and versioned.
- Bootstrap state (the state of the state bucket itself) is local: keep it encrypted and
  private, or import the bucket into a second, small remote state.
