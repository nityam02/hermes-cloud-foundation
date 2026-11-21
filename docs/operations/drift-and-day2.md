# Drift and day-2 operations

Running an environment after the first apply: detecting drift, changing the spec safely, upgrading Hermes, the
engine and the provider pins, and recovering state. **Implemented, not tested against a real cloud**
([limitations](../limitations.md)).

## Status and drift

```bash
hermes status --env production            # local metadata: last plan/apply, bindings, resource counts
hermes status --env production --drift    # plan -refresh-only -detailed-exitcode; exit 3 when drift is found
```

- `status` reads `.hermes/plans/<env>/meta.json` and `.hermes/history/<env>.jsonl`. When the root module is
  initialized, it also reads the state through the engine (`output -json`, `show -json`).
- `--drift` runs `plan -refresh-only -detailed-exitcode` in `.hermes/build/<env>/`. It reads the cloud with
  **your** credentials and **never changes anything**. The engine's exit code 2 (changes) is reported as
  `drift: true` with exit 3, which is handy in a scheduled CI job.
- `--drift` needs an initialized root. On a fresh checkout, run `hermes plan` first, or it refuses with
  "`--drift needs an initialized root module; run hermes plan first`".

**When drift is found:**

1. Find out who changed what (cloud audit logs: CloudTrail, Cloud Audit Logs, ActionTrail).
2. Decide which side is right.
   - If the change should stay, encode it in the spec (or the module) and re-plan.
   - If it should go, run `hermes plan`. The plan shows the changes that revert reality to the spec. Review them,
     then apply. Deletes or replaces need `--allow-destructive`.
3. Never "fix" drift by editing state by hand.

## Changing an environment

Every change follows the same loop: edit `hermes.yaml` → `hermes validate --env <env>` → `hermes plan --env <env>`
→ review (summary, `!` destructive lines, policy) → `hermes apply --env <env> [--approve …]`.

Changes that replace resources (they show as `replace` and need `--allow-destructive`):

| Change | Effect |
|---|---|
| `network.cidr` | the network and everything in it |
| `project.name`, environment name | native names, and the state key (migrate the state first; see below) |
| `database.engine`/major `version` | the database instance, or a provider-managed upgrade. Read the plan |
| Bucket, topic or subscription names | new native resources; data is **not** moved |

Changes that are normally in-place: service image, CPU and memory, instance counts, environment variables,
`log_retention_days`, alert e-mail, `access` grants. The plan is authoritative, so always read it.

## Upgrading Hermes

A new `hermes` binary embeds new modules, so its modules digest changes:

1. Read `CHANGELOG.md` and `hermes version` (modules digest and provider pins).
2. `hermes render --env <env>`. If `.hermes/modules/<version>/` already holds a different module set, the CLI
   refuses (`modules directory does not match the embedded modules`). Re-run with `--refresh-modules`.
3. `hermes plan`. Saved plans from the old binary are stale (`HCF502`) and cannot be applied. Review the new plan
   carefully: module changes can change resources even when your spec did not change.
4. Upgrade non-production first.

## Upgrading the engine

- Supported floor: OpenTofu ≥ 1.10, Terraform ≥ 1.11. `hermes doctor` warns below OpenTofu 1.12 (the oldest line
  still receiving security fixes).
- The engine version is recorded in each saved plan (`meta.json`). `apply` uses the engine that made the plan.
  Re-plan after an upgrade.
- Hermes does not test switching an existing state between OpenTofu and Terraform. Treat a switch as a
  migration: back up the state, re-plan, and expect no changes. Client-side state encryption exists only in
  OpenTofu, so an encrypted environment cannot move to Terraform.

## Bumping provider pins

Rendered roots pin exact versions (`internal/providers/versions.go`). Modules use `~>` constraints. A pin bump is a
Hermes code change, not a spec change:

1. Change the pin in `versions.go` (and the module constraints if the major or minor version moves).
2. `make tf-validate tf-test contract-test tf-compat` with the new provider. Mock tests catch schema changes,
   not behaviour changes.
3. Regenerate the golden roots (`go test ./internal/render -update`), review, and update the provider guide's
   "tested with" line.
4. Release, then upgrade environments as in "Upgrading Hermes" above.

## State recovery and moves

- Where state lives, and how it is locked and encrypted: `hermes state show --env <env>`.
- Moving buckets or prefixes, renames, and stuck locks: [state management](state-management.md#moving-state).
- Restoring a previous state version from the versioned bucket:
  [backup and restore](../disaster-recovery/backup-and-restore.md#3-state-file-rollback), then `hermes plan` and
  review before any apply.
- Protecting state: [state security](../security/state-security.md).

## Routine checks

| Check | How | Frequency (suggested) |
|---|---|---|
| Drift | `hermes status --drift` in CI (exit 3 alerts) | daily |
| Toolchain | `hermes doctor` | on every CI runner image update |
| Policies | `make policy-test` after policy changes | per change |
| Restore drill | PITR restore into a scratch instance ([backup and restore](../disaster-recovery/backup-and-restore.md)) | quarterly |
| Alerts reach a human | trigger a test alarm ([monitoring and alerting](monitoring-and-alerting.md)) | quarterly |
| History review | `.hermes/history/<env>.jsonl` (`--skip-policy`, refusals) | per release |
