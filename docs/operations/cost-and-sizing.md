# Cost and sizing

What each footprint deploys, what drives its cost, where the architecture becomes unnecessarily expensive, and
how to estimate. **All figures are indicative list prices, not quotes.** They exclude taxes, discounts, free
tiers, data transfer beyond what is stated, and log volume. Nothing here was measured on a real bill, because
Hermes has not been deployed ([limitations](../limitations.md)).

Sources:

- AWS unit prices: the [AWS provider guide](../providers/aws.md#cost-notes-indicative-us-east-1-list-prices-verify-with-aws-pricing)
  (us-east-1 list prices). The NAT gateway rate ($0.045/h, $0.045/GB) and the public IPv4 rate ($0.005/h) were
  re-checked on the [AWS VPC pricing page](https://aws.amazon.com/vpc/pricing/) on 2026-11-20 (the page's US
  East (Ohio) example).
- GCP and Alibaba: relative statements from the [GCP](../providers/gcp.md#cost-notes-relative-verify-with-the-google-cloud-pricing-calculator)
  and [Alibaba](../providers/alibaba.md#cost-notes-no-infracost-support-estimate-with-alibabas-pricing-pages)
  guides. No unit prices were verified for them in this pass.

Always confirm with the [AWS Pricing Calculator](https://calculator.aws/), the
[Google Cloud pricing calculator](https://cloud.google.com/products/calculator), or Alibaba Cloud's pricing pages.

## What drives cost (in rough order)

| Driver | Why it matters | Spec knobs |
|---|---|---|
| **NAT** | Fixed hourly charge per gateway, plus a per-GB processing fee on everything that leaves through it. AWS runs one gateway per zone in protected environments | `network.nat`, `network.zones`, `protected` |
| **Database size and HA** | HA roughly doubles instance cost on all three providers. Instance class scales cost further | `database.size`, `database.high_availability`, `storage_gb`, `max_storage_gb` |
| **Always-on compute** | `min_instances × (cpu, memory)` is billed around the clock. On GCP, the tracing sidecar switches Cloud Run to instance-based billing and adds 0.5 vCPU / 512 MiB per instance | `min_instances`, `cpu`, `memory_mb`, `observability.tracing` |
| **Load balancers** | AWS: one shared ALB per environment. Alibaba: **one CLB per public service**. GCP: none (Cloud Run serves HTTPS directly) | number of `public: true` services |
| **Customer-managed keys** | A key per environment, plus requests. On Alibaba, secrets need a **KMS 3.0 instance**, which is a significant fixed monthly cost | `security.customer_managed_keys`, `secrets.names` (Alibaba) |
| **Log volume and retention** | Ingestion is billed per GB. Flow logs (enabled with observability on AWS and GCP) and database logs add volume | `observability.log_retention_days`, `observability.enabled` |
| **Public IPv4** | AWS charges $0.005/h for each public IPv4 address (NAT EIPs, ALB addresses) | `network.nat`, public services |
| **Data transfer** | Internet egress and cross-zone traffic; large during migrations | — (see the migration estimate below) |

## Footprints

### Small startup / development

- **Deploys:** 2 zones; one NAT (AWS) or none; one or two services at the smallest size with `min_instances` 0–1;
  a `small` single-zone database; no CMKs; short log retention.
- **AWS, indicative:** NAT ≈ $33 + ALB ≈ $16 (if a service is public) + one always-on 0.5 vCPU / 1 GiB task ≈ $17
  + db.t4g.small ≈ $25–30 plus storage + public IPv4 for the NAT and ALB ≈ $4 each + Secrets Manager $0.40 per
  secret + CloudWatch Logs per GB. That is **roughly $100–150/month** before data and logs.
- **GCP:** usually the cheapest dev footprint. Use `min_instances: 0`, `tracing: false` (request-based CPU, no
  sidecar), `nat: false`, a small single-zone Cloud SQL and short retention. There is no load-balancer cost. Cloud
  SQL is then the dominant fixed cost.
- **Alibaba:** `nat: false` (images from ACR inside the VPC), RDS Basic `pg.n2.2c.1m` with 20 GB ESSD, SAE
  0.5 vCPU / 1 GB × 1 (SAE cannot scale to zero), LRS buckets, and **no secrets** (which avoids the KMS instance).

### Medium (one protected production environment)

- **Deploys:** 2–3 zones; on AWS, one NAT per zone; HA database (`medium`); 2–4 services with
  `min_instances` ≥ 2; HTTPS; alerts; optional CMKs.
- **AWS, indicative:** 3 NAT gateways ≈ $100/month before data; Multi-AZ roughly doubles the database line; each
  always-on 1 vCPU / 2 GiB task ≈ $34/month (Fargate pricing is linear in vCPU and memory); the ALB ≈ $16 plus
  LCUs; CMKs $1/month per key plus requests; Container Insights and Enhanced Monitoring are billed per metric.
  Typically **several hundred USD/month**, dominated by the database and the NAT gateways.
- **GCP:** Cloud SQL `REGIONAL` (≈ 2× the instance cost), always-on Cloud Run with the sidecar, Cloud NAT if
  `all-traffic` egress is used, and logging volume.
- **Alibaba:** HA RDS (≈ 2× Basic), the enhanced NAT (hourly and per-LCU charges plus EIP traffic), one CLB per
  public service, a KMS 3.0 instance (if secrets are used), ZRS buckets, and SLS ingestion.

### Enterprise

The medium footprint plus larger database sizes (`large`/`xlarge`), 3 zones everywhere, CMKs, longer log
retention, separate accounts per environment, CI identities, and the policy and scanner toolchain. The
enterprise features that are **essential** for production are not the expensive ones:

| Essential (low cost) | Optional, and priced accordingly |
|---|---|
| Protected environments, saved-plan approvals, policy checks | 3 zones instead of 2 (on AWS, one more NAT gateway) |
| Remote state with locking and versioning | CMKs everywhere (per-key and per-request costs; on Alibaba, a KMS instance) |
| HTTPS and pinned images | `xlarge` databases and long PITR windows |
| Database HA and deletion protection | Long log retention and full flow logs |
| Per-service least-privilege identities | Container Insights / Enhanced Monitoring |
| Alerts to a real on-call address | Multi-region or multi-cloud DR (below) |

### Multiple environments

Each environment is a full copy: network, NAT, load balancer, database, keys, logs. Five environments of the
medium shape cost about five times as much. Make non-production environments small, not protected (one NAT on
AWS, no HA), and scale them to zero where the provider allows (GCP `min_instances: 0`; AWS queue consumers only;
not on Alibaba). Use `environments.<env>` overlays to change only sizes.

### Disaster recovery

Within a region, HA and PITR are part of the database line. Cross-region DR (a warm standby or a restore into a
second region) roughly duplicates the database and network fixed costs, and adds replication and transfer charges.
Hermes does not provision cross-region DR today; see the [DR strategy](../disaster-recovery/strategy.md).

### Multi-cloud

Running the same spec on two clouds roughly doubles every fixed cost: NAT, database, load balancers, keys and the
minimum compute. It also doubles the operational load (two sets of post-provision steps, alerts and IAM).
Multi-cloud is worth it when a regulatory, customer or exit requirement demands it. It is not a cheap form of DR.
The [DR strategy](../disaster-recovery/strategy.md#5-multi-cloud-dr-the-honest-cost) covers the honest cost.

## Where the architecture becomes unnecessarily expensive

- **A NAT per zone outside production.** Hermes already uses one NAT in non-protected AWS environments. Do not mark
  dev environments `protected` only to get stricter gates.
- **`nat: false` as a cost fix on AWS.** It removes the NAT cost but also all task egress, because no interface
  endpoints are created. It is only viable with ECR-only images and no AWS API calls.
- **The tracing sidecar on GCP for light services.** It forces instance-based billing. Turn tracing off in dev, or
  send OTLP elsewhere.
- **Many public services on Alibaba.** Each one gets its own CLB. Prefer one public entry service.
- **Secrets on Alibaba for a small environment.** They require a KMS 3.0 instance.
- **HA databases in every environment.** HA belongs in protected environments.
- **Long log retention everywhere.** Keep 7–14 days in dev and longer only where an audit requirement exists.
- **Oversized defaults.** The defaults are `small` databases and 0.5 vCPU / 1 GiB services (rounded up to
  1 vCPU on Cloud Run). Size up from evidence, not by default.

## Sensible defaults (what Hermes picks)

`network: {cidr: 10.20.0.0/16, zones: 2, nat: true}`, services `cpu: 0.5`, `memory_mb: 1024`,
`min_instances: 1`, `max_instances: 3`, database `size: small`, `storage_gb: 20`, `max_storage_gb: 100`,
`high_availability: false`, `backup_retention_days: 7`, observability `log_retention_days: 30`,
`security.customer_managed_keys: false`. Captured with `hermes render --print`. See the
[quickstart](../tutorials/quickstart.md#4-see-the-normalized-spec).

## Estimating

| Tool | Providers | How |
|---|---|---|
| `hermes cost` | AWS, GCP | Runs `infracost breakdown --path plan.json` on the **saved plan** (run `hermes plan` first). Infracost uses its own pricing API and API key. Without Infracost, the command explains what is missing; it **never invents numbers**. Not tested in this project (Infracost was not installed) |
| Provider calculators | all | Use the resource list from `plan.json` or the provider guide |
| `PricingAssumptions` | migration (AWS → GCP or Alibaba) | `hermes migrate assess` computes a transfer-cost **range** from [`migration/pricing/assumptions.yaml`](../../migration/pricing/assumptions.yaml) (24 rates, retrieved 2026-11-18, each with a source URL and an evidence label). Override it with `--pricing` for negotiated rates |
| Alibaba | — | Infracost does not price Alibaba Cloud. Use Alibaba's pricing pages and calculator |

Example of the migration estimate (sample inventory, a real capture):

```text
Data to migrate: 1953597383680 bytes required; indicative transfer cost USD 97.68-332.11 (range from pricing assumptions, not a quote).
```
