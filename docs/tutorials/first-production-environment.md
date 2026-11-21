# Your first production environment

This tutorial turns the [quickstart](quickstart.md) spec into something you could run against a real account:
remote state, a protected environment, approvals, and CI with OIDC instead of long-lived keys.

> **Honesty note.** This project has never applied a Hermes environment to a real cloud. The steps below follow
> the code and the bootstrap modules, which are tested with mock providers only. Do your first run in a
> **disposable test account**, and read the [limitations register](../limitations.md) for your provider first.

## 0. Decide the account layout

- One account (AWS), project (GCP) or account (Alibaba) per environment is the safest layout. At minimum,
  production should be separate from everything else.
- Set `cloud.account` for every environment. On AWS it becomes `allowed_account_ids`, so the provider refuses to
  run with credentials for another account. A protected environment cannot be planned without it (`HCF206`).
- Keep state in a bucket in the production account (or a dedicated state account) with restricted access. State
  is sensitive even without secrets ([state security](../security/state-security.md)).

## 1. Make `production` valid

The quickstart spec fails for `production` with `HCF207` (public service without HTTPS). Fix every finding
until `hermes validate --env production` passes:

```yaml
cloud:
  account: "123456789012"            # placeholder; per-environment overlays can change it
workloads:
  compute:
    services:
      - name: web
        image: ghcr.io/example/web@sha256:<digest>   # pinned; :latest is an error when protected (HCF114)
        public: true
  database:
    high_availability: true          # Multi-AZ / REGIONAL / HA Edition
    deletion_protection: true
  observability:
    enabled: true
    alert_email: oncall@example.com
security:
  customer_managed_keys: true        # optional; adds a KMS key per environment (cost, ops)
environments:
  dev: {}
  production:
    cloud: {account: "210987654321"}
extensions:
  aws:
    acm_certificate_arn: arn:aws:acm:eu-north-1:210987654321:certificate/<id>
```

What `protected` changes is listed per provider. On AWS, for example: one NAT gateway per zone, ALB and RDS
deletion protection, a final snapshot, longer recovery windows for secrets and keys, and 90 days of noncurrent
object versions ([AWS guide](../providers/aws.md#what-each-spec-field-becomes)). Protected environments also
refuse `state.backend: local` (`HCF112`), and on Alibaba they require Tablestore locking (`HCF212`).

## 2. Bootstrap the state backend (once per account and provider)

The state bucket cannot hold the state of its own creation, so Hermes bootstraps it from a separate root with
local state ([state management](../operations/state-management.md#bootstrapping-the-backend-the-chicken-and-egg-problem)):

```bash
hermes state bootstrap --env production                     # renders .hermes/build/_bootstrap-aws/, saves a plan
hermes state bootstrap --env production --apply --approve <first 8+ chars of the plan id>
```

- Applying **always** needs `--approve`, whatever the environment, because the bucket protects every
  environment.
- The bucket is versioned, encrypted, private and TLS-only, with `prevent_destroy`.
- Its state is in `.hermes/state/_bootstrap/<project>/<provider>/terraform.tfstate`. Keep it safe, or migrate it
  into the new bucket.
- On Alibaba, copy the printed `tablestore_endpoint` and `tablestore_table` into
  `extensions.alibaba.state_tablestore_endpoint` and `_table`.

Then set `state.bucket` (and optionally `state.prefix`, `state.lock_table` for DynamoDB, and
`state.encryption.client_side: true` with `kms_key` for OpenTofu client-side encryption on AWS or GCP).

## 3. Plan, review, approve, apply

```bash
hermes plan  --env production
# apply plan for production (aws, protected)
#   <n> to create, ...
#   policy: passed (...)
#   plan id: 3f2a9c1b...
# Next: hermes apply --env production --approve 3f2a9c1b7d04
hermes apply --env production --approve 3f2a9c1b
```

The rules in a protected environment:

| Rule | Why |
|---|---|
| `--approve <prefix>` of at least 8 characters of the plan id; `--yes` is ignored | Approval names one exact plan, so a re-plan invalidates it |
| conftest must run (`HCF512` otherwise), unless `--skip-policy`, which is recorded in history | Policy is evidence for the reviewer ([ADR 0011](../decisions/0011-policy-as-code-on-plan-json.md)) |
| deletes or replaces need `--allow-destructive` (`HCF505`) | Stops accidental data loss |
| the plan expires after `--max-plan-age` (24 h) and becomes stale if the spec or modules change | The plan must still describe reality |
| `destroy` needs `--approve` and `--confirm-environment production` | Two deliberate acts |

Review `plan.json` (or the engine's text plan) before approving. Hermes summarizes, but the reviewer owns the
decision. History is in `.hermes/history/production.jsonl`.

## 4. Post-provision steps

Each provider has manual steps that IaC cannot or should not do: set secret values, create database users and
GRANTs, run `CREATE EXTENSION postgis`, and on Alibaba bind RAM roles and authorize KMS. They are listed in the
provider guides ([AWS](../providers/aws.md#post-provision-steps), [GCP](../providers/gcp.md#post-provision-steps),
[Alibaba](../providers/alibaba.md#post-provision-steps-manual-documented-not-automated)). Secret values never
pass through Hermes ([ADR 0012](../decisions/0012-secret-values-outside-iac.md)).

## 5. CI with OIDC (no long-lived keys)

1. Apply `infra/bootstrap/<provider>/ci-identity` for your repository. It creates a **plan** identity (read-only
   plus state access) and an **apply** identity per environment, trusted only for your repository through the
   provider's OIDC federation (IAM OIDC provider, Workload Identity Federation, RAM OIDC provider). The apply
   permission set is inherently broad. See [IAM strategy](../security/iam-strategy.md#3-the-deploy-permission-set-is-broad-say-so).
2. Create one GitHub environment per Hermes environment, with required reviewers, "prevent self-review" and
   deployment branches limited to `main`.
3. Set the repository and environment variables listed at the top of
   [`.github/workflows/integration.yml`](../../.github/workflows/integration.yml).
4. Run the `integration` workflow (manual dispatch only, `main` only, never for forks). It plans by default. An
   apply re-plans inside the approved job and refuses if the change set differs from the one reviewers saw.
   Plan files are never uploaded as artifacts.

Details and limits (for example, reviewers approve a job, not a plan file):
[CI/CD authentication](../security/ci-cd-authentication.md). The workflows are SHA-pinned but have **never run on
GitHub** in this project.

## 6. Day 2

- Drift: `hermes status --env production --drift` (exit 3 when drift is found; read-only).
- Upgrades and provider pin bumps: [drift and day-2 operations](../operations/drift-and-day2.md).
- Monitoring: [monitoring and alerting](../operations/monitoring-and-alerting.md).
- Recovery: [DR strategy](../disaster-recovery/strategy.md), [backup and restore](../disaster-recovery/backup-and-restore.md).
- Cost: [cost and sizing](../operations/cost-and-sizing.md).

## Checklist

- [ ] `hermes validate --env production` passes, with no warnings left unexplained
- [ ] `cloud.account` set per environment; separate production account
- [ ] state bucket bootstrapped, `state.bucket` set, bootstrap state kept safe
- [ ] images pinned by digest; HTTPS certificate set for public services
- [ ] `database.deletion_protection: true`, HA decided, backups and PITR understood for your provider
- [ ] conftest installed; policies pass
- [ ] CI identities applied; GitHub environments with required reviewers
- [ ] post-provision steps done and recorded
- [ ] first apply done in a disposable test account, with the results recorded
