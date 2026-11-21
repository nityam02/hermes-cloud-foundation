# Monitoring and alerting

What each provider composition alerts on, how telemetry flows, suggested SLOs and alert routing.
Status: alert resources are implemented in the compositions and mock-tested; thresholds have not
been tuned against real workloads.

## 1. Built-in alerts

Alerts notify `observability.alert_email` when it is set (SNS topic / notification channel /
contact group); without it they are created with no recipient.

| Signal | AWS (CloudWatch) | GCP (Cloud Monitoring) | Alibaba (CloudMonitor) |
|---|---|---|---|
| Service CPU high | ECS service `CPUUtilization` | — (Cloud Run scales) | — (SAE scales) |
| HTTP 5xx | ALB `HTTPCode_ELB_5XX_Count` and per-target `HTTPCode_Target_5XX_Count` | Cloud Run `request_count` with `response_code_class = 5xx` (> 5 in 5 min) | — (designed: SLS log alert on 5xx) |
| Database CPU | RDS `CPUUtilization` | Cloud SQL CPU > 80 % for 10 min | RDS `CpuUsage` > 80 % |
| Database storage | RDS `FreeStorageSpace` low | Cloud SQL disk > 85 % | RDS `DiskUsage` > 80 % |
| Dead letters | SQS DLQ `ApproximateNumberOfMessagesVisible` > 0 | — (designed: dead-letter subscription backlog) | — |

Gaps worth adding: queue age (oldest message), database connections and replication lag,
certificate expiry, and alerts on CI identity use outside CI (see below).

## 2. Security monitoring (not provisioned by Hermes)

Enable at the account/organisation level, outside the compositions:

- Audit logs: CloudTrail (management + S3 data events for the state bucket), Cloud Audit Logs
  (Admin Activity + Data Access for the state bucket and Secret Manager), ActionTrail.
- Alert on: assumption of CI apply identities outside a GitHub Actions run, use of break-glass
  principals, changes to CI identities or OIDC providers, `PutBucketPolicy`/IAM policy changes on
  state buckets, KMS key deletion scheduling, secret value reads by unexpected principals,
  disabled deletion protection.
- Threat detection: GuardDuty, Security Command Center, Security Center (Alibaba).

## 3. Telemetry pipeline (OpenTelemetry)

Services receive `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_EXPORTER_OTLP_ENDPOINT` and
`OTEL_EXPORTER_OTLP_PROTOCOL` (data-contracts §2). With tracing enabled the endpoint is a local
collector (`http://localhost:4317`, gRPC) that exports to the provider's backend: CloudWatch/X-Ray
on AWS, Cloud Trace/Monitoring on GCP, SLS on Alibaba. Applications only speak OTLP, so moving
providers does not change instrumentation. Logs go to the provider's log service with
`log_retention_days`. Do not log secrets or full request bodies; the log destinations are
readable by operators.

## 4. Suggested SLOs

| SLO | Target (medium tier) | Measured by |
|---|---|---|
| Availability of public APIs | 99.9 % of requests non-5xx over 30 days | load balancer / Cloud Run request metrics |
| Latency | 95 % of requests < 300 ms | OTLP traces or LB latency metrics |
| Telemetry freshness | 99 % of device telemetry processed within 60 s | queue age, consumer lag |
| Database recoverability | successful PITR rehearsal each quarter | DR test log |

Alert on error-budget burn rate (for example 2 % of the monthly budget in 1 hour pages; 5 % in 6
hours opens a ticket) rather than on single threshold breaches.

## 5. Alert routing

- Page (24/7 on-call): SLO fast burn, database storage critical, dead letters in safety-relevant
  queues, security alerts from §2.
- Ticket (business hours): CPU high, slow burn, certificate expiry within 21 days.
- Route by `hermes-environment`: production pages, other environments open tickets.
- Use a team distribution address or an incident-management integration for `alert_email`,
  never a personal mailbox.
