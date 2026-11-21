# Runbook: PostgreSQL cutover (Amazon RDS / Aurora → Cloud SQL or ApsaraDB RDS)

| | |
|---|---|
| Workflow | `migration/workflows/relational-database-postgresql.yaml` (steps `freeze-writes` … `keep-source-read-only`) |
| Status | Designed procedure. Not tested against a real cloud. |
| Typical duration | Write freeze of minutes to an hour, depending on lag and the number of sequences and services |

## Purpose

Move the primary role of one PostgreSQL database from AWS to the target. Replication is
already running and validated. The source must stay intact, so a rollback is possible for the
agreed window.

## Preconditions (all must be true; record evidence in the change record)

- [ ] A continuous migration job (Google DMS or Alibaba DTS) is in CDC and lag is stable. Alternatively, native logical replication is running.
- [ ] `hermes migrate validate` passes for row counts, range checksums, sequences, extensions and constraints, run within the last 24 h.
- [ ] A rehearsal in non-production measured the freeze duration and exercised the rollback.
- [ ] Every application that writes to the database is known: services, batch jobs, cron, ad-hoc tooling and other teams. Each has a stop procedure.
- [ ] The target is sized for production load. HA, backups and PITR are enabled immediately after promotion, because Google DMS suspends them during replication.
- [ ] A new Platform spec revision with the target `HERMES_DB_*` bindings is planned (`hermes plan`) and reviewed, but not applied.
- [ ] The reverse-replication decision is recorded: either it is in place, or the rollback window accepts losing post-cutover writes.
- [ ] The source has free storage headroom and no replication slot lag alarms.

## Roles

| Role | Responsibility |
|---|---|
| Migration lead | Runs the runbook and owns go/no-go at each gate |
| DBA | Freezes writes, checks replication and sequences, promotes |
| Service owners | Stop and start writers, run smoke tests |
| Approver (change authority) | Approves each gate; never the same person as the migration lead |
| On-call SRE | Watches dashboards and alerts on both clouds |
| Communications lead | Status page and stakeholder updates |

## Steps

| # | Step | Owner | Gate | Verification |
|---|---|---|---|---|
| 1 | Announce the start of the window; confirm on-call on both sides | Comms lead | — | Announcement sent |
| 2 | Go/no-go: preconditions checked, lag below threshold | Migration lead | **Approval** | Signed checklist |
| 3 | Stop writers: put services into maintenance mode, scale writers to 0, pause batch jobs | Service owners | **Approval** | No application sessions in `pg_stat_activity` except read-only ones |
| 4 | Revoke write privileges from application roles on the source | DBA | **Approval** | A test `INSERT` as an application role fails |
| 5 | Record the freeze point: `pg_current_wal_lsn()` and the timestamp | DBA | — | Values recorded in the change record |
| 6 | Wait for zero lag on the migration job | DBA | — | Tool shows lag 0; the target has seen the last write (probe table) |
| 7 | Sequences: with DTS or native replication, `setval()` every sequence to at least the source value plus a margin. Google DMS syncs them on promotion | DBA | **Approval** | For each sequence, target `last_value` ≥ source `last_value` |
| 8 | Promote the target (Google DMS *promote* / complete the DTS task) | DBA | **Approval** | Target accepts writes; job state is completed |
| 9 | Enable HA, automated backups and PITR; confirm deletion protection | DBA | — | Settings visible on the instance |
| 10 | Run `ANALYZE` on the target if it was not run after the load | DBA | — | `pg_stat_user_tables.last_analyze` is set |
| 11 | Apply the reviewed spec: `hermes apply --approve <plan-id-prefix>` (new `HERMES_DB_*`) | Migration lead | **Approval** | Services restart with the new bindings |
| 12 | Start writers on the target; run smoke tests (write and read paths) | Service owners | Validation | Smoke tests pass; error rate within SLO |
| 13 | Optional: start reverse replication target → source | DBA | **Approval** | Reverse lag near 0 |
| 14 | Keep the source read-only and intact; record the rollback baseline | DBA | **Approval** | No source writes since step 4 |
| 15 | Announce completion and the start of the rollback window | Comms lead | — | Announcement sent |

## Abort criteria (before step 8, abort is cheap)

- Lag does not reach zero within the agreed time.
- A writer that was not on the list appears in `pg_stat_activity` after step 4.
- Validation diffs appear for any table.
- Promotion fails or the target rejects writes.

To abort before promotion: re-grant write privileges on the source, restart writers on the
source and leave replication running. Nothing changed on the source except privileges.

## Rollback (after promotion)

Promotion is **not reversible** in the sense of resuming forward replication. See
[rollback.md](rollback.md). In short:

1. Stop writers on the target and revoke their write privileges there.
2. With reverse replication running: wait for zero reverse lag, then re-grant writes on the source and re-apply the previous spec revision.
3. Without reverse replication: every write made on the target after promotion is lost unless it is exported and replayed by hand. Decide explicitly whether that is acceptable.
4. Sequences on the source must be advanced past any values used on the target.

## Communication plan

| When | Audience | Message |
|---|---|---|
| T-5 days | Service owners and stakeholders | Window, expected read-only/maintenance duration, rollback window |
| T-1 day | Same | Go/no-go confirmation and on-call contacts |
| Step 1, then every 15 min | Change channel, status page | Progress and next step |
| Abort or rollback | All of the above | What happened, the current state and the next attempt |
| Step 15 | All | Completed; rollback window end date; whom to contact |
