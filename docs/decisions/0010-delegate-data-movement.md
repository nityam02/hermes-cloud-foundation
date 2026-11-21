# ADR 0010 — Delegate data movement to managed migration services

- Status: Accepted (2026-11-18)

## Context

Creating target infrastructure is not migrating. Data has to move with integrity, change data capture, lag
monitoring and validation, and that is a specialised, well-served problem:

- Google Database Migration Service (continuous PostgreSQL via pglogical, plus MySQL).
- Alibaba DTS (full plus incremental migration).
- AWS DMS.
- Google Storage Transfer Service (agentless, from S3, can use a federated role).
- Alibaba Data Online Migration / ossimport (needs AWS access keys).
- AWS DataSync.

See §6 for details.

## Decision

- Hermes **does not copy data**. Its migration workflows (`migration/workflows/*.yaml`) model each data movement
  as `automation: provider-tool` steps, with explicit gates, validation criteria, rollback notes and
  source-impact labels. Hermes generates the plan, checks the gates, validates outcomes, and records the audit
  trail.
- Steps that modify the source (for example enabling `rds.logical_replication`, which needs a reboot) are
  labelled `source_impact: modifies-source-config` and always need explicit approval.
- Known limits are stated in the workflows, for example: logical replication does not carry sequences or DDL,
  tables without primary keys lose UPDATE/DELETE events in DMS standard mode, multipart ETags are not MD5
  checksums, and credentials must be re-created at the target rather than copied.

## Consequences

- Each data path has a runbook (`migration/runbooks/`). Validation checks (`hermes migrate validate`) let teams
  encode row-count, checksum and smoke tests as commands.
- When a managed service needs long-lived source credentials (Alibaba Data Online Migration, Migration Center),
  the guides call out the risk and recommend short-lived, narrowly scoped, promptly revoked keys.
