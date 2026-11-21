# Networking

What network each composition builds from `network.cidr`, `network.zones` and `network.nat`; how traffic gets in
and out; how services reach the database privately; and how the target network coexists with the source during a
migration. The subnet math comes from the module code (`infra/modules/<p>/network/main.tf`). **Not tested
against a real cloud.**

## The spec

```yaml
network:
  cidr: 10.20.0.0/16    # default; must be a valid CIDR of /20 or larger (HCF118); non-private ranges warn
  zones: 2              # 2 or 3
  nat: true             # default
```

Pick a CIDR that does not overlap any network you will ever connect to: other environments, other clouds,
on-premises, and **the migration source** (hybrid routing needs disjoint ranges). Changing `network.cidr` later
replaces the network and everything in it.

## Address plans (default `10.20.0.0/16`)

| | AWS | GCP | Alibaba Cloud |
|---|---|---|---|
| Formula | private *i* = `cidrsubnet(cidr, 2, i)`; public *i* = `cidrsubnet(cidrsubnet(cidr, 2, 3), 3, i)` | subnet = `cidrsubnet(cidr, 1, 0)`; private services access = `cidrsubnet(cidr, 2, 2)` | vSwitch *i* = `cidrsubnet(cidr, 4, i)` |
| Zone 1 | private `10.20.0.0/18`, public `10.20.192.0/21` | one **regional** subnet `10.20.0.0/17` | `10.20.0.0/20` |
| Zone 2 | private `10.20.64.0/18`, public `10.20.200.0/21` | (regional) | `10.20.16.0/20` |
| Zone 3 (`zones: 3`) | private `10.20.128.0/18`, public `10.20.208.0/21` | (regional) | `10.20.32.0/20` |
| Other ranges | the rest of the last quarter is unused | `10.20.128.0/18` for Cloud SQL private IP (PSA peering); `10.20.192.0/18` reserved | the rest is unused |
| `network.zones` | number of subnet pairs and NAT gateways (when protected) | **no effect** (regional services; Cloud SQL HA picks its standby zone) | number of vSwitches; `extensions.alibaba.zone_ids` pins the zones |
| At `/20` | private `/22`, public `/25` | subnet `/21`, PSA `/22` | vSwitch `/24` |

## Ingress

| | AWS | GCP | Alibaba Cloud |
|---|---|---|---|
| Public services | one shared ALB in the public subnets; 80/443 from `extensions.aws.alb_ingress_cidrs` (default `0.0.0.0/0`); HTTPS with `acm_certificate_arn` (TLS 1.3/1.2 policy, HTTP→HTTPS redirect) | Cloud Run `run.app` HTTPS (Google front end; not through the VPC); `allUsers` invoker | one SAE internet CLB per public service; HTTPS 443 with `certificate_id`, otherwise HTTP 80 |
| Routing | first public service is the default route; others are on `/<name>/` (prefix not stripped) | one URL per service | one CLB per service |
| Private services | no public endpoint; URL `""` (no service discovery) | `INGRESS_TRAFFIC_INTERNAL_ONLY`; URL `""` | no internal load balancer; URL `""` |
| Workload ingress rule | tasks accept traffic only from the ALB security group, on the container port | the VPC has a deny-all-ingress rule (priority 65534) and no allow rules | security group: service ports from the VPC CIDR, plus `100.64.0.0/10` for public ports (load-balancer range; not re-verified for SAE) |

Custom domains and DNS records are not implemented on any provider (`dns.enabled` is reserved, `HCF111`).

## Egress

| | AWS | GCP | Alibaba Cloud |
|---|---|---|---|
| `nat: true` | NAT gateway: **one** in non-protected environments, **one per zone** when protected. An S3 gateway endpoint is always created (free; keeps S3 and ECR layer traffic off the NAT) | Cloud Router + Cloud NAT (regional, no per-zone HA needed). With the default `vpc_egress: private-ranges-only`, Cloud Run reaches the internet through Google's egress, **not** through NAT; NAT carries Cloud Run traffic only with `vpc_egress: all-traffic` | enhanced NAT gateway (`CrossAZ`) + EIP + an SNAT entry per vSwitch |
| `nat: false` | **tasks have no egress**: no interface endpoints are created, so non-ECR image pulls and AWS API calls fail (warning `HCF210`) | Cloud Run still has Google-managed egress with `private-ranges-only`; with `all-traffic` there is then no internet egress | no NAT/EIP; images must come from ACR inside the VPC (`acr_instance_id`) |
| Workload egress rule | TCP 443 to anywhere, plus 5432 to the database for services with `access.database` | implied allow-all egress | no explicit egress rules (the security group's default outbound behaviour applies) |
| Flow logs | all traffic to CloudWatch when observability is on | VPC flow logs (5-minute aggregation, 50% sampling) and NAT error logs when observability is on | not configured |

## Private database access

All three databases are **private only** and require TLS:

| | AWS | GCP | Alibaba Cloud |
|---|---|---|---|
| Placement | RDS in the private subnets, `publicly_accessible = false` | Cloud SQL private IP only (`ipv4_enabled = false`) through private services access peering | RDS in the vSwitches; allow-list = the VPC CIDR; no public connection |
| Who can connect | security group: 5432 from the task security groups of services with `access.database` | Cloud Run Direct VPC egress (private ranges) + `roles/cloudsql.client` with an IAM condition on this instance | anything in the VPC that holds the password |
| TLS | `rds.force_ssl = 1` | `ssl_mode = ENCRYPTED_ONLY` | SSL forced |
| Auth | IAM (`rds-db:connect`) | IAM database users | password (out-of-band) |
| Operator access | no bastion is created. Use a session-manager style tunnel or a temporary task inside the VPC | Cloud SQL Auth Proxy | a host inside the VPC |

## Cost of the network

NAT is usually the largest network line item. On AWS, a NAT gateway is billed per hour and per GB processed
($0.045/h and $0.045/GB in the US East (Ohio) example on the [AWS VPC pricing page](https://aws.amazon.com/vpc/pricing/),
checked 2026-11-20), and a protected 3-zone environment runs three of them. Each public IPv4 address costs
$0.005/h. Cloud NAT and Alibaba's enhanced NAT also charge per hour and per GB; see
[cost and sizing](cost-and-sizing.md) for ranges and for when NAT is avoidable.

## Hybrid networking during a migration

Hermes provisions the **target** VPC, subnets and NAT. The private link between source and target (HA VPN,
Cross-Cloud Interconnect, IPsec-VPN, Express Connect), the route tables for migration flows, DNS forwarding and
firewall translation are **not provisioned by Hermes v1alpha1**. They are manual or hook-driven steps in the
`hybrid-connectivity` and `traffic-cutover` workflows.

- Plan CIDRs first: the target `network.cidr` must not overlap the source VPCs.
- Keep the migration link in its own segment, and advertise only what migration needs.
- Database replication services reach the source over this link or over TLS on the internet. Prefer the link.

Read [network migration](../migration/network-migration.md) (VPC design differences, connectivity options, DNS)
and the [hybrid network runbook](../../migration/runbooks/hybrid-network.md).

## Known gaps

No VPC interface or Private Service Connect endpoints, no transit or hub networking, no firewall or WAF in front of
public services, no IPv6, no custom DNS, and no peering between Hermes environments. See the
[limitations register](../limitations.md).
