# Contributing to Hermes Cloud Foundation

Thank you for helping. This project manages infrastructure that people trust with production systems, so the bar
is correctness and safety first, then simplicity.

## Before you start

- Read [`CLAUDE.md`](CLAUDE.md). It is the engineering brief for humans and coding agents alike: architecture,
  non-negotiable rules, conventions.
- Read the relevant [ADRs](docs/decisions/README.md). If your change contradicts one, propose a new ADR first.
- For anything larger than a bug fix, open an issue describing the problem and the proposed approach.

## Ground rules

1. **No real cloud calls in tests.** Unit tests use fakes, module tests use `mock_provider`, the discovery demo
   uses moto. Real-cloud tests are opt-in only and never run in CI by default.
2. **No secrets.** Never commit credentials, keys, tokens or real account identifiers. Fixtures use obviously
   fake values (`123456789012`, `example.com`, `demo-not-a-secret`).
3. **Safety gates are not optional.** Do not add code paths that apply without a saved plan, pass
   `-auto-approve`, or modify a discovery source. Changes to gates need a test proving that each refusal still
   triggers.
4. **Honest capability claims.** If you add or change a provider feature, update the capability matrix in Go,
   `docs/providers/<provider>.md`, and `docs/limitations.md` in the same change.
5. **Contracts move together.** A change to `docs/architecture/data-contracts.md` must update the JSON Schema
   under `schemas/`, `infra/contract/`, every provider composition, and the contract tests in one change.

## Development workflow

```bash
make build          # bin/hermes
make lint test      # gofmt, go vet, golangci-lint, unit tests
make contract-test  # provider contract checks (+ mock-provider runs; downloads providers)
make tf-validate tf-test
make policy-test
make security       # gitleaks, trivy, checkov, tflint, conftest
```

The tools you need are listed in [`docs/tutorials/installation.md`](docs/tutorials/installation.md).

## Adding a provider

Follow [`docs/providers/implementing-a-provider.md`](docs/providers/implementing-a-provider.md). A provider is
accepted when it:

- implements the composition contract (`infra/contract/`);
- passes the shared contract runs and its own mock tests;
- has a Go adapter with a truthful capability matrix;
- documents its gaps.

## Commit and PR conventions

- One logical change per PR. Write the commit subject in the imperative mood ("Add GCS soft-delete setting").
- Describe in the PR what you tested and how. For example: "tofu test (mocks)" is not the same as "applied in a
  test account".
- CI must be green: lint, unit, contract, module tests, policy tests, security scans and link checks.

## Reporting security issues

Do not open public issues for vulnerabilities. See [`SECURITY.md`](SECURITY.md).

## Code of conduct

Participation is governed by the [Contributor Covenant](CODE_OF_CONDUCT.md).
