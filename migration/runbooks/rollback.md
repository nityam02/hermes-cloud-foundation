# Runbook: rollback

| | |
|---|---|
| Applies to | Any cutover in `relational-database-*`, `object-storage`, `messaging`, `nosql-database`, `container-workload`, `traffic-cutover` |
| Status | Designed procedure. Not tested against a real cloud. |

## Purpose

Return service to the source environment after a cutover goes wrong. This runbook states
exactly what can be reversed and what cannot. Teams should decide *before* cutover how much
post-cutover data loss or manual reconciliation they accept.

## Reversible versus not

| Change | Reversible? | How | What is lost or must be reconciled |
|---|---|---|---|
| DNS weight shift | Yes | Set target weight to 0 | Nothing; in-flight sessions may break |
| Application bindings (`HERMES_*`) | Yes | Re-apply the previous spec revision (`hermes apply --approve`) | Nothing by itself |
| Database write freeze on the source | Yes | Re-grant write privileges | Nothing |
| Database promotion on the target | **No** (forward replication cannot resume) | Point apps back to the source | **Writes made on the target after promotion**, unless reverse replication ran or they are replayed by hand. Sequences on the source must be advanced past values used on the target |
| Object storage switch | Partly | Re-point apps to S3, remove the deny-write policy | Objects written or deleted on the target after cutover, unless copied back |
| Messaging producer switch | Yes | Point producers back | Messages published to the target and not yet consumed must be drained there, or replayed to the source |
| Secrets re-issued on the target | Yes | Source copies stay valid until decommission | Nothing, unless source credentials were already revoked |
| Source decommissioned (deleted) | **No** | Restore from final snapshots and exports | Everything after the snapshot; new endpoints |
| KMS key deletion (after its waiting period) | **No** | — | All data encrypted only with that key |

## Preconditions for a rollback

- The rollback window has not closed, and the source is intact and read-only.
- The person invoking rollback has approver authority, or is backed by an approver.
- The data-loss consequence from the table above is understood and accepted, and recorded in the change record.

## Roles

Migration lead (runs it), approver (authorizes), DBA, storage owner, service owners, on-call
SRE, communications lead.

## Steps

| # | Step | Owner | Gate | Verification |
|---|---|---|---|---|
| 1 | Declare rollback and state which trigger fired | Migration lead | **Approval** | Recorded |
| 2 | Shift traffic back (DNS weights) if traffic was moved | DNS operator | **Approval** | Authoritative answers updated |
| 3 | Stop writers on the target | Service owners | **Approval** | No writes on target databases, buckets or topics |
| 4 | Reconcile data: wait for reverse replication to reach zero lag, or export target-only writes and objects | DBA / storage owner | **Approval** | Reconciliation report |
| 5 | Database: advance source sequences beyond target values, then re-grant writes on the source | DBA | **Approval** | A test insert succeeds without key conflicts |
| 6 | Objects: copy target-only objects back to S3, then remove the deny-write policy | Storage owner | **Approval** | Counts reconciled |
| 7 | Re-apply the previous spec revision (bindings back to the source) | Migration lead | **Approval** | Services healthy on the source |
| 8 | Drain unconsumed target messages, or replay them to the source | Service owners | — | Target backlog 0 |
| 9 | Keep the target intact for analysis; do not destroy it during the incident | Migration lead | — | — |
| 10 | Post-incident review; update the workflow and runbooks | Migration lead | — | Review document |

## Abort criteria for the rollback itself

If the source cannot accept writes (for example storage is full or replication slots are
blocking), stop. Fix forward on the target instead, and escalate.

## Communication plan

Announce the rollback immediately in the change channel and on the status page. Give
stakeholders an ETA, and say plainly whether any data written during the cutover period needs
reconciliation and who will contact affected users.
