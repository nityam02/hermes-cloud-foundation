# Runbook: object storage cutover (Amazon S3 → Cloud Storage or OSS)

| | |
|---|---|
| Workflow | `migration/workflows/object-storage.yaml` (steps `incremental-sync` … `keep-source-read-only`) |
| Status | Designed procedure. Not tested against a real cloud. |

## Purpose

Switch readers and writers of one or more buckets from S3 to the target bucket without losing
objects written during the switch. The S3 bucket stays readable, and unchanged, until the
rollback window closes.

## Preconditions

- [ ] The bulk transfer completed. Every failed object was retried or explained.
- [ ] Incremental syncs run on a schedule, and the last run copied only recent changes.
- [ ] Per-prefix counts and bytes match. Sampled objects match by recomputed hash (CRC32C or SHA-256), not by ETag. Multipart S3 ETags are not MD5 hashes of the whole object.
- [ ] Archived objects (Glacier Flexible Retrieval or Deep Archive) were restored and copied, or were explicitly excluded.
- [ ] The target bucket has versioning, encryption (CMEK in protected environments), lifecycle rules, CORS and notifications re-created and tested.
- [ ] Applications read bucket names only from `HERMES_BUCKET_*`. Presigned URL generation, bucket-policy assumptions and S3-only APIs (S3 Select, Object Lambda) are ported.
- [ ] The source credential is known and scoped read-only. That is the federated role for Storage Transfer Service, or the dedicated IAM user key for Alibaba Data Online Migration, which must be revoked later.
- [ ] Retention obligations are mapped. S3 Object Lock settings and legal holds do not transfer, so target retention policies are configured.

## Roles

| Role | Responsibility |
|---|---|
| Migration lead | Runs the runbook and owns go/no-go |
| Storage owner | Runs the transfer jobs and checks counts and hashes |
| Service owners | Switch applications and run smoke tests |
| Security officer | Applies the deny-write policy and later revokes transfer credentials |
| Approver | Approves the gated steps |
| Communications lead | Stakeholder updates |

## Steps

| # | Step | Owner | Gate | Verification |
|---|---|---|---|---|
| 1 | Choose the write strategy: **freeze** (stop writers, simplest) or **dual-write** (writers write to both, needs app support) | Migration lead | **Approval** | Decision recorded |
| 2 | Freeze: stop writers or switch them to read-only mode. Dual-write: enable writes to the target | Service owners | **Approval** | S3 PUT/DELETE request metrics drop to ~0 (freeze) |
| 3 | Run the final incremental sync and wait for completion | Storage owner | — | Job finished; changed-object count matches writes since the last sync |
| 4 | Final comparison per prefix: counts, bytes, sampled hashes | Storage owner | Validation | `hermes migrate validate` object checks pass |
| 5 | Apply the spec revision with target `HERMES_BUCKET_*` (`hermes apply --approve <plan-id-prefix>`) | Migration lead | **Approval** | Services restarted with new bindings |
| 6 | Resume writers on the target; run smoke tests (upload, download, signed URL, delete) | Service owners | Validation | Smoke tests pass |
| 7 | Apply a deny-write bucket policy on the S3 bucket (keep reads) | Security officer | **Approval** | A test PUT to S3 is denied |
| 8 | Disable the incremental sync schedule; keep the job definition | Storage owner | — | No further runs |
| 9 | Announce completion and the rollback window | Comms lead | — | Announcement sent |

## Abort criteria

- The final sync reports failures, or counts do not match after the sync.
- Unknown writers are still writing to S3 after step 2.
- Smoke tests fail on the target.

To abort before step 5: resume writers on S3. Nothing on the source has changed.

## Rollback

See [rollback.md](rollback.md). Re-apply the previous spec revision to point applications back
to S3. Remove the deny-write policy first. Objects written to the target after step 6 must be
copied back to S3 (target egress, or a reverse transfer job) or they are lost to the
applications. Deletions made on the target after cutover are not propagated back
automatically.

## Communication plan

| When | Audience | Message |
|---|---|---|
| T-3 days | Service owners, users of signed URLs or public objects | Window, freeze duration, URL changes |
| Step 2 | Change channel | Freeze started |
| Step 6 | Change channel, status page | Target live |
| Abort or rollback | All | State and next attempt |
| Step 9 | All | Completed; rollback window end; S3 becomes read-only |
