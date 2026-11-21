# Developer onboarding

For engineers who will change Hermes. Read [CLAUDE.md](../../CLAUDE.md) (rules and conventions) and
[CONTRIBUTING.md](../../CONTRIBUTING.md) first. This page gives you the map, the commands and a first change.

## 1. The five rules that shape every change

1. **No real cloud calls in development or tests.** Use fakes, `mock_provider`, or the local moto server. Run
   engine commands with [credential isolation](installation.md#working-without-touching-real-clouds).
2. **No secrets** in code, fixtures, state, outputs or logs. Fake values only (`123456789012`, `example.com`).
3. **Discovery is read-only.** A unit test enforces the operation allowlist.
4. **Never apply silently.** Saved plan only, plus destructive and approval gates.
5. **Honest labels.** A provider feature change updates the Go matrix, the provider guide and
   [limitations.md](../limitations.md) together.

## 2. Repository tour

```text
cmd/hermes/main.go            entry point: cli.Execute(build info)
internal/
  cli/                        cobra commands; each command group registers itself (Register in init())
  config/                     load YAML → schema → overlay → defaults → semantic rules → *Platform
  schema/                     JSON Schema (draft 2020-12) validation → diagnostics with spec paths
  diag/                       the Diagnostic type and HCFxxx code ranges
  providers/                  one file per cloud: capability matrix, regions, extension keys, rules; pins in versions.go
  render/                     *Platform → .hermes/build/<env>/main.tf.json; extracts embedded modules
  state/                      backend configuration and OpenTofu state-encryption block
  execution/                  engine runner (tofu/terraform) and safety gates; enginetest/ is the fake engine
  policy/                     conftest runner over plan JSON
  tfplan/                     the subset of the JSON plan format used by gates and adoption
  diagnostics/                `hermes doctor`
  inventory/, inventory/aws/  Inventory model; read-only AWS collectors, guard, IAM policy
  graph/                      dependency graph, waves, blast radius
  migration/, migration/adopt catalog, assess, plan, validate, cutover; import-only adoption
  ui/                         tables, JSON, prompts
schemas/                      embedded JSON Schemas (platform, inventory, migration)
infra/contract/               variables.tf (copied verbatim into each composition) and outputs.md
infra/providers/<p>/platform  the composition for provider <p>
infra/modules/<p>/<module>    naming, network, kms, container-service, postgres, object-storage, messaging, secrets, observability
infra/bootstrap/<p>/          state-backend and ci-identity roots
infra/embed.go                embeds infra/ into the binary (render extracts it to .hermes/modules/<version>/)
policies/plan, policies/tests Rego policies and their fixtures
migration/                    catalog.yaml, workflows/, pricing/, runbooks/, discovery/ (embedded by migration/embed.go)
tests/contracts               static contract checks + the canonical contract runs (contract-runs.hcl)
tests/integration/            moto discovery test (tag moto), opt-in real-AWS discovery test (tag realaws)
examples/                     example specs (see examples/README.md); examples/migration/ for the toolkit
```

The [architecture overview](../architecture/overview.md) explains how these fit together, and the
[detailed design](../architecture/detailed-design.md) goes package by package.

## 3. Build and test

| Command | What it does | Needs |
|---|---|---|
| `go build -o bin/hermes ./cmd/hermes` (or `make build`) | Build the CLI | Go |
| `make test` | `go test ./...` with credential isolation | Go |
| `make lint` | gofmt check, `go vet`, golangci-lint | Go (+ golangci-lint) |
| `make contract-test` | static contract checks + `tofu test -filter=tests/contract.tftest.hcl` on each composition | OpenTofu, provider downloads |
| `make tf-validate` | `tofu fmt -check`, `init -backend=false`, `validate` for every module dir | OpenTofu, provider downloads |
| `make tf-test` | `tofu test` (mock providers) for every module that has tests | OpenTofu, provider downloads |
| `make tf-compat` | the three compositions with Terraform | Terraform |
| `make integration` | render every example, then `tofu init -backend=false` + `validate` the rendered roots | OpenTofu |
| `make policy-test` | `conftest verify` | conftest |
| `make security` | gitleaks, trivy, checkov, tflint, conftest (`scripts/security-scan.sh`) | the scanners |
| `make docs-links` | lychee offline link and anchor check | lychee |
| `make demo-discovery` | moto-backed discovery demo | Python venv with moto (~180 MB) |
| `make ci` | lint, test, contract-test, tf-validate, tf-test, policy-test, docs-links | all of the above |

> `make` needs GNU Make ≥ 4.3. On macOS, `/usr/bin/make` (3.81) fails to parse the `Makefile`. Use `gmake`,
> or run the underlying commands shown in the `Makefile` directly.

Set `TF_PLUGIN_CACHE_DIR=$HOME/.cache/hermes-tf-plugins` (the `Makefile` does) so that providers download
once. Each provider is hundreds of MB. What each test layer proves is in the [testing guide](../testing.md).

## 4. Your first change: add a region the AWS adapter does not know yet

A small change that touches the rule, the test and the docs.

1. **See the current behaviour:**

   ```bash
   hermes init --name trial --provider aws --region xx-example-1 --account 123456789012
   hermes validate --env dev
   # warning HCF200 at cloud.region: region "xx-example-1" is not in the list of AWS regions known to Hermes
   ```

2. **Change the rule:** append the region to `awsRegions` in `internal/providers/aws.go`. Regions live in the
   same file as the capability matrix on purpose: one file per provider.
3. **Test:** `go test ./internal/providers` (add a case to `providers_test.go` if the list has a test), then
   `make test`.
4. **Check the composition:** nothing in `infra/` depends on the list, so `make tf-test` is not needed here.
   A change under `infra/` always needs `make tf-validate tf-test contract-test` (and `make tf-compat` for
   compositions).
5. **Golden files:** if your change alters rendered output, regenerate the golden roots with
   `go test ./internal/render -update` and review the diff. The discovery IAM policy and catalog markdown
   goldens use `HERMES_UPDATE_GOLDEN=1`.
6. **Docs:** update the provider guide if it lists regions, and `CHANGELOG.md`.

## 5. Changes that need more care

| Change | Also update |
|---|---|
| A spec field | `schemas/platform/v1alpha1.schema.json`, `internal/config`, `infra/contract/variables.tf` **and** the verbatim copy in each composition, `docs/reference/configuration.md`, data-contracts §1 |
| A capability or provider feature | the Go matrix (`internal/providers/<p>.go`), `docs/providers/<p>.md`, [comparison matrix](../providers/comparison-matrix.md), [limitations](../limitations.md) |
| A safety gate | a test proving each refusal still triggers (`internal/execution/gates_test.go`), [diagnostics](../reference/diagnostics.md) |
| A discovery collector | the operation registry (`internal/inventory/aws/registry.go`). The read-only guard and the generated IAM policy follow from it, so regenerate the policy golden |
| A plan policy | a pass and a fail fixture in `policies/tests/fixtures/`, then `make policy-test` |
| A new provider | [implementing a provider](../providers/implementing-a-provider.md) |

## 6. Where to ask "is this verified?"

Search the [limitations register](../limitations.md). If a behaviour is not listed there as *implemented &
verified (offline)*, assume it has not been verified, and never claim real-cloud behaviour in a PR without a
recorded run.
