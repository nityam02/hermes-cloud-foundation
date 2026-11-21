# Service mapping: how to read the catalog

`migration/mappings/catalog.yaml` (kind `ServiceCatalog`, data-contracts §4) maps AWS resource
types to GCP and Alibaba Cloud targets. The full table is **generated**. Do not maintain one by
hand:

```bash
hermes migrate catalog --format markdown
```

> Status: catalog data written and validated against the contract shape by a local script
> (82 entries; all enums, workflow references and match rules checked). Reference URLs were
> link-checked on 2026-11-18. Mappings describe **capability equivalence, not operational
> equivalence**.

## Reading one entry

| Field | Meaning |
|---|---|
| `source.resource_types` | CloudFormation / Cloud Control type names, as in `Inventory.resources[].type` |
| `source.match` | Optional attribute → allowed values. All keys must match. Entries with `match` are evaluated before entries without, so the entry without `match` is the fallback for a type |
| `compatibility` | `equivalent` (drop-in), `compatible` (same engine/semantics, configuration differs), `partial` (subset; redesign some parts), `re-architect` (different model), `none` (no target), `unknown` (not verified) |
| `complexity` | Effort and risk of the move, not of the target service |
| `data_migration` | `required` (state must move), `optional` (history/replay only), `none` (rebuild) |
| `lock_in` | Lock-in of the **source** service. PostgreSQL is low; DynamoDB is high |
| `hermes_support` | Can Hermes v1alpha1 provision the target? `implemented` = inside the v1alpha1 Platform scope: VPC network, containerized services (Cloud Run / SAE), PostgreSQL (Cloud SQL / ApsaraDB RDS for PostgreSQL), buckets (Cloud Storage / OSS), topics and subscriptions (Pub/Sub / SMQ), secret containers, observability basics. Adapter implementation status is tracked in `docs/limitations.md` |
| `workflow` | The `MigrationWorkflow` that drives the steps |
| `differences` / `issues` | What differs, and what typically goes wrong |

## Attribute keys used by `match`

The catalog relies on these normalized inventory attribute names. The inventory collector must
emit them with the AWS API's native values (case-sensitive):

| Resource type | Attribute | Values used |
|---|---|---|
| `AWS::RDS::DBInstance`, `AWS::RDS::DBCluster` | `engine` | `postgres`, `aurora-postgresql`, `mysql`, `aurora-mysql`, `aurora`, `mariadb`, `sqlserver-ee/se/ex/web`, `oracle-ee/se2/ee-cdb/se2-cdb` |
| `AWS::ElasticLoadBalancingV2::LoadBalancer` | `type` | `application`, `network` |
| `AWS::ElastiCache::*` | `engine` | `redis`, `valkey`, `memcached` |
| `AWS::SQS::Queue` | `fifo_queue` | `true` (boolean) |
| `AWS::EC2::Instance` | `source_dest_check` | `false` (boolean): routers, NAT instances, VPN relays |

A resource whose attribute is missing falls through to the type's fallback entry, or to
`manual-review` when the type has none.

## Similar names that are not equivalent

| Source | Target | Why it is not a rename |
|---|---|---|
| SQS queue | Pub/Sub subscription | No standalone queue. Ack deadline is at most 600 s (SQS visibility allows up to 12 h). No delay queues. Dead-lettering needs 5–100 attempts. Subscription retention is at most 7 days (SQS 14). Exactly-once delivery only for pull subscriptions in one region |
| SQS queue | SMQ (formerly MNS) queue | Retention at most 7 days. Message size far below SQS's 1 MiB, so large payloads need a claim-check. No SQS FIFO semantics |
| SNS fan-out to SQS | Pub/Sub topic with subscriptions | Native fan-out, but filters are fixed at subscription creation. Email/SMS/mobile push have no Pub/Sub equivalent |
| ALB | Cloud Load Balancing | Built from several resources (forwarding rule, proxy, URL map, backend service). Global anycast option. Hermes provides HTTPS ingress only for public services |
| RDS Multi-AZ | Cloud SQL regional HA | Multi-AZ repoints DNS; Cloud SQL keeps the IP. Both drop connections, neither standby serves reads, failover times differ; test reconnects |
| S3 ACLs / bucket policies | Cloud Storage UBLA + IAM | No bucket policy language. Storage Transfer Service keeps neither ACLs nor object tags nor timestamps |
| S3 | OSS | Similar API family, different auth signatures, endpoints, metadata limits (8 KB user metadata) and event model |
| Lambda | Cloud Run functions / Function Compute | Handler signatures, event formats, triggers and layers differ |
| IAM roles and policies | GCP IAM / RAM | No 1:1 policy translation. Policies are re-derived from need |
| Aurora | Cloud SQL / AlloyDB / PolarDB | Aurora-only features have no equivalent |
| ElastiCache | Memorystore / Tair | ElastiCache restricts replication commands, so online replication from it is limited |
| ACM certificate | Certificate Manager / Certificate Management Service | Public ACM certificates are not exportable unless requested as exportable (since 2025-06-17). Re-issue on the target |
| Cognito | Identity Platform | No password-hash export. Users reset passwords or are migrated lazily |
| IoT Core | — | Cloud IoT Core was retired on 2023-08-16. Alibaba IoT Platform stopped accepting new subscriptions |
| EC2 VPN relay (source/dest check off) | Instance with IP forwarding | Safety-critical when it carries device command-and-control traffic. Dual-running window, never part of a bulk cutover |

## Review status and evidence (2026-11-18)

Evidence labels follow `docs/research/open-source-landscape.md`: **[V]** read on official pages
in this review, **[S]** search summaries of official pages or secondary sources, **[U]**
maintainer knowledge.

| Claim area | Evidence |
|---|---|
| Google DMS PostgreSQL limitations and RDS/Aurora source configuration; MySQL source privileges and GTID_MODE | [V] (docs fetched) |
| Storage Transfer Service metadata preservation | [V]; data integrity, managed private network, agentless/agent pricing [S] |
| Alibaba DTS billing, Amazon RDS PostgreSQL source requirements, Aurora PostgreSQL guide scope | [S] |
| Alibaba Data Online Migration (long-lived key, one bucket per task); STS federated role | [S] (open-source investigation) |
| Alibaba CDT internet prices and free quota | [V] (page updated 2026-08-05) |
| AWS exit programme (180 days non-EU, Support request) | [V] (AWS blog, updated 2026-10-08) |
| AWS Interconnect – multicloud GA (April 2026, Google Cloud partner) | [V] (AWS What's New) |
| X-Ray SDK maintenance (2026-02-25) and end of support (2027-02-25); SQS 1 MiB; ACM exportable certificates | [S] (official pages via search) |
| Alibaba IoT Platform no new subscriptions | [V] (help page updated 2026-07-24) |
| Alibaba TSDB phase-out, TSDB for InfluxDB EOL 2026-10-23 | [S] |
| Timestream for LiveAnalytics closed to new customers (2025-06-20) | [S] (open-source investigation) |
| Terraformer archived 2026-03-16 | [V] (repository) |
| SMQ message-size limit, Firestore with MongoDB compatibility coverage, Parameter Manager availability, Alibaba managed Memcached and CIAM offerings, RDS for MariaDB availability on Alibaba | [U]: flagged "verify" in the entries |
| Most other entries (networking, compute, observability, analytics) | [U]: maintainer knowledge; re-review before relying on details |

Link check: `lychee` on 138 reference URLs on 2026-11-18. 130 returned HTTP 2xx, some after
redirects. 8 on `help.aliyun.com` timed out from the review machine; those URLs were taken from
search results. A 2xx response shows the page exists, not that it supports the claim.

## Extending the catalog

Add an entry when a resource type recurs in assessments. Keep `id` unique and kebab-case.
Provide both `gcp` and `alibaba` targets, and use `unknown` honestly. Reference a reserved
workflow. Put the attribute you `match` on into the table above, so the collector emits it.
