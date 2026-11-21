# Object storage migration (S3 → Cloud Storage / OSS)

> Status: designed guidance backed by the `object-storage` workflow and the
> [cutover runbook](../../migration/runbooks/object-storage-cutover.md). Hermes provisions target
> buckets. Data movement is delegated to provider tools. Not tested against a real cloud.

## Tools

| | Storage Transfer Service (to Cloud Storage) | Data Online Migration (to OSS) | ossimport (to OSS) | rclone / gcloud storage / ossutil |
|---|---|---|---|---|
| Source credential | Federated AWS role (no long-lived key) or access key | **Long-lived IAM user access key** | Access key | Access key or role on the host |
| Scope per job | Bucket, prefixes or manifest | One bucket per task | Configured | Any |
| Incremental | Scheduled or event-driven (S3 notifications via SQS); deletions not propagated in event-driven mode | Incremental tasks | Re-runs | Sync mode |
| Integrity | End-to-end checksum validation when source checksum metadata exists; `DATA_LOSS` on mismatch | Check the service documentation for its verification options [U] | **No verification after copy** | Tool-dependent (`--checksum`) |
| Service fee | Agentless: none; managed private network 0.03–0.08 USD/GiB, replacing AWS egress | Reported free during public preview (unverified) | Free | Free |

## Assessment checklist

Per bucket: bytes and object count (count dominates for small objects), storage classes
(archived objects need a restore first), versioning (only current versions move by default; a
manifest can name versions), encryption (SSE-KMS needs `kms:Decrypt` for the transfer role),
Object Lock, lifecycle, replication, notifications, policies, CORS, website hosting and
requester pays.

## Metadata mapping (Storage Transfer Service)

| S3 | Cloud Storage | Notes |
|---|---|---|
| Content-Type, Cache-Control, Content-Disposition | Preserved | Fixed-key metadata |
| User metadata (`x-amz-meta-*`) | Preserved as custom metadata | OSS: total user metadata limited to 8 KB |
| ETag | Stored as `x-goog-source-etag` | Not comparable across providers |
| ACLs | **Not preserved** | Use uniform bucket-level access and IAM |
| Object tags | **Not preserved** | Re-create as metadata if needed |
| Timestamps | **Not preserved** | `timeCreated` is the copy time; age-based lifecycle restarts |
| Storage class | Destination default or a chosen class | Map classes explicitly |
| Object Lock / legal hold | **Not preserved** | Configure retention policies on the target |

## Checksums

Multipart S3 uploads have an ETag that is not an MD5 of the object; it is derived from part
digests plus a part count. Composite Cloud Storage objects carry only CRC32C, with no MD5. Do
not compare ETags. Rely on the transfer service's end-to-end validation, and for spot checks
recompute CRC32C or SHA-256 on both sides.

## Incremental sync and cutover

1. Run the bulk transfer, throttled if the source serves production traffic.
2. Schedule incrementals until the changed-object count approaches the application's write rate.
3. Freeze writes, or dual-write. Run the final sync. Validate counts and bytes per prefix.
4. Switch `HERMES_BUCKET_*` with an approved apply. Then apply a deny-write policy on S3 and keep it readable for the rollback window.

Never enable deletion of source objects in a transfer job. Enable deletion of
destination-only objects only for the final sync, if at all.

## Egress and cost

AWS bills data transfer out and GET/LIST requests on the source side (tiered egress from about
0.09 USD/GB after 100 GB/month free). The AWS exit programme may waive egress on request. The
Google-managed private network replaces AWS egress for supported S3 regions. OSS bills
destination requests. Archive restores and retrievals are extra. Rates:
`migration/pricing/assumptions.yaml`.
