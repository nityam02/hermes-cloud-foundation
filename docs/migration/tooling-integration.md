# Tooling integration

Hermes integrates managed migration tools instead of reinventing them. Every tool below is
either **integrated** (a workflow step names it as `automation: provider-tool`), a
**complement** (useful beside Hermes, not called by it), or **not used** (with the reason).

> Status: decisions for the data and workflows in `migration/`. Statements about third-party
> tools were checked on 2026-11-18 where marked; see the evidence table in
> [service-mapping.md](service-mapping.md#review-status-and-evidence-2026-11-18).

| Tool | Decision | Where | Reason |
|---|---|---|---|
| Google Database Migration Service | **Integrated** | `relational-database-postgresql`, `relational-database-mysql` | Continuous homogeneous migration into Cloud SQL / AlloyDB at no additional charge. Its limitations (DDL, tables without PK, large objects, roles) are encoded as workflow steps |
| Alibaba Data Transmission Service | **Integrated** | Same workflows | Schema + full (free) + incremental (billed hourly). DML only; manual sequences; Aurora PostgreSQL incremental support to be confirmed |
| AWS DMS | **Complement** | `database-migration.md` | Runs from the AWS side and suits heterogeneous moves. It does not migrate sequences during ongoing replication and bills instance hours. The target-side tools are the defaults |
| Google Storage Transfer Service | **Integrated** | `object-storage` | Agentless; federated AWS role (no long-lived key); end-to-end checksums; optional managed private network |
| Alibaba Data Online Migration | **Integrated, with a security exception** | `object-storage` | Needs a long-lived IAM user key and one task per bucket. The key is created behind an approval gate, never stored by Hermes, and revoked in `decommission` |
| ossimport | **Complement** | `object-storage` | Read-only on the source, but no post-copy verification. Hermes validation must run afterwards |
| AWS DataSync | **Complement** | `object-storage-migration.md` | Agentless GCS ↔ S3 transfers (Enhanced mode) are reported, which helps a reverse/rollback copy into S3 [S]. Not the default for AWS → target |
| Google Cloud Migration Center (AWS discovery) | **Complement** | Sizing input | Useful for utilization and TCO data. Its AWS discovery authenticates with a long-lived access key, is partly in Preview (EC2 and RDS GA), and is not a source for the Hermes `Inventory` |
| AWS Application Discovery Service | **Not used** | — | Closed to new customers since 2025-11-07. AWS points to AWS Transform. Hermes discovery is agentless and API-based instead |
| AWS Transform | **Not used** | — | AWS migration and modernization service aimed at moving workloads into AWS; not needed for cross-cloud capability mapping |
| Terraformer | **Not used** | — | Repository archived on 2026-03-16; no security fixes |
| former2 | **Complement (reference)** | `adopt` research | Active (last push 2026-07), MIT, browser-based, AWS-only. Useful to cross-check generated source configuration, not as a dependency |
| Import blocks + `-generate-config-out` | **Integrated** (via `hermes adopt aws`) | `foundation` step `adopt-source-iac` | Hermes generates `import` blocks from its own `Inventory`, which carries ids and CloudFormation types. The engine performs the import. `-generate-config-out` is experimental in both engines and can write sensitive values, so its output is reviewed and never committed unreviewed |
| `terraform query` / `list` blocks | **Not used** | — | Exists only in Terraform ≥ 1.14. OpenTofu (the Hermes default) has no bulk import discovery, so Hermes's inventory-driven import generation is the portable path |
| MirrorMaker 2 | **Integrated (manual)** | `messaging` | Kafka topic replication and offset translation for MSK → managed Kafka |
| OCI copy tools (skopeo, crane) | **Integrated (manual)** | `container-workload` | Copy images by digest |
| Velero | **Complement** | EKS → GKE/ACK | Kubernetes object and volume backup/restore. Kubernetes is reserved in Hermes v1alpha1 |
| moto | **Used for tests** | Discovery demo | LocalStack's community repository was archived on 2026-03-23 and its image needs an auth token. moto has no Cloud Control or Resource Explorer backend |
| CloudQuery, Steampipe, Cartography | **Not used / complement** | — | CloudQuery now gates releases behind a login. Steampipe's CLI is AGPL; it is fine side by side, not embedded. Cartography is a reference for relationship vocabulary |

## Rules for any integrated tool

1. A step that creates something in the source account (users, keys, slots, endpoints, roles) has `source_impact: modifies-source-config` and `gate: approval`. A matching removal step exists in `decommission`.
2. Credentials for tools are entered out-of-band by an operator. Hermes never stores them.
3. Tool output is never trusted by itself. `hermes migrate validate` runs independent checks (counts, checksums, presence).
4. Starting, promoting or completing a tool job is a human action behind a gate. Hermes does not trigger cutover through a tool.
