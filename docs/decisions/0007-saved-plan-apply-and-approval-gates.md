# ADR 0007 — Saved-plan-only apply with destructive-change and protected-environment gates

- Status: Accepted (2026-11-18)

## Context

The most damaging infrastructure incidents are silent: an apply that replaces a database because an immutable
argument changed, an apply against the wrong account, or a destroy run from a stale terminal. The brief requires
"never allow the CLI to perform arbitrary destructive operations silently" and "require deliberate approval for
production apply, cutover, and destruction".

## Decision

- `hermes plan` saves the plan file, its JSON rendering, and metadata: `plan_id` (the SHA-256 of the plan file),
  the render digest, the engine and version, the author, the policy result, and a classified summary from
  `internal/tfplan`.
- `hermes apply` applies **only a saved plan**. It never re-plans, and it never passes `-auto-approve` (the
  engine runner refuses that flag). It refuses when:
  - the rendered configuration changed since the plan (stale), or the plan is older than its maximum age;
  - the plan deletes or replaces anything and `--allow-destructive` was not given;
  - the environment is protected and `--approve <plan-id-prefix>` was not given. The prefix proves the approver
    saw *this* plan;
  - policy checks failed, or the policy engine is unavailable in a protected environment without an explicit,
    recorded `--skip-policy`.
- `hermes destroy` always requires `--approve`. Protected environments additionally require
  `--confirm-environment <name>`.
- The AWS provider is rendered with `allowed_account_ids` when `cloud.account` is set, so an apply against the
  wrong account fails inside the engine.
- Every apply, destroy and refusal is appended to `.hermes/history/<env>.jsonl`.

## Consequences

- CI pipelines run `hermes plan` in one job and `hermes apply --approve $PLAN_ID` in a job protected by a
  GitHub environment that needs human approval (`docs/security/ci-cd-authentication.md`).
- Local history is an audit aid, not a tamper-proof log. Organisations should rely on cloud audit logs
  (CloudTrail, Cloud Audit Logs, ActionTrail) and on CI logs for compliance.
