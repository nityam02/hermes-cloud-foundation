# Runbook: DNS-based traffic cutover

| | |
|---|---|
| Workflow | `migration/workflows/traffic-cutover.yaml` |
| Status | Designed procedure. Not tested against a real cloud. |

## Purpose

Move user and partner traffic for one or more hostnames from AWS endpoints to target endpoints
in stages. Every stage has a hold period and a numeric go/no-go. Changing the weight back is
the rollback.

## Preconditions

- [ ] Data behind the endpoints is already served from the target, or is reachable from it within latency budgets. Database and object cutovers either come first or are part of this change.
- [ ] TTLs of the records to change were lowered (for example to 60 s) at least one old-TTL period earlier, preferably two. Confirm with `dig +noall +answer <name>` against the authoritative servers.
- [ ] Target certificates are ACTIVE for every hostname (Certificate Manager DNS authorization, or a certificate uploaded to or issued in Alibaba Cloud Certificate Management Service). CAA records permit the issuing CA.
- [ ] Preflight passed against target endpoints with `curl --resolve <host>:443:<target-ip>`: TLS chain, redirects, headers, CORS, body size limits, timeouts, WebSockets and health endpoints.
- [ ] Health checks exist on both sides (for example a Route 53 health check per weighted record), so a failing target can be removed from rotation.
- [ ] Partners updated their allowlists with target egress IPs. Webhooks and OAuth redirect URIs include the target.
- [ ] Rollback triggers are written down, for example: 5xx rate above 1% for 5 minutes, p95 latency above SLO+20% for 10 minutes, or any security alert.
- [ ] Device-facing endpoints and VPN relays are **excluded** from this bulk procedure. They follow their own cohort plan ([hybrid-network.md](hybrid-network.md)).

## Roles

| Role | Responsibility |
|---|---|
| Migration lead | Runs stages and owns go/no-go |
| DNS operator | Changes weights and TTLs |
| On-call SRE (source and target) | Watches SLO dashboards on both clouds |
| Service owners | Functional checks at each stage |
| Approver | Approves each weight increase |
| Communications lead | Updates |

## Stage gates

| Stage | Target weight | Minimum hold | Gate | Verification before the next stage |
|---|---|---|---|---|
| 0 | 0% (records created with weight 0) | — | **Approval** | Records resolve; target health check is green |
| 1 | 1% | 30 min | **Approval** | Target error rate and latency within triggers; logs and traces arrive |
| 2 | 10% | 2 h | **Approval** | Same as above, plus business metrics (sign-ups, orders) unaffected |
| 3 | 50% | 24 h (covering a peak) | **Approval** | Capacity headroom on the target ≥ 30% at peak |
| 4 | 100% | Rollback window (for example 7–14 days) | **Approval** | Source traffic trends to ~0 (residual resolvers and hard-coded clients) |

Weights are a coarse control. Resolvers cache answers, and large clients may pin to one answer.
Expect the observed split to differ from the configured split.

## Steps per stage

| # | Step | Owner | Gate | Verification |
|---|---|---|---|---|
| 1 | Announce the stage | Comms lead | — | Posted |
| 2 | Change the weights in the current DNS provider | DNS operator | **Approval** | Authoritative answers show the new weights |
| 3 | Hold and observe for at least the minimum hold | On-call SRE | — | Dashboards within triggers |
| 4 | Go/no-go for the next stage | Migration lead | **Approval** | Recorded decision |

## Abort criteria

Any rollback trigger fires, the target health check fails, a data-consistency alarm fires, or
the source or target security team asks to stop.

## Rollback

Set the target weight to 0 (and the source weight back to 100). The effect takes about one TTL
plus resolver caching. The source stack must stay fully running until stage 4 has held for the
whole rollback window. If data was already switched, follow [rollback.md](rollback.md) for the
data layer first.

## After the window

- Close the rollback window with sign-off (`traffic-cutover` step `close-rollback-window`).
- Optional, later: move DNS hosting to Cloud DNS or Alibaba Cloud DNS. Recreate and compare every record first. Alias records become A/AAAA or CNAME records. With DNSSEC, follow a safe DS-record procedure before the NS change.
- Restore normal TTLs.

## Communication plan

| When | Audience | Message |
|---|---|---|
| T-7 days | Partners, support, stakeholders | Window, new egress IPs, contacts |
| Each stage | Change channel, status page | Stage, weight and observations |
| Rollback | All | Weights reverted, impact and next attempt |
| Stage 4 + window | All | Completed; source decommission scheduled (never automatic) |
