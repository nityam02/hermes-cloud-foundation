# ADR 0011 — Policy as code with Conftest on plan JSON

- Status: Accepted (2026-11-18)

## Context

Organisations need enforceable guardrails, for example "no public buckets", "no publicly reachable databases",
and "no wildcard IAM". Terraform 1.17 introduced native policies, but OpenTofu has nothing equivalent [§2].
Static scanners (Checkov, Trivy, TFLint) inspect code. They cannot see values that only resolve at plan time,
and their Alibaba coverage is thin [§7].

## Decision

- Organisation policy is written in **Rego** and evaluated by **Conftest** against `show -json` of the saved
  plan. This works the same way for both engines and all providers.
- The built-in policies live in `policies/plan/`, are embedded in the CLI, and can be extended or replaced with
  `--policy-dir`. Each rule carries a stable identifier (`HCF-POL-nnn`).
- Static scanners run in CI as defence in depth: Checkov, Trivy config, TFLint and gitleaks. Because scanner
  coverage of Alibaba resources is weak, the Rego rules cover the critical Alibaba cases explicitly.
- The execution gates in ADR 0007 are not policy. They are hard-coded safety behaviour that an organisation
  policy cannot weaken.

## Consequences

- Protected environments require Conftest to be present, unless the operator gives an explicit
  `--skip-policy`, which is recorded.
- The rules can only see what the plan shows. Values that are unknown until apply are treated conservatively,
  and each such case is documented on the rule.
