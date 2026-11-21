# `hermes` CLI reference

Hermes adds a cloud-independent spec, provider selection, validation, safety gates and
portability measurement on top of OpenTofu (default) or Terraform. The engine does all
planning, applying and state work.

Status legend: **verified** = implemented and covered by Go tests with a fake engine (no
real cloud); **not tested against a real cloud** applies to everything that changes
infrastructure, because no command has been run against a real account in this repository.

## Global flags

| Flag | Default | Meaning |
|---|---|---|
| `-c, --config` | `hermes.yaml` | spec file (relative to `--workdir` when set) |
| `-e, --env` | `project.environment` | environment to operate on |
| `-o, --output` | `text` | `text` or `json` (`bindings` also accepts `env`) |
| `--workdir` | current directory | where `.hermes/` lives |
| `-v, --verbose` | off | also print info diagnostics |

`--env` must name a declared environment (`project.environment` or a key of `environments`).
A typo is an error, never a silently unprotected environment. Every name in an explicit
`project.protected_environments` must be declared too (`HCF121`), and a production-like name
that is not protected warns (`HCF122`).

## Exit codes

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | unexpected error (engine failure, I/O) |
| 2 | validation failed or a safety gate refused (`Refused:` on stderr with an `HCF5xx` code) |
| 3 | changes present: `plan --detailed-exitcode`, or drift found by `status --drift` |

## Approvals

Every approval flag follows the same rule: `hermes apply --approve`, `hermes destroy --approve`,
`hermes migrate cutover --approve` (and `--complete-step` of an approval-gated step),
`hermes migrate cutover --approve-hooks`, and `hermes adopt apply --approve` take **at least 8
hexadecimal characters** of the id being approved (the plan id, or the hooks file sha256),
compared **case-insensitively** after trimming surrounding spaces. A shorter, non-hexadecimal or
non-matching value is refused (exit 2). Copy the prefix from the output of the command that
produced the id (`hermes plan`, the cutover dry-run, `hermes adopt plan`/`verify`).
*Implemented & verified (`internal/approval` plus a test per command).*

## Working directory (`.hermes/`, gitignored)

```
.hermes/
  build/<env>/main.tf.json          rendered root module (JSON syntax, deterministic)
  build/_bootstrap-<provider>/      state-backend bootstrap root
  modules/<cli-version>/            modules extracted from the binary (+ .hermes-modules.json manifest)
  policies/<cli-version>/           plan policies extracted from the binary
  plans/<env>/                      plan.tfplan, plan.json, meta.json, policy-data.json
  plans/<env>/destroy/              saved destroy plan
  state/                            local state (backend local, and the bootstrap root)
  history/<env>.jsonl               append-only record of plans, applies, destroys and refusals
```

## Commands

### `hermes init`
`--name <project> [--provider aws|gcp|alibaba] [--region] [--account] [--template basic|containerized|database|autonomous-logistics] [--force]`.
Writes `hermes.yaml` (never overwrites without `--force`) and adds `.hermes/` to
`.gitignore`. The template's `dev` environment always validates; findings for `production`
(certificates, account) are printed as things to fill in. Values marked `REPLACE-ME` are
placeholders. *Verified.*

### `hermes validate [--portability]`
Schema, semantic and provider rules for every declared environment (or only `--env`).
Diagnostics carry a code, the spec path and `file:line`. *Verified.*

### `hermes portability`
Lists the features the spec relies on and scores each provider:
`score = 100 × Σ weight / count` with implemented = 1, partial = 0.5, not-implemented = 0,
plus provider-specific sizing findings and the features whose status differs. Statuses
describe the Hermes modules (mock-tested), not real-cloud behaviour. *Verified.*

### `hermes render [--print] [--refresh-modules]`
Writes `.hermes/build/<env>/main.tf.json`: `terraform { required_version, required_providers
(exact pins), backend, encryption (OpenTofu only) }`, the provider block(s), `module "platform"`
with every contract variable, and the outputs `bindings`, `service_environment`,
`secret_environment`. `--print` prints the normalized spec instead. Embedded modules are
extracted to `.hermes/modules/<version>/`; a directory with different content is never
overwritten unless `--refresh-modules`. *Verified (golden files).*

### `hermes plan [--detailed-exitcode] [--policy-dir DIR]... [--policy-data FILE]... [--skip-policy] [--refresh-modules]`
Plan-time checks (account, state bucket, GCP project, Tablestore locking in protected
Alibaba environments) → render → `init` → `plan -out` → `show -json` → classification →
`conftest test` → save `plan.tfplan`, `plan.json`, `meta.json` (plan id = sha256 of the plan
file, render and modules digests, engine and version, user, summary, policy result).
Missing conftest: warning, or an error in protected environments unless `--skip-policy`
(recorded). A policy failure saves the plan for inspection, exits 2 and blocks apply.
*Verified with a fake engine; not tested against a real cloud.*

Policy flags (also on `hermes destroy`):
- `--policy-dir DIR` (repeatable) **adds** a policy directory. The embedded built-in policies
  always run in their **own** conftest invocation; custom directories run in a second
  invocation (together with a copy of the embedded bundle, so they can use its helpers). The
  findings of both are combined, so a custom policy can add denies and warnings but can never
  remove or neutralise a built-in one (for example by redefining the `excepted` helper).
  Custom policies use `package main` (the conftest default namespace). Symbolic links inside a
  policy directory are refused.
- `--policy-data FILE` (repeatable; `.json`, `.yaml` or `.yml`) passes an extra data file to
  conftest with `--data`, after the generated `policy-data.json`. This is how the time-boxed,
  per-rule, per-address exceptions (`hermes.exceptions`) and `hermes.secret_placeholders`
  described in [policies/README.md](../../policies/README.md) ("Data") are supplied. A data
  file that sets a key Hermes derives from the spec (`hermes.project`, `environment`,
  `provider`, `protected`, `protected_environments`) is refused, so it cannot mark a protected
  plan as unprotected. Every exception must name one rule and one exact resource address
  (`*` and other wildcards are refused), give a reason, and expire within 90 days
  (`YYYY-MM-DD`); otherwise the plan fails before conftest runs. Expired exceptions are ignored
  with a warning (`HCF306`). Every exception in effect is printed with the plan
  (`policy exception: <rule> <address> (reason; expires; whether the address is in this plan;
  file)`) and recorded in `meta.json` (`policy.exceptions`) and in history
  (`policy_exceptions`).
- `meta.json` (`policy.bundles`, `policy.data_files`) and the history event (`policies`) record
  the sha256 of every bundle (over every file in the directory: relative path, size and
  content, hidden files included) and of every data file, so a reviewer can tell exactly which
  policies and exceptions a plan was checked with. *Verified with a fake conftest runner (unit
  and CLI tests) and, for the bundle separation, with the real conftest
  (`TestRealConftestCustomBundleCannotNeutralise`, skipped when conftest is not installed).*

### `hermes apply [--approve PREFIX] [--allow-destructive] [--yes] [--max-plan-age 24h]`
Applies only the saved plan. Gates, in order: right kind and environment, not already
applied, spec/modules unchanged since planning (stale), younger than `--max-plan-age`, plan
file matches its id (tamper), policies passed, no delete/replace without
`--allow-destructive`, and approval: protected environments need `--approve` with ≥ 8
characters of the plan id (`--yes` is ignored there); elsewhere a terminal prompt (y/N),
`--yes`, or `--approve`. Non-interactive runs without either are refused. Hermes never
passes `-auto-approve` (the engine wrapper refuses it). *Verified with a fake engine.*

### `hermes destroy [--approve PREFIX] [--confirm-environment ENV]`
Without `--approve`: saves a destroy plan (`plan -destroy`) and prints its id; nothing is
destroyed. With `--approve`: applies that saved destroy plan after the same gates.
Protected environments also need `--confirm-environment <env>`. *Verified with a fake engine.*

### `hermes status [--drift]`
Environment, provider, state location, last plan and last successful apply (from history);
when `.hermes/build/<env>` is initialized: `bindings` from `output -json` and the number of
managed resources from `show -json`. `--drift` runs `plan -refresh-only -detailed-exitcode`
(reads the cloud, changes nothing) and exits 3 on drift. *Verified with a fake engine.*

### `hermes bindings [-o json|env] [--service NAME]`
Prints `bindings`, `service_environment` and `secret_environment` (references only) from the
state. `-o env --service api` prints shell-quoted `KEY=value` lines. The JSON form is the
input of `hermes migrate validate` checks of type `bindings`. *Verified with a fake engine.*

### `hermes state show` / `hermes state bootstrap [--apply --approve PREFIX]`
`show`: backend type, location, locking and encryption. `bootstrap`: renders
`infra/bootstrap/<provider>/state-backend` with **local** state and saves a plan; `--apply`
applies it through the same gates and always requires `--approve`. See
[state management](../operations/state-management.md). *Verified with a fake engine.*

### `hermes doctor`
Engine and version (warns below OpenTofu 1.12), optional tools (presence only), credential
sources (environment variable **names** and whether well-known files exist; never values,
never API calls), spec validity, `.hermes/` in `.gitignore`, free disk. Exit 2 when the
engine is missing or the spec is invalid. *Verified.*

### `hermes providers [name]`, `hermes version`
Capability matrix, extension keys, known regions, engine provider pins and the embedded
modules digest. *Verified.*

### `hermes cost`
Runs `infracost breakdown --path .hermes/plans/<env>/plan.json` when Infracost is installed;
otherwise explains what is missing. Alibaba Cloud is not priced by Infracost. Hermes never
invents numbers. *Verified (absence paths); the Infracost call itself is not tested.*

Migration commands (`hermes inventory`, `hermes migrate …`, `hermes adopt …`) are documented
in [docs/migration/migration-commands.md](../migration/migration-commands.md).
