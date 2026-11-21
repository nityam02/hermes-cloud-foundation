# IAM strategy

Who (or what) gets which permissions, on each provider, and why. Status: the CI identities are
implemented and mock-tested (`infra/bootstrap/<provider>/ci-identity`, `tofu test`); the workload
identities are part of the provider compositions (mock-tested). Nothing here has been exercised
against a real cloud account.

## 1. Identity types

| Identity | Credential | Lifetime | Permissions |
|---|---|---|---|
| Humans (operators) | SSO: AWS IAM Identity Center, Google Workspace/Cloud Identity, Alibaba CloudSSO | session (1–12 h) | read-only by default; deploy rights via a time-boxed elevated role; production applies through CI |
| CI plan | GitHub OIDC → role / service account | 1 h | read metadata, read state, write lock files |
| CI apply (one per environment) | GitHub OIDC with `environment:<env>` subject | 1 h | deploy the composition into that environment only |
| Workloads (one per service) | ECS task role / Cloud Run service account / RAM role | platform-managed | exactly the `access` block of the spec |
| Discovery (migration source) | role in the source account, assumed with ExternalId | 1 h | allowlisted read-only API calls ([migration discovery README](../../migration/discovery/README.md)) |
| Break-glass | two named emergency principals per cloud, hardware MFA, credentials sealed | used rarely | administrator; every use alerts and is reviewed |

There are **no long-lived access keys** for CI or workloads. HCF-POL-009 denies
`aws_iam_access_key`, `google_service_account_key` and `alicloud_ram_access_key` in plans.
Migration tools that only accept static keys (Alibaba Data Online Migration, GCP Migration Center
AWS discovery) need a temporary, read-only IAM user created out-of-band behind an approval gate and
revoked in the `decommission` workflow.

## 2. Separation of plan and apply

- **Plan** runs for pull requests and the main branch. It must see everything to refresh, but it
  must not change anything. It reads state of every environment of the project (needed to plan
  each one) and can only create/delete lock files.
- **Apply** runs only in a GitHub environment with required reviewers. Each environment has its
  own identity whose state access is limited to `<prefix>/<project>/<env>/`. A staging
  compromise cannot write production state.
- Apply identities are trusted only for `repo:<owner>/<repo>:environment:<env>`, so they can only
  be assumed by a job that passed that environment's protection rules.

## 3. The deploy permission set is broad: say so

An identity that deploys a VPC, a database, container services, IAM roles and KMS keys can do
serious damage. The modules make the set explicit (no `AdministratorAccess`, no `roles/owner`)
and add guardrails, but **treat every apply identity as highly privileged**: limit who can
approve its environment, alert on its use outside CI, and review its policy on every change.

## 4. Per provider

### AWS (`infra/bootstrap/aws/ci-identity`)

- OIDC provider `token.actions.githubusercontent.com`, audience `sts.amazonaws.com`, no wildcard
  subjects (`StringEquals` only; validated inputs reject `*` and `?`).
- Roles under the path `/hermes-ci/`, `max_session_duration = 3600`.
- **Plan**: `ReadOnlyAccess` (configurable) plus explicit denies: object data outside state,
  `secretsmanager:GetSecretValue`, SSM parameter values (except public `/aws/service/*`), queue
  and stream data, log events, DB log downloads, EC2 password/console data, DynamoDB items.
  State: `s3:GetObject` on `<prefix>/<project>/*`, `s3:PutObject`/`DeleteObject` on `*.tflock`.
- **Apply** (per environment): control-plane services on `*` (EC2 networking, ECS, ELB,
  Application Auto Scaling, CloudWatch, RDS); name-scoped data services (`s3:*`, `sns:*`, `sqs:*`
  on `<project>-*`; Secrets Manager container actions only; KMS keys only when tagged
  `hermes-project = <project>`); IAM limited to roles named `<project>-*`, attachable managed
  policies limited to `AmazonECSTaskExecutionRolePolicy` and `AmazonRDSEnhancedMonitoringRole`,
  `iam:PassRole` only to `ecs-tasks`, `monitoring.rds`, `vpc-flow-logs`; service-linked roles only
  for ECS, ELB, RDS and ECS auto scaling. Explicit denies: data-plane reads/writes as for plan,
  any change to `/hermes-ci/` roles or the OIDC provider, IAM users, access keys, login profiles,
  identity providers, Organizations and account settings.
- Optional `workload_permissions_boundary_arn`: role creation and policy changes then require
  the boundary, and removing it is denied. Recommended for production accounts.
- IAM names are global per account: deploying the same project and environment to two regions
  of one account collides, and the apply scope `role/<project>-*` spans regions. Use one account
  per environment (recommended) or distinct project names per region.

### GCP (`infra/bootstrap/gcp/ci-identity`)

- Workload identity pool and OIDC provider; `attribute_condition` pins
  `assertion.repository` and, when `github_repository_id` is set, `assertion.repository_id`
  (survives renames, defeats a re-created repository with the same name).
- Impersonation (`roles/iam.workloadIdentityUser`) is bound to exact subjects with
  `principal://iam.googleapis.com/<pool>/subject/<sub>`, never to a whole pool.
- **Plan**: `roles/viewer` (the composition's requirement) plus state-bucket access through IAM
  conditions: read under `<prefix>/<project>/`, create/delete only `*.tflock`. Note: buckets that
  keep the default `projectViewer` convenience bindings are readable by viewers; override
  `plan_roles` with service-specific viewer roles if that matters.
- **Apply** (per environment): Service Usage Admin, Compute Network Admin and Security Admin,
  Service Networking Admin, Cloud SQL Admin, Cloud Run Admin, Service Account Admin and Service
  Account User, Storage Admin, Pub/Sub Admin, Secret Manager Admin, Cloud KMS Admin, Logging Config
  Writer, Monitoring AlertPolicy and NotificationChannel Editor, and Project IAM Admin
  **conditioned** with `api.getAttribute('iam.googleapis.com/modifiedGrantsByRole', []).hasOnly([...])`
  so it can only grant `cloudsql.client`, `cloudsql.instanceUser`, `logging.logWriter`,
  `monitoring.metricWriter` and `cloudtrace.agent`. The serviceusage and cloudresourcemanager APIs
  must already be enabled.
- Broad parts: Service Account User and Secret Manager Admin are project-wide (Secret Manager
  Admin can read secret values). Mitigate with a **separate CI project** (`ci_project_id`) for the
  pool and CI service accounts, and **one GCP project per environment**.

### Alibaba Cloud (`infra/bootstrap/alibaba/ci-identity`)

- RAM OIDC provider for `https://token.actions.githubusercontent.com`, client id
  `sts.aliyuncs.com`, fingerprints supplied and verified by the operator.
- Trust policies pin `oidc:iss`, `oidc:aud` and exact `oidc:sub` values; roles named
  `hermes-ci-<project>-plan` / `-apply-<env>`, one-hour sessions.
- **Plan**: an explicit list of `Describe`/`List`/metadata `Get` actions (no `ReadOnlyAccess`,
  which includes object reads), state object read, Tablestore lock-row access.
- **Apply**: control-plane services on `*` (VPC, security groups, SAE, SLB, RDS, CloudMonitor,
  KMS key and secret-container management), `oss:*`, `mns:*`, `log:*` on `<project>-*`, RAM roles
  and policies on `<project>-*`, `ram:PassRole` on `<project>-*`. Explicit denies: object
  contents, secret values, decrypt, messages, log contents, any RAM/IMS change to the CI roles,
  OIDC providers and RAM users.
- RAM action names were taken from public documentation and are **not verified** against a real
  account.

## 5. Workload identities (from the compositions)

| Provider | Mechanism | Scope | Notes |
|---|---|---|---|
| AWS | task role per service + execution role | bucket ARNs (read vs read-write), topic and queue ARNs, secret ARNs, KMS via service | trust conditioned on `aws:SourceAccount` and the ECS ARN |
| GCP | service account per Cloud Run service | bucket-, topic-, subscription- and secret-level IAM; project-level only for Cloud SQL client and telemetry writers | IAM database authentication |
| Alibaba | RAM role per SAE application | OSS bucket/object, MNS topic/queue messages, KMS secret and key ARNs | **trust is the whole account** until the SAE RRSA binding is implemented (threat model F1) |

## 6. Discovery and break-glass

- Discovery: a read-only role in the source account, trusted only for the operator's account with
  an `sts:ExternalId` condition. The IAM policy is the outer layer; the CLI guard is the inner
  layer. Revoke the role after the migration.
- Break-glass: keep it outside Hermes and CI. Alert on every use (CloudTrail/Cloud Audit
  Logs/ActionTrail), rotate after use, and record the reason.
