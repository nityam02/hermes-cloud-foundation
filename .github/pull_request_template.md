## What and why

<!-- What changes, and which problem it solves. Link the issue or ADR. -->

## How it was tested

<!-- Exact commands and results, e.g. `make test`, `make tf-test`, `make policy-test`. -->
<!-- Say what was NOT tested (for example: "not tested against a real cloud"). -->

## Checklist

- [ ] No secrets, credentials, real account ids or real company/customer names (fixtures use `123456789012`, `example.com`, `demo-not-a-secret`).
- [ ] No cloud API calls in tests; engine tests use mock providers only.
- [ ] Safety gates unchanged, or the change is explained (saved-plan apply, `--allow-destructive`, `--approve`, read-only discovery).
- [ ] IAM changes are least-privilege and reviewed (no `*` actions, no wildcard OIDC subjects, no new long-lived keys).
- [ ] New or changed resources carry the Hermes tags/labels and pass `make policy-test` and `make security`.
- [ ] GitHub Actions are pinned to full commit SHAs with a version comment.
- [ ] Docs and `docs/limitations.md` updated with an honest status (implemented & verified / not tested against real cloud / designed / not supported).
