# Backup and restore procedures

Concrete steps per provider. These procedures follow the providers' documented tools; **they have
not been rehearsed against real accounts by this project**. Run them first in a non-production
environment. Every restore that changes managed resources ends with a `hermes plan` to bring IaC
and reality back in line.

Conventions: `<project>-<env>-…` names from data-contracts §2; examples use fake values.

## 1. Database point-in-time recovery (PITR)

Restore into a **new** instance, verify, then switch applications. Never overwrite the original
until the restore is verified.

**AWS RDS PostgreSQL**

```bash
aws rds describe-db-instances --db-instance-identifier sample-production-db \
  --query 'DBInstances[0].LatestRestorableTime'
aws rds restore-db-instance-to-point-in-time \
  --source-db-instance-identifier sample-production-db \
  --target-db-instance-identifier sample-production-db-restore \
  --restore-time 2026-11-20T09:30:00Z \
  --db-subnet-group-name sample-production-db --no-publicly-accessible \
  --vpc-security-group-ids sg-0123456789abcdef0
```

Then verify data, and either point the application at the restored instance (update the spec or
rename instances) or copy the missing rows back. Import or replace in IaC deliberately: a renamed
instance appears in the next plan as a change.

**GCP Cloud SQL**

```bash
gcloud sql instances clone sample-production-db sample-production-db-restore \
  --point-in-time 2026-11-20T09:30:00Z
```

The clone keeps private IP settings of the source. Verify, then switch.

**Alibaba ApsaraDB RDS PostgreSQL**

Console or API: *Backup and Restoration → Restore Database (point in time)* into a new
instance (`CloneDBInstance` with `RestoreTime`), in the same VPC and vSwitch, with the same
whitelist (never `0.0.0.0/0`).

## 2. Bucket object versions

**AWS S3**

```bash
aws s3api list-object-versions --bucket sample-production-artifacts-1a2b --prefix path/to/key
aws s3api copy-object --bucket sample-production-artifacts-1a2b --key path/to/key \
  --copy-source 'sample-production-artifacts-1a2b/path/to/key?versionId=<version-id>'
# an object "deleted" in a versioned bucket has a delete marker: remove the marker to restore
aws s3api delete-object --bucket sample-production-artifacts-1a2b --key path/to/key --version-id <delete-marker-id>
```

**GCP Cloud Storage**

```bash
gcloud storage ls --all-versions gs://sample-production-artifacts-1a2b/path/to/key
gcloud storage cp gs://sample-production-artifacts-1a2b/path/to/key#<generation> gs://sample-production-artifacts-1a2b/path/to/key
```

**Alibaba OSS**

```bash
ossutil ls oss://sample-production-artifacts-1a2b/path/to/key --all-versions
ossutil cp oss://sample-production-artifacts-1a2b/path/to/key oss://sample-production-artifacts-1a2b/path/to/key --version-id <version-id>
```

Buckets created with `versioning: false` cannot be restored this way; noncurrent versions expire
according to the lifecycle rules.

## 3. State file rollback

Use when an apply wrote a bad state or state was corrupted. Stop all CI runs for the environment
first.

1. Find the previous version of
   `<prefix>/<project>/<env>/<provider>/terraform.tfstate` (`list-object-versions`,
   `gcloud storage ls --all-versions`, `ossutil ls --all-versions`).
2. Download it and inspect `serial` and `lineage` (the lineage must match).
3. Upload it as the current version (copy the old version over the key), keeping the bad version
   for analysis.
4. Run `hermes plan --env <env>`: the plan shows the difference between the restored state and
   reality. Resources created after the restored version appear as "create" (they exist but are
   untracked): import them instead of re-creating them.
5. Record the rollback in the incident log.

With OpenTofu client-side encryption, a restored version needs the key (or the fallback key) it
was written with.

## 4. Secrets re-creation

IaC manages secret containers, never values. When a container is deleted or replaced (HCF-POL-007
warns in the plan):

1. Recover if possible: AWS Secrets Manager `restore-secret` within the recovery window (30 days in
   protected environments); GCP has no soft delete, so destroyed versions cannot be recovered;
   Alibaba KMS secrets `RestoreSecret` within the recovery window.
2. Otherwise, re-apply to create the container, then set the value out-of-band:
   `aws secretsmanager put-secret-value`, `gcloud secrets versions add`, Alibaba
   `PutSecretValue` (the module's placeholder value is replaced; it stays ignored by IaC).
3. Rotate every credential the old value granted (database users, API keys); restart services so
   they read the new value.

## 5. Whole-environment rebuild

1. Restore or re-create the state backend (bootstrap module).
2. Restore the database from backup or PITR into the new environment.
3. `hermes plan` / `hermes apply` with the saved-plan gates; set secret values; restore bucket data
   (provider transfer services or versioned copies).
4. Validate with `hermes migrate validate`-style checks (counts, checksums, application probes)
   before moving traffic.
