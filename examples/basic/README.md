# Example: the minimal spec

This is the developer's minimal example from
[data-contracts §1](../../docs/architecture/data-contracts.md), verbatim: a production
platform on Google Cloud with a container runtime, a highly available PostgreSQL database,
a bucket, a topic, secrets and observability, all with defaults.

```bash
hermes validate -c examples/basic/hermes.yaml          # valid, with warnings
hermes render -c examples/basic/hermes.yaml --print    # the normalized spec (all defaults filled)
```

`hermes validate` reports three warnings, as the contract says it should: no `apiVersion`, no
`cloud.account` (an error at plan time: `production` is protected, so the GCP project must be
in the spec) and no `state.bucket` (required to plan with the GCS backend). Defaults it fills
include the bucket `data`, the topic `events` with subscription `events-default`, and, because
`production` is protected, customer-managed keys and database deletion protection.
