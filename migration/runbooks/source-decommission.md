# Runbook: source decommission

| | |
|---|---|
| Workflow | `migration/workflows/decommission.yaml` |
| Status | Designed procedure. Not tested against a real cloud. **Never automatic.** Hermes never deletes source resources. |

## Purpose

Retire the AWS resources of a migrated scope safely, without losing data that must be retained
and without breaking dependencies the migration did not see.

## Preconditions

- [ ] The rollback window is closed with written sign-off from each service owner.
- [ ] Usage on every resource in the batch is near zero for the observation period: load balancer request counts, database connections, S3 requests, queue receives, Lambda invocations, KMS decrypt calls (CloudTrail).
- [ ] The dependency check is clean: a fresh `hermes inventory aws` shows no `relationships` from resources outside the batch into it, and the old inventory's `gaps` were triaged (undiscovered scope may still depend on these resources).
- [ ] Retention decisions are recorded per data set: backups, snapshots, CloudTrail, logs, Object Lock data, audit evidence.
- [ ] Final snapshots and exports exist and at least one restore was tested.
- [ ] Exit credits (if applicable) were requested and approved *before* the bulk data transfer. AWS's programme requires a Support request; check the current terms.

## Roles

| Role | Responsibility |
|---|---|
| Migration lead | Owns the batch and the sequence |
| Service owners | Confirm no use; sign off |
| Security officer | Credential and key revocation; retention of audit data |
| Finance / FinOps | Billing verification |
| Approver | Approves every destructive step |

## Steps

| # | Step | Owner | Gate | Verification |
|---|---|---|---|---|
| 1 | Confirm preconditions and freeze the batch list | Migration lead | **Approval** | Signed checklist |
| 2 | Final snapshots and exports; copy required archives to target cold storage | Service owners | **Approval** | Restore test passed |
| 3 | Remove migration artefacts: DMS/DTS jobs, replication slots, `pglogical`, CDC streams, transfer roles and **access keys created for Data Online Migration or Migration Center**, HMAC keys, temporary security group rules | Security officer | **Approval** | None remain (IAM credential report, `pg_replication_slots`) |
| 4 | Scream test: stop instances, scale services to 0 and disable triggers for an agreed period (for example 7 days) | Service owners | **Approval** | No incidents attributed |
| 5 | Remove deletion protection explicitly, per resource | Service owners | **Approval** | Change recorded per resource |
| 6 | Delete through the owning tool: CloudFormation stack deletion, the owning OpenTofu/Terraform `destroy` with approval, or manual deletion for unmanaged resources | Service owners | **Approval** | Re-run `hermes inventory aws`: only retained resources remain |
| 7 | Remove hybrid connectivity once nothing else uses it | Network engineer | **Approval** | Tunnel counters ~0 before removal |
| 8 | Disable KMS keys, watch for errors, then schedule deletion (7–30 day waiting period) **last**, only after all data encrypted with them is gone or not needed | Security officer | **Approval** | No Decrypt calls while disabled |
| 9 | Verify billing for at least one full cycle | FinOps | — | Only retained-resource charges |
| 10 | Close or minimize accounts | Migration lead | **Approval** | Account status recorded |

## Abort criteria

Any unexpected usage, alarm or incident during the scream test. A retention requirement
discovered late. A restore test failure.

## Rollback

Steps 1–5 and 8 (during the waiting period) are reversible: restart, restore desired counts,
cancel key deletion. Steps 6, 7 (new IPs and keys), 8 after the waiting period, and 10 after
the post-closure period are **not reversible**. Recovery from them means restoring from final
snapshots into new resources.

## Communication plan

| When | Audience | Message |
|---|---|---|
| T-14 days | Service owners, security, finance | Batch list, retention plan, scream-test dates |
| Scream test start and end | Change channel | Resources stopped / to be deleted |
| Deletion | Change channel | Deleted resources; where archives live |
| Key deletion scheduled | Security, service owners | Keys and dates; how to cancel during the window |
