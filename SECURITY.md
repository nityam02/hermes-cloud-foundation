# Security policy

## Reporting a vulnerability

Report vulnerabilities **privately** through GitHub's private vulnerability reporting:
open the repository's **Security** tab and choose **Report a vulnerability** (GitHub Security
Advisories). Do not open a public issue, pull request or discussion for a suspected vulnerability.

Please include the affected version or commit, the component (see Scope), reproduction steps,
the impact you expect, and whether the issue is already public. Never include real credentials,
account identifiers or customer data in a report; use fake values such as `123456789012`.

What to expect:

| Step | Target |
|---|---|
| Acknowledgement | within 5 working days |
| First assessment (severity, scope) | within 10 working days |
| Fix or mitigation for High/Critical | as fast as practical; coordinated disclosure date agreed with the reporter |
| Advisory | published with the fix, crediting the reporter unless they prefer otherwise |

These are goals of a volunteer-maintained open-source project, not contractual service levels.

## Supported versions

Hermes is pre-1.0 (`v1alpha1` contracts). Only the latest release and the `main` branch receive
security fixes. Engines: OpenTofu >= 1.10 and Terraform >= 1.11 are supported; OpenTofu lines
that no longer receive upstream security support (1.8 and 1.9) are not.

## Scope

In scope:

- the `hermes` CLI (`cmd/`, `internal/`): rendering, execution safety gates, policy evaluation,
  read-only discovery, migration planning and cutover hooks;
- the HCL modules (`infra/`), including the CI identity bootstrap modules;
- the plan policies (`policies/`), scanner configuration, scripts and GitHub Actions workflows;
- documentation that gives security guidance (`docs/security/`, `docs/disaster-recovery/`).

Out of scope: vulnerabilities in OpenTofu, Terraform, cloud providers, provider plugins or
third-party tools (report them upstream; tell us if Hermes must react), findings that need an
already-compromised workstation or CI administrator, and deployments that changed the defaults
(for example, removed GitHub environment protection rules).

## What Hermes will never do

- Apply silently: `apply` only applies a saved plan; deletions and replacements need
  `--allow-destructive`; protected environments need `--approve <plan-id-prefix>`; `destroy`
  always needs approval. Cutover and source decommissioning are never automatic.
- Write to a source estate during discovery: collectors may only call allowlisted
  `Describe*`/`List*`/`Get*`(non-secret)/`Search`/`Select*`/`Lookup*` operations, enforced
  before any request is signed or sent.
- Read or store secret values: secret values are never read by discovery and never managed by
  IaC; state, plans, inventories, outputs and logs must not contain them.
- Use long-lived cloud keys in CI: CI authenticates with OIDC federation only.
- Send telemetry, inventories or plans anywhere. Everything stays on your machine or in your
  CI unless you move it.

## Hardening guidance

See [docs/security/security-architecture.md](docs/security/security-architecture.md),
[docs/security/threat-model.md](docs/security/threat-model.md) and
[docs/security/ci-cd-authentication.md](docs/security/ci-cd-authentication.md).
