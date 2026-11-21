# Disaster recovery strategy

Recovery objectives, what each provider composition provides, and what it honestly costs. Procedures
are in [backup-and-restore.md](backup-and-restore.md). Status: backup settings are implemented in
the compositions and mock-tested; **no restore has been rehearsed against a real cloud**.

## 1. Safety principle

**Drone, vehicle and device safety never depends on cloud availability.** Edge devices must fail
safe on their own (hold, land, stop, return) when the cloud, the network or a Hermes deployment is
unavailable. The cloud side provides fleet coordination, telemetry, history and planning; none of
it may be in a safety-critical control loop. DR decisions below are about business continuity and
data, never about keeping a device safe.

## 2. Tiers

| Tier | Typical use | RPO | RTO | What it takes |
|---|---|---|---|---|
| Small | dev, internal tools | 24 h | 1–2 days | automated DB backups (7 days), bucket versioning, state versioning; restore in place |
| Medium | production, single region | ≤ 5 min (PITR) | 4 h | `high_availability: true` (multi-AZ / regional DB), PITR, 14–35 day retention, deletion protection, final snapshots, rehearsed restores |
| Enterprise | regulated or revenue-critical | ≤ 1 min (cross-region replicas) | 1 h | medium + cross-region DB replica and bucket replication, IaC ready to deploy in a second region, regular failover tests |

Protected environments in Hermes default to deletion protection, final snapshots and
customer-managed keys; HCF-POL-006/011 refuse plans that disable deletion protection, backups,
PITR or final snapshots there. The **enterprise tier is designed, not implemented**: the
compositions have no cross-region replica or bucket replication options.

## 3. What each composition provides

| Capability | AWS | GCP | Alibaba Cloud |
|---|---|---|---|
| DB high availability | RDS Multi-AZ | Cloud SQL regional (HA) | ApsaraDB RDS high-availability edition |
| DB backups | automated backups, `backup_retention_days` | automated backups + transaction logs | `alicloud_db_backup_policy` |
| PITR | yes (within retention) | yes (`point_in_time_recovery_enabled`) | yes (log backups), per edition |
| Final snapshot on delete | yes in protected environments | deletion protection; final backup per settings | release protection; backup retention after release per policy |
| Bucket versioning | per bucket (`versioning`) | per bucket | per bucket |
| State backend recovery | S3 versioning | GCS versioning | OSS versioning |
| Secrets | containers in IaC; values must be re-set out-of-band after re-creation | same | same |

## 4. Cross-region options (manual today)

- AWS: RDS cross-region automated backup replication or a read replica; S3 Cross-Region
  Replication; a second state key per region.
- GCP: Cloud SQL cross-region read replica; dual-region or multi-region buckets; Cloud Run is
  regional, so deploy the same environment to a second region.
- Alibaba: ApsaraDB cross-region backups or DTS to a second instance; OSS cross-region
  replication.

## 5. Multi-cloud DR: the honest cost

Hermes makes it possible to deploy the same spec to a second provider, which is valuable for
**exit and portability**. As a DR mechanism it is expensive: continuous data replication between
clouds (DMS/DTS/logical replication), egress charges on every byte, two operational skill sets,
two IAM and network models, and failover testing across providers. Most teams meet their
objectives more cheaply with multi-region on one provider plus tested backups. Use multi-cloud DR
only when the risk is the provider itself (account closure, sanctions, provider-wide outage) and
budget for it explicitly.

## 6. State backend recovery

The state backend is a dependency of every change, not of running workloads. Losing it stops
deployments, not services. Recovery: restore the previous object version; if the bucket is lost,
re-create it with the bootstrap module and re-import resources (`import` blocks generated from
the resources, or a restored copy of the state). Keep an offline, encrypted copy of production
state after major changes.

## 7. Testing

Rehearse at least: a DB PITR into a new instance (quarterly for medium/enterprise), restoring a
deleted object version, a state rollback in a non-production environment, and re-creating secret
values after a secret container is replaced. Record RPO/RTO achieved, not just success.
