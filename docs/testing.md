# Testing guide

Hermes is tested **without cloud credentials** ([ADR 0013](decisions/0013-testing-without-cloud-credentials.md)).
Every layer below runs on a laptop or in CI with no cloud account. This page says how to run each layer, what it
proves, and, just as importantly, what it does **not** prove.

> **No test in this repository has been run against a real cloud.** An opt-in, read-only real-AWS discovery
> test exists but has never run. There is no real-cloud provisioning test.

Run anything that might pick up credentials with the isolation prefix. The `Makefile` does this through
`$(ISOLATE)`; see [installation](tutorials/installation.md#working-without-touching-real-clouds).

## Layers at a glance

| # | Layer | Command | Evidence (latest recorded run) |
|---|---|---|---|
| 1 | Go unit tests (with the fake engine and `httptest`) | `make test` (`go test ./...`) | 151 top-level tests (262 including subtests) in 22 packages pass, 0 failed, 0 skipped (2026-11-21) |
| 2 | Golden files (rendered roots, discovery IAM policy, catalog markdown) | part of `make test` | 10 golden roots in `internal/render/testdata/golden/` |
| 3 | Integration: render every example and `init -backend=false` + `validate` with the real engine | `make integration` | 10/10 example environments with OpenTofu. **Not run with Terraform** |
| 4 | Module tests with `mock_provider` | `make tf-test` | 144/144 `run` blocks pass in 36 `*.tftest.hcl` files across 32 directories (AWS 58, GCP 47, Alibaba 39); on the compositions AWS 11/11, GCP 17/17, Alibaba 12/12; `tf-validate` 37/37 (2026-11-21) |
| 5 | Contract checks + canonical contract runs | `make contract-test` | static checks in `tests/contracts`; 5 canonical runs per composition |
| 6 | Engine compatibility (Terraform) | `make tf-compat` | the three compositions validate and test with Terraform 1.15.6 (recorded before remediation round 2; **not re-run** after it, for lack of disk space) |
| 7 | Plan policies (conftest) | `make policy-test` | `conftest verify`: 41/41 Rego tests (common 9, AWS 15, GCP 5, Alibaba 6, composition-derived fixtures 6) (2026-11-21) |
| 8 | Scanners | `make security` | trivy config 0 findings; checkov 3.4.5 0 failed (terraform 284 and 568 passed, github_actions 310 passed; justified inline skips); gitleaks no leaks (2026-11-21); tflint clean (earlier run) |
| 9 | Discovery against a local moto server | `make demo-discovery`; `go test -tags moto ./tests/integration/discovery/...` | 58 resources / 28 types; 81 operations, all allowlisted; passes (recorded by the migration workstream) |
| 10 | Opt-in real cloud | `go test -tags realaws ./tests/integration/aws-real/` with `HERMES_IT_ALLOW_REAL_AWS=1` | **never run** |
| 11 | Docs links | `make docs-links` (lychee offline, with anchors) | see the lead's integration notes |

The numbers in layers 3–9 were recorded by the workstreams that own those layers, on 2026-11-19 and 2026-11-20.
Re-run the commands to reproduce them.

## 1. Go unit tests

```bash
make test                         # or: go test -count=1 ./...
go test ./internal/execution -run TestCheckApply -v
```

- **Fake engine:** `internal/execution/enginetest` records every engine invocation, writes plan files and
  returns canned `show -json` / `output -json` documents. It never executes a binary. CLI tests inject it through
  `App.DetectEngine`. Time, environment, `LookPath`, the user name and auxiliary tools are injectable too
  (`App.Now`, `App.Getenv`, `App.RunTool`).
- **httptest and local listeners:** migration validation checks (`http` over TLS with a test CA, `tcp`, `dns`,
  `command`) run against servers on `127.0.0.1`.
- **Read-only guard:** `internal/inventory/aws/guard_test.go` checks that every registered operation is
  read-named, that the denylist (secret and data reads) is refused before sending, and that the generated IAM
  policy equals `migration/discovery/aws-readonly-discovery-policy.json`.

**Proves:** spec parsing and validation, overlays and defaults, provider rules and capability matrices,
deterministic rendering, every safety-gate refusal path, history, policy wiring, the discovery guard, inventory
normalization, graph waves, assessment, plan generation and tamper detection, the cutover state machine with
test hooks, and the import-only adoption gate.
**Does not prove:** that the engine accepts the rendered root (layer 3), or that any cloud behaves as assumed.

## 2. Golden files

| Golden | Regenerate |
|---|---|
| `internal/render/testdata/golden/*.tf.json` (every example environment) | `go test ./internal/render -update` |
| `migration/discovery/aws-readonly-discovery-policy.json` | `HERMES_UPDATE_GOLDEN=1 go test ./internal/inventory/aws/ -run Policy` |
| `internal/migration/testdata/catalog.golden.md` | `HERMES_UPDATE_GOLDEN=1 go test ./internal/migration -run TestCatalogMarkdownGolden` |

Always review the diff. **Proves:** an output change is intentional. **Does not prove:** that the output is
correct. A golden is only as good as its review.

## 3. Integration (`integration` build tag)

```bash
HERMES_TF_INTEGRATION=1 TF_PLUGIN_CACHE_DIR=$HOME/.cache/hermes-tf-plugins \
  go test -tags integration ./internal/render -run TestIntegration -v        # HERMES_TF_ENGINE=terraform for Terraform
go test -tags integration ./internal/policy -run TestIntegration -v          # the real conftest binary
```

It renders every example environment with the local backend and the repository's `infra/` as the modules
directory, then runs `init -backend=false` and `validate`. It never plans or applies.
**Proves:** the rendered JSON is valid against the real provider schemas (pinned versions).
**Does not prove:** runtime behaviour, or any plan-time API validation.

## 4. Module tests (`tofu test` with `mock_provider`)

```bash
make tf-test
cd infra/providers/gcp/platform && tofu init -backend=false && tofu test     # one composition
```

Tests live in `tests/*.tftest.hcl` next to each module and composition. Most runs use `command = apply`
against mock providers, so computed values exist and outputs can be asserted. **Proves:** wiring (variables reach
the right arguments), conditional logic (protected, HA, CMK, NAT on and off), least-privilege policy **contents**,
preconditions and guardrails, and output shapes.
**Does not prove:** that the cloud API accepts the request, that argument combinations are valid at apply time,
IAM propagation, quotas, or region availability. Mock values are synthetic. For example, every mocked GCP service
account gets the same email ([GCP guide](providers/gcp.md#tests)).

## 5. Contract tests

`tests/contracts/contract_test.go` checks, statically, that every composition's `variables.tf` is identical to
`infra/contract/variables.tf`, that the canonical section of every `tests/contract.tftest.hcl` is byte-identical
to `tests/contracts/contract-runs.hcl`, that outputs match `infra/contract/outputs.md`, and that no output is
sensitive. `make contract-test` then runs the canonical runs (`contract_minimal` and others) against each
composition with mock providers.
**Proves:** the same inputs produce the same output **shapes** and binding variable names on every provider.
**Does not prove:** semantic equivalence. An SQS queue and a Pub/Sub subscription still behave differently
([application portability](architecture/application-portability.md)).

## 6. Engine compatibility

`make tf-compat` validates and tests the three compositions with Terraform. Modules contain no engine-specific
features. OpenTofu-only features (client-side state encryption) are emitted only by the renderer.
**Proves:** that the modules parse and pass the mock tests on both engines. **Does not prove:** identical runtime
behaviour of the two engines.

## 7. Plan policies

```bash
make policy-test          # conftest verify --policy policies/plan --policy policies/tests
```

Each rule has pass and fail fixtures (synthetic `plan.json` files in `policies/tests/fixtures/`) for AWS, GCP
and Alibaba, plus fixtures for destructive changes, unknown values and inputs that are not plans. Three more
fixtures, `policies/tests/fixtures/{aws,gcp,alibaba}_default`, are **derived** from mock-provider plans of the
real compositions (the golden render of `examples/multi-cloud`) by `scripts/gen-policy-fixtures.sh`; every deny
rule must pass on them, and `make policy-fixtures-check` (run in the infra workflow) fails when a module change
makes them stale.
**Proves:** the rules fire and pass as intended on those fixtures, and the default configuration of every
composition passes every deny rule. **Does not prove:** coverage of every misconfiguration or configuration, or
behaviour on plans produced from real provider responses (mock providers fill computed values with mock data). For Alibaba, these policies are
the **only** automated guardrail (there are no TFLint or Trivy rules).

## 8. Scanners

`make security` runs `scripts/security-scan.sh`: gitleaks, trivy config, checkov, tflint and conftest.
**Proves:** known static misconfiguration patterns are absent or justified (see the inline ignores).
**Does not prove:** security of the deployed system. Checkov 3.4.5 (pinned in CI) needs Python 3.10 or later. Alibaba coverage is
minimal upstream ([research §7](research/open-source-landscape.md#7-lint-security-policy-and-cost-tooling-the-claudemd-toolchain)).

## 9. Discovery against moto (`moto` build tag)

```bash
python3 -m venv .venv && .venv/bin/pip install 'moto[server]' boto3     # ~180 MB, once
make demo-discovery                                                      # or VENV=.venv examples/migration/aws-discovery-demo/run-demo.sh
```

The demo starts moto on `127.0.0.1` with fake keys and isolated AWS config files. It seeds a hand-built
environment with boto3 (no Terraform), then runs `hermes inventory aws` through an assumed role with an
ExternalId. A second run turns on moto's IAM enforcement and checks that access-denied errors become gaps.
`go test -tags moto ./tests/integration/discovery/...` (with `HERMES_MOTO_ENDPOINT`) asserts the result, and
refuses any non-localhost endpoint.
**Proves:** the end-to-end flow, the read-only guard on real SDK requests, gap recording, and the graph.
**Does not prove:** AWS behaviour that moto does not emulate, such as Cloud Control, Resource Explorer, CBOR
CloudWatch operations, or partial EC2/SSM coverage ([research C8](research/open-source-landscape.md#10-where-the-evidence-contradicts-or-strains-the-current-architecture)).

## 10. Opt-in real-cloud tests (`realaws` build tag)

```bash
HERMES_IT_ALLOW_REAL_AWS=1 HERMES_IT_AWS_ROLE_ARN=arn:aws:iam::<account>:role/HermesDiscoveryReadOnly \
HERMES_IT_AWS_EXTERNAL_ID=<id> HERMES_IT_AWS_REGION=eu-north-1 \
  go test -tags realaws ./tests/integration/aws-real/
```

This is read-only discovery in **your own test account** through the discovery role. It skips unless explicitly
enabled. It is never run by CI or by the project's tooling, and **it has never been run**. Real-cloud
provisioning tests (plan/apply/destroy in a disposable account) are *designed, not implemented*. Until they exist
and are recorded, no document may claim real-cloud behaviour.

## What CI runs

`make ci` runs lint, test, contract-test, tf-validate, tf-test, policy-test and docs-links. The GitHub workflows
(`ci.yml`, `infra.yml`, `security.yml`, `docs.yml`, `discovery-demo.yml`) have no cloud access. `integration.yml`
is manual, plan-only by default, and behind environment approval
([CI/CD authentication](security/ci-cd-authentication.md)). The workflows are SHA-pinned but have **not yet run on
GitHub**.

## Writing tests: rules of thumb

- A new gate or refusal needs a test proving that it triggers, and that the happy path still passes.
- A new provider feature needs a `tofu test` run in that provider's module or composition tests, plus the Go
  matrix entry. If it changes the contract, it also needs the contract runs.
- A new collector operation goes in the registry. The guard test and the policy golden then force you to justify
  it.
- Never put real account IDs, hostnames or secrets in fixtures. Use `123456789012`, `example.com` and
  `demo-not-a-secret`.
