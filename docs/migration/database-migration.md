# Database migration (PostgreSQL and MySQL)

> Status: designed guidance backed by the `relational-database-postgresql` and
> `relational-database-mysql` workflows and the
> [PostgreSQL cutover runbook](../../migration/runbooks/postgresql-cutover.md). Hermes provisions
> PostgreSQL targets (Cloud SQL / ApsaraDB RDS for PostgreSQL). MySQL targets are reserved in
> v1alpha1. Nothing has been tested against a real cloud.

## Phases

1. **Snapshot.** A consistent initial copy (dump or tool-managed snapshot) taken at a known position (LSN or binlog/GTID).
2. **CDC.** Continuous change data capture from that position (PostgreSQL logical decoding, MySQL binary log).
3. **Lag management.** Lag is measured continuously. A long-running CDC retains WAL or binlogs on the source, so source storage is part of the risk.
4. **Read-only window.** Writes stop on the source, lag drains to zero, and sequences are fixed.
5. **Primary switch.** The target is promoted and applications are re-pointed through `HERMES_DB_*`.
6. **Rollback window.** The source is kept read-only and intact. Reverse replication is optional.

## Tool comparison

| | Google DMS | Alibaba DTS | AWS DMS | Native logical replication (PostgreSQL) | pglogical (self-run) |
|---|---|---|---|---|---|
| Direction | Into Cloud SQL / AlloyDB | Into ApsaraDB / PolarDB | From AWS to any reachable PostgreSQL/MySQL endpoint | Any PostgreSQL ≥ 10 pair | Any pair where the extension is available |
| PostgreSQL CDC mechanism | pglogical | Logical decoding (`dts_sync_*` slot) | Logical decoding (test_decoding/pglogical) | Publication/subscription | Replication sets |
| DDL | Not replicated (use `pglogical.replicate_ddl_command`) | Not replicated; no DDL during migration | Limited | Not replicated | Via `replicate_ddl_command` |
| Sequences | Synced on promotion | Not migrated; set manually | Not migrated during ongoing replication | Not replicated before PostgreSQL 19 | Manual sync functions |
| Tables without PK | Standard mode: snapshot + INSERT only | Check the DTS precheck [U] | Needs PK/replica identity for UPDATE/DELETE | Needs replica identity for UPDATE/DELETE | Needs PK |
| Large objects | Not supported | Not supported | Limited | Not supported | Not supported |
| Price | No additional charge (homogeneous) | Schema/full free; incremental billed hourly | Replication instance hours | Free | Free |
| Fit | Default for AWS → GCP | Default for AWS → Alibaba | Useful when running from the AWS side or for heterogeneous moves | Simple databases, reverse replication | When managed tools are not an option |

PostgreSQL 19 adds sequence synchronization to native logical replication
(`ALTER SUBSCRIPTION ... REFRESH SEQUENCES`). It requires PostgreSQL 19 on the publisher too.
General availability was planned for 2026-11-19 and managed services will lag behind, so do not
plan around it yet.

## PostgreSQL specifics

- **Versions:** target major ≥ source major. Major-version upgrades during the migration go through `pg_restore`, which installs the target's default extension versions; upgrade source extensions first.
- **Extensions:** compare every installed extension with the target's supported list. PostGIS, pgvector and pg_cron are commonly available, but versions differ. `pg_cron` jobs are not migrated by Google DMS. AWS-only extensions (`aws_s3`, `aws_lambda`, `aws_commons`) and C-language functions have no target equivalent.
- **Source preparation on RDS/Aurora:** `rds.logical_replication = 1`. For Google DMS also add `pglogical` to `shared_preload_libraries`, set `wal_sender_timeout = 0`, and size `max_replication_slots`, `max_wal_senders` and `max_worker_processes`. Reboot (or restart the cluster for Aurora). Grant `rds_replication` to the migration user.
- **Not migrated:** users and roles, materialized view data (refresh after load), `UNLOGGED`/`TEMPORARY` table data, custom tablespaces (objects move to `pg_default`).
- **Encryption:** encrypted RDS/Aurora sources are fine, because AWS decrypts on read. The target is encrypted with target keys (CMEK in protected environments).
- **Unsupported source shapes:** read replicas in recovery mode (Google DMS); RDS with the AWS SCT extension pack (Google DMS).

## MySQL specifics

- Binary logging requires automated backups on RDS. Use `binlog_format = ROW` and set retention long enough for the full load (`mysql.rds_set_configuration('binlog retention hours', N)`).
- `GTID_MODE` must be `ON` or `OFF` for Google DMS.
- Only InnoDB. Plan `DEFINER` accounts. The migration user password is limited to 32 characters (Google DMS).
- MariaDB is not MySQL. GCP has no managed MariaDB, and its GTIDs and several features are incompatible.

## Consistency and validation

| Check | How |
|---|---|
| Row counts | Exact `count(*)` per table after the freeze (estimates are not enough) |
| Content | Checksums over primary-key ranges (for example the hash of an ordered aggregate per range) on both sides |
| Sequences / AUTO_INCREMENT | Target value ≥ source value for every sequence |
| Schema | Constraints, indexes, triggers, routines and extension versions present |
| Behaviour | Read-only application smoke tests against the target before cutover; write smoke tests after promotion |
| Performance | Run `ANALYZE` after the load; check top queries |

Encode the checks in a `ValidationChecks` file and run `hermes migrate validate`.

## High availability differences

Multi-AZ RDS fails over by repointing a DNS name. Cloud SQL regional HA keeps the same IP.
ApsaraDB HA editions use their own switchover behaviour. All of them drop open connections.
Applications need connection retry with backoff. Test a failover on the target before
production cutover. Reports of failovers far longer than documented exist, so measure your own.

## Rollback limits

Before promotion, rollback is cheap: re-grant writes on the source. After promotion, forward
replication cannot resume. Rolling back then means either draining reverse replication (DML only:
no DDL, sequences or large objects; every table needs replica identity) or accepting the loss of
target-only writes. Decide which before cutover ([rollback runbook](../../migration/runbooks/rollback.md)).
