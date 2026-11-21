# Implementing a provider

A step-by-step guide to adding a cloud (called `<p>` below) to Hermes. The three existing providers are the
reference implementations: AWS is the most complete, GCP the most regular, and Alibaba shows how to document a
provider with real gaps honestly.

A provider has two halves that must agree ([ADR 0003](../decisions/0003-capability-contracts-and-provider-compositions.md)):
an **HCL composition** that implements the capability contract, and a **Go adapter** that validates specs for it
and states truthfully what the composition implements. Read the
[data contracts](../architecture/data-contracts.md) (§1 spec, §2 module contract) and the
[detailed design](../architecture/detailed-design.md#4-provider-adapter-and-composition-model) first.

## 0. Decide the service mapping

For each capability, pick the managed service closest to the portable semantics, and write down why. Use the
"Service choices" tables in [aws.md](aws.md), [gcp.md](gcp.md) and [alibaba.md](alibaba.md) as the template.
Check the ecosystem before you start ([research §7](../research/open-source-landscape.md#7-lint-security-policy-and-cost-tooling-the-claudemd-toolchain)):

- Is there a TFLint ruleset, Trivy checks, Checkov coverage and Infracost pricing for it?
- Does OpenTofu have a KMS key provider for client-side state encryption?
- Does an engine backend with locking exist for its object store?

Missing pieces are acceptable, as long as they are documented.

## 1. The composition: `infra/providers/<p>/platform`

1. **`variables.tf`**: copy `infra/contract/variables.tf` **verbatim**. `tests/contracts` fails if a single byte
   differs. Provider-specific inputs go through `var.extensions` only.
2. **`versions.tf`**: `required_providers` with `~>` constraints (for example `~> 2.2`). Exact pins belong to the
   rendered root, not the module. No `provider` block: the root configures the provider.
3. **Modules**: `infra/modules/<p>/{naming,network,kms,container-service,postgres,object-storage,messaging,secrets,observability}`.
   Use official provider resources only (no registry modules), and keep them compatible with OpenTofu ≥ 1.10 and
   Terraform ≥ 1.11, with no engine-specific features.
4. **Naming**: `<project>-<environment>-<name>`, truncated to the service limit with `-<sha1[0:6]>`. Globally
   unique names get a stable `random_id` suffix (data-contracts §2).
5. **Tags/labels** on every taggable resource: `hermes-project`, `hermes-environment`, `hermes-managed-by=hermes`,
   `hermes-component`, plus `var.labels`.
6. **Guardrails as preconditions**: HTTPS required for public services when `protected`, unknown
   `extensions` keys rejected (recommended), invalid size combinations rejected.
7. **Protected behaviour** (`var.protected`): deletion protection, final backups where available, longer recovery
   windows, and HA network egress. Document exactly what changes.
8. **Outputs**: exactly `bindings`, `service_environment` and `secret_environment`, with the shapes in
   [outputs.md](../../infra/contract/outputs.md). The `HERMES_*` variable **names** must match the binding contract.
   Secret outputs hold references only, never values, and nothing is marked sensitive (`TestNoSensitiveOutputs`).
9. **State bootstrap**: `infra/bootstrap/<p>/state-backend` (versioned, encrypted, private, TLS-only bucket with
   `prevent_destroy`; a lock table if the backend needs one). Outputs: `bucket` and `backend_config`.
10. **CI identity** (owned with the security workstream): `infra/bootstrap/<p>/ci-identity`, with OIDC federation,
    separate plan and apply identities, and trust pinned to the repository.

## 2. Tests (mock providers only)

| File | Content |
|---|---|
| `infra/providers/<p>/platform/tests/contract.tftest.hcl` | A **provider header** (`mock_provider "<p>"` blocks with realistic defaults where the provider validates IDs or ARNs, plus a file-level `variables { region = "…", extensions = {…} }`), then the canonical section copied **verbatim** from `tests/contracts/contract-runs.hcl` (from the `BEGIN HERMES CONTRACT RUNS` marker to the end) |
| `infra/providers/<p>/platform/tests/<p>_*.tftest.hcl` | Provider wiring: least privilege (a service without access gets nothing), CMK grants, HA, deletion protection, ingress, dead-letter wiring, the binding environment, a protected variant and a lean variant; guardrail errors (`expect_failures`) |
| `infra/modules/<p>/*/tests/*.tftest.hcl` | Per-module behaviour (rounding, address plan, bucket security, and so on) |
| `infra/bootstrap/<p>/state-backend/tests` | Plan-only runs (`prevent_destroy`) |

Use `command = apply` with mocks so that computed values exist. Mocks prove wiring, not acceptance. Say so in
the provider guide.

Then add `<p>` to:

- `providers` in `tests/contracts/contract_test.go` (static checks: identical variables, identical contract runs,
  outputs, no sensitive outputs);
- `COMPOSITIONS` in the `Makefile` (contract runs and `tf-compat`);
- the provider enum in `contract-runs.hcl` (`contains(["aws", "gcp", "alibaba"], output.bindings.provider)`).
  This is a contract change, so update it in every composition's copy too.

## 3. The Go adapter: `internal/providers/<p>.go`

Implement `providers.Adapter` in **one file**, which holds the capability matrix, regions and rules, so it can be
reconciled with the HCL in one place:

```go
type Adapter interface {
    Name() string                                  // spec value of cloud.provider
    DisplayName() string
    Capabilities() CapabilityMatrix                // all 21 features: Implemented | Partial | NotImplemented + note
    Regions() []string                             // snapshot; unknown regions only warn (HCF200)
    ExtensionKeys() []ExtensionKey                 // name, description, type (string|bool|list), pattern
    Validate(p *config.Platform) diag.Diagnostics  // validateCommon + account format, certificates, provider rules
    CheckWorkloads(p *config.Platform) diag.Diagnostics // sizing (mirror the module rounding), DB extensions, capabilities
    PlanChecks(p *config.Platform, getenv func(string) string) diag.Diagnostics // account/state/locking at plan time
    Providers() []TerraformProvider                // exact pins, primary first (plus SharedProviders: random)
    ProviderConfigs(p *config.Platform) map[string]map[string]any // provider block bodies (region, tags, account guard)
}
```

- **Register it** in `init()` in `providers.go` (`register(<p>Adapter{})`).
- **Pins**: add `Pin<P>` to `versions.go` with the exact version the modules were tested with.
- **Capability matrix**: every feature in `AllFeatures`. Be strict. If part of the portable semantics is missing,
  the feature is `Partial`; if users must do a manual step, say so in the note. The matrix drives
  `hermes portability` and `hermes providers`, which print that the statuses are mock-tested only.
- **Strict extensions**: implement `strictExtensions() bool { return true }` if the composition rejects unknown
  keys, so that validation reports them as errors rather than warnings.
- **Mirrors**: when the module rounds a value (as Cloud Run and SAE sizes do), implement the same function in Go,
  name it after the module, and test both sides with the same table.
- **Diagnostics**: use the existing `HCF2xx` codes ([diagnostics](../reference/diagnostics.md)). A new code needs
  a row there.

Other Go touch points:

| File | What to add |
|---|---|
| `internal/config/types.go` | `Provider<P>` constant, `Providers` list, default backend in `DefaultBackendFor` |
| `schemas/platform/v1alpha1.schema.json` | the `cloud.provider` enum, `extensions.<p>`, any new backend |
| `internal/state/state.go` | backend configuration and location; the encryption key provider if OpenTofu has one (otherwise reject client-side encryption, as Alibaba does with `HCF208`) |
| `internal/cli/init.go` | default example region, account hint, template extensions |
| `internal/cli/state.go`, `internal/cli/tools.go` | `state show` locking text; `cost` support (Infracost or not) |
| `internal/diagnostics/doctor.go` | credential-source environment variable names and well-known files (names only, never values) |
| `internal/render/testdata/golden/` | golden roots for an example environment on `<p>` (`go test ./internal/render -update`) |
| `policies/plan/*.rego` + `policies/tests/` | rules for `<p>` resource types (encryption, public access, DB exposure, IAM wildcards, ingress, tags, protection), each with pass and fail fixtures |
| `schemas/migration/*`, `migration/mappings/catalog.yaml` | only if `<p>` becomes a migration target |

Tests: `go test ./internal/providers` (matrix completeness, regions, extension validation, sizing), plus
`internal/config`, `internal/render` and `internal/cli`.

## 4. Documentation

- `docs/providers/<p>.md`, using the existing guides as the template: status banner (mock-tested only), layout,
  architecture diagram (Mermaid), service choices, feature matrix (**every** feature, including
  `state.client_side_encryption`, with the same status as the Go matrix), sizes, extension keys (all of them,
  including keys read only by `hermes`), naming, protected behaviour, prerequisites and deploy permissions,
  post-provision steps, cost notes, tests, known limitations.
- Add a column to the [comparison matrix](comparison-matrix.md).
- Add a section to the [limitations register](../limitations.md).
- Add networking and cost rows to [networking](../operations/networking.md) and
  [cost and sizing](../operations/cost-and-sizing.md).
- Add a `CHANGELOG.md` entry.

## 5. Definition of done

- [ ] `variables.tf` is identical to the contract; the contract runs are verbatim; `make contract-test` passes
- [ ] module and composition `tofu test` pass with mock providers (`make tf-test`); `make tf-validate` is clean
- [ ] `make tf-compat` passes with Terraform
- [ ] the Go adapter is registered; the matrix covers all 21 features; `go test ./...` passes; the golden roots are reviewed
- [ ] `make integration` renders and validates an example environment for `<p>`
- [ ] policies have pass and fail fixtures for `<p>`; `make policy-test` passes
- [ ] scanners run where they support `<p>`; gaps in scanner coverage are documented
- [ ] the provider guide, comparison matrix, limitations, networking and cost docs are updated; the Go matrix and
      the guide agree row by row
- [ ] every feature is labelled honestly: *implemented, not tested against a real cloud* until a recorded real run
      exists
