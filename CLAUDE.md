# CLAUDE.md — engineering brief for Hermes Cloud Foundation

Read this before changing anything. It is the architecture summary, the conventions, and the
safety rules for humans and coding agents working in this repository.

## What Hermes is

A thin, safety-focused platform layer that lets a team describe infrastructure **once** in a
cloud-independent spec (`hermes.yaml`, `kind: Platform`) and deploy it to AWS, GCP or Alibaba
Cloud, plus a Terraform-independent migration toolkit (discover → assess → plan → validate →
gated cutover). It is **not** an IaC engine: OpenTofu (default) or Terraform does all planning,
applying, state and import work.

```
hermes.yaml ──► config (load, overlay, defaults) ──► schema + semantic validation
             ──► provider adapter (aws|gcp|alibaba) ──► renderer: .hermes/build/<env>/main.tf.json
             ──► execution (tofu|terraform init/plan/show -json) ──► safety gates ──► apply saved plan
inventory (read-only AWS APIs) ──► classify (catalog) ──► assess ──► plan ──► validate ──► gated cutover
```

Layers:

| Layer | Where |
|---|---|
| A. Developer interface (CLI) | `cmd/hermes`, `internal/cli` |
| B. Cloud-independent contracts | `schemas/`, `docs/architecture/data-contracts.md`, `infra/contract/` |
| C. Provider adapters | Go: `internal/providers`; HCL: `infra/providers/<p>/platform`, `infra/modules/<p>/*` |
| D. Execution engine | `internal/execution` (wraps tofu/terraform), `internal/state`, `internal/policy` |
| E. Application portability | env-var binding contract, OTLP, CloudEvents, PostgreSQL — see `docs/architecture/application-portability.md` |
| Migration | `internal/inventory`, `internal/migration`, `migration/` data, `docs/migration/` |

## Non-negotiable rules

1. **No real cloud calls during development or tests.** This machine may hold real cloud
   credentials. Never run `aws`, `gcloud`, `aliyun`, or `tofu/terraform plan|apply|import`
   against real providers. Use `tofu validate`, `tofu test` with `mock_provider`, Go fakes,
   and the moto server for the AWS discovery demo. When running anything that might pick up
   credentials, isolate it: `AWS_CONFIG_FILE=/dev/null AWS_SHARED_CREDENTIALS_FILE=/dev/null
   AWS_EC2_METADATA_DISABLED=true AWS_PROFILE= GOOGLE_APPLICATION_CREDENTIALS=/dev/null`.
2. **No secrets anywhere**: not in code, fixtures, state, outputs, logs, or CI artifacts.
   Secret *values* are never read by discovery and never managed by IaC.
3. **Discovery is read-only.** Collectors may only call `Describe*`, `List*`, `Get*` (non-secret),
   `Search`, `Select*`, `Lookup*` operations. A unit test enforces the allowlist.
4. **Never apply silently.** `apply` only applies a saved plan produced by `hermes plan`;
   destructive actions (delete/replace) need `--allow-destructive`; protected environments need
   `--approve <plan-id-prefix>`. `destroy` always needs approval. Cutover and source
   decommissioning are never automatic.
5. **Honesty.** Never claim something works that has not been tested. Label features as
   *implemented & verified*, *implemented, not tested against real cloud*, *designed, not
   implemented*, or *not supported*. `docs/limitations.md` is the register.
6. **Generic.** No customer, employer, or company-specific names, data, or branding.
7. **Don't build a wrapper for its own sake.** Every CLI feature must add something the engine
   does not: cloud-independent spec, provider selection, validation, safety gates, portability
   measurement, or migration workflow.

## Conventions

- Go ≥ 1.26, module `github.com/nityam02/hermes-cloud-foundation`. CLI framework: cobra.
  Standard library first; dependencies must be justified. `gofmt`, `go vet`, `golangci-lint`.
- Errors: wrap with context (`fmt.Errorf("render: %w", err)`); user-facing diagnostics carry a
  code (`HCF1xx` config, `HCF2xx` provider, `HCF3xx` execution, `HCF4xx` migration, `HCF5xx` safety).
- Every command supports `--output text|json` where it produces data. Exit codes: 0 ok,
  1 error, 2 validation failed / gate refused, 3 changes present (`plan --detailed-exitcode`).
- HCL: modules compatible with **both** OpenTofu ≥ 1.10 and Terraform ≥ 1.11 (tested on OpenTofu
  1.13 and Terraform 1.15). No engine-specific
  features inside modules (engine-specific features such as OpenTofu state encryption are only
  emitted by the renderer when that engine is selected). Exact provider pins in rendered roots,
  `~>` constraints in modules. `tofu fmt`, `tflint`, `checkov`, `trivy config`.
- Module tests: `*.tftest.hcl` in each module's `tests/` dir using `mock_provider`; run with
  `tofu test`. Contract fixtures live in `tests/contracts/fixtures/`.
- Native names follow `<project>-<environment>-<name>` with hash-truncation (see data-contracts §2).
- Generated working files live in `.hermes/` (gitignored).

## Commands

```bash
make build          # go build ./cmd/hermes -> bin/hermes
make test           # go unit tests
make tf-validate    # tofu fmt -check + init -backend=false + validate for every module
make tf-test        # tofu test with mock providers (no credentials needed)
make contract-test  # provider contract tests
make policy-test    # conftest verify
make security       # checkov, trivy config, gitleaks, forbidden-terms check
make demo-discovery # moto-backed AWS discovery demo (no real AWS)
make ci             # everything CI runs
```
