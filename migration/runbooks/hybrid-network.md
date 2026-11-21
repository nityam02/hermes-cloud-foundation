# Runbook: hybrid network between AWS and the target cloud

| | |
|---|---|
| Workflow | `migration/workflows/hybrid-connectivity.yaml` |
| Status | Designed procedure. Not tested against a real cloud. Hermes v1alpha1 does not provision hybrid connectivity. |

## Purpose

Build private, redundant connectivity between AWS VPCs and the target VPC for the coexistence
period. It carries database replication, service-to-service calls and DNS resolution, under
explicit firewall and route controls. It also moves VPN relays and device-facing tunnels
safely, separately from the bulk cutover.

## Preconditions

- [ ] Throughput requirement computed: bytes to move / transfer window, plus replication and application traffic. Bulk object data may go over the Storage Transfer Service managed private network or the internet instead.
- [ ] CIDR plan: the target VPC CIDR overlaps no source VPC or on-premises range. Advertised prefixes are limited to what migration needs.
- [ ] Private ASNs chosen for both sides; link-local /30 inside-tunnel ranges allocated.
- [ ] Owners for the AWS VPC, transit gateway route tables and target network are identified.
- [ ] A place to hold pre-shared keys as secrets exists (never in files, tickets or chat).

## Options

| Pair | Option | Notes |
|---|---|---|
| AWS ↔ GCP | HA VPN + Cloud Router (BGP) | Four tunnels for the 99.99% HA VPN SLA: two AWS Site-to-Site VPN connections (A, B), each with two tunnels, defined as one external VPN gateway with `FOUR_IPS_REDUNDANCY`. VPN data still incurs AWS data transfer out plus hourly connection fees |
| AWS ↔ GCP | AWS Interconnect – multicloud / Cross-Cloud Interconnect | Private dedicated capacity. AWS Interconnect – multicloud has been generally available since April 2026 with Google Cloud as launch partner, in a limited set of region pairs; it is provisioned from the AWS side with an activation key, and the Google side used CLI-only provisioning at GA. Priced by bandwidth (not modelled in `PricingAssumptions`) |
| AWS ↔ Alibaba Cloud | VPN Gateway IPsec-VPN, dual tunnel, BGP | Two customer gateways on AWS (the Alibaba tunnels have different IPs); enable BGP (local ASN, tunnel CIDR, local BGP IP per tunnel); destination-based routing |
| AWS ↔ Alibaba Cloud | Express Connect via a connectivity partner | Dedicated capacity; lead time and partner contracts |

## Roles

Network engineer (AWS), network engineer (target), security officer (firewall review), migration
lead, approver, on-call SRE.

## Steps

| # | Step | Owner | Gate | Verification |
|---|---|---|---|---|
| 1 | Record the option, capacity and addressing in `docs/decisions` | Migration lead | **Approval** | ADR merged |
| 2 | Target: create the VPN gateway and router (GCP HA VPN + Cloud Router, or Alibaba VPN Gateway) | Target network engineer | **Approval** | Gateway public IPs recorded |
| 3 | AWS: create customer gateways and Site-to-Site VPN connections; attach to a virtual private gateway or transit gateway | AWS network engineer | **Approval** | Configuration downloaded; PSKs stored as secrets |
| 4 | Configure tunnels and BGP on the target with the AWS-provided inside addresses and keys | Target network engineer | — | All tunnels UP; BGP established on every tunnel |
| 5 | Enable route propagation only on the AWS route tables that need it; check transit gateway route table impact | AWS network engineer | **Approval** | Only migration prefixes learned on each side |
| 6 | Firewalls: allow only the required flows (for example 5432 from replication ranges, application ports from target subnets) on AWS security groups and target firewall rules/security groups | Security officer | **Approval** | Negative test from a non-allowed range fails |
| 7 | DNS: target forwarding zones → Route 53 Resolver inbound endpoints; Route 53 Resolver outbound endpoints and rules → target resolvers | Network engineers | **Approval** | Private names resolve both ways; no loops |
| 8 | Validate: throughput (bulk TCP test), failover (disable one tunnel), path MTU / MSS clamping, latency p50/p95 | Network engineers | Validation | Requirement met with one tunnel down |
| 9 | Monitoring: alerts for tunnel down and BGP down on both sides | On-call SRE | — | Test alert received |

## VPN relays and device command-and-control traffic

EC2 instances with source/destination check disabled (catalog entry `aws-ec2-network-appliance`)
and Client VPN endpoints often relay traffic for devices in the field. When that traffic
includes **command and control**, treat the move as safety-critical:

1. Build the target relay alongside the source relay. Never replace the source relay in place.
2. Dual-running window: both relays serve at the same time for weeks, not hours.
3. Move devices in small cohorts, starting with test or non-critical devices. Keep a tested fallback to the old relay that the device applies automatically if the new path fails.
4. Gate every cohort on healthy command-and-control sessions and on the device fleet's operational status.
5. **Never** include relays or device endpoints in a bulk DNS or traffic cutover. Decommission the source relay only after the last device has run on the target relay for the agreed period.

## Abort criteria

Tunnels flap, BGP sessions are unstable, prefixes leak (unexpected routes learned), throughput
falls short of the requirement, or any device cohort loses command-and-control connectivity.

## Rollback

Withdraw advertised routes, or disable route propagation first. That removes the path without
deleting configuration. Then delete the VPN resources if needed. For relays, devices fall back
to the source relay, which stays up for the whole dual-running window.

## Cost and latency notes

- VPN traffic out of AWS is billed at AWS data transfer out rates plus per-connection hourly fees. Traffic out of the target is billed at the target's egress rates (rollback and reverse replication).
- Cross-cloud calls add tens of milliseconds even within one metro area. Chatty service-to-service traffic across the link is a common latency and cost surprise. Move dependencies in waves (assessment `dependencies`).

## Communication plan

Announce tunnel builds and route changes in the network change channel with the advertised
prefixes. Device-cohort moves are communicated to the fleet operations team before each cohort,
with the fallback procedure attached.
