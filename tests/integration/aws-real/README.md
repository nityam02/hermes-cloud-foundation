# Opt-in real-account discovery test (`realaws`)

**Never run by CI or by the Hermes maintainers' agents.** It exists so an operator can check
discovery against their own account with the dedicated read-only role.

What it does: assumes the discovery role (session `hermes-discovery`, 1 hour, with your
ExternalId), runs the read-only collectors in one region, asserts the principal is the assumed
role and that every API operation sent is on the read-only allowlist, and logs the summary and
gaps. It reads metadata only: secret values, object data, queue messages and table items are
refused by the client-side guard before any request and denied again by the role policy.

```bash
# 1. Deploy the role in the account to inspect (see migration/discovery/README.md).
# 2. Run with credentials that may assume it:
HERMES_IT_ALLOW_REAL_AWS=1 \
HERMES_IT_AWS_ROLE_ARN=arn:aws:iam::<account>:role/HermesDiscoveryReadOnly \
HERMES_IT_AWS_EXTERNAL_ID=<external-id> \
HERMES_IT_AWS_REGION=eu-north-1 \
HERMES_IT_OUT=/tmp/inventory.json \
go test -count=1 -tags realaws ./tests/integration/aws-real/ -v
```

Without `HERMES_IT_ALLOW_REAL_AWS=1` the test is skipped. Status: implemented, not tested against a
real cloud.
