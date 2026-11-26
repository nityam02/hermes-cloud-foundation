# The Hermes UI

A local, **read-only** web page for the documents Hermes already produces. It shows an inventory,
an assessment and a migration plan in one place, so someone who does not live in a terminal can
review them. It cannot plan, apply, approve or cut over anything.

```bash
hermes inventory aws --role-arn <role> --external-id <id> --out inventory.json
hermes migrate assess --inventory inventory.json --target gcp --out assessment.json
hermes migrate plan --assessment assessment.json --out plan.yaml

hermes ui --inventory inventory.json --assessment assessment.json --plan plan.yaml --open
```

Open `http://127.0.0.1:8787/`. Add `?demo` to the address to see a synthetic sample without any files.

## What each page shows

| Page | Shows |
|---|---|
| Overview | Portability score and how it is calculated, blockers and risks, data volume, indicative transfer cost, migration waves |
| Inventory | Every resource with where it lands on the target, fit, effort and wave; search and filters; per-resource detail |
| Plan | The phases as a route, each step with its gate (approval or validation), whether it changes the source, whether it is reversible, its checks, risks and rollback |
| Gaps | Calls the read-only role could not make, and resources without a ready target |

## Security model

- **Loopback only.** The server refuses any address that is not `127.0.0.1`, `::1` or `localhost`,
  because the UI has no authentication.
- **DNS-rebinding defence.** Requests whose `Host` header is not loopback are refused.
- **Read-only.** Only `GET` and `HEAD` are accepted. There is no endpoint that changes anything.
- **No outside requests.** The page loads nothing from other sites (Content-Security-Policy
  `default-src 'self'`, system fonts only).
- **Plan integrity.** `hermes ui --plan` checks the plan's `plan_id` against its body, the same check
  as the other `migrate` commands, and refuses a plan that was edited after generation.
- **No credentials.** The UI never asks for AWS or GCP access. Discovery stays in the CLI, which uses
  the read-only role described in [`migration/discovery/`](../../migration/discovery/README.md).

## Develop the UI

The app lives in [`ui/`](../../ui) (React, TypeScript, Vite). The CLI embeds the build.

```bash
make ui-install      # npm ci, no install scripts
make ui-test         # typecheck and unit tests
make ui-build        # production build, copied to internal/uiserver/dist
make build           # CLI with the UI embedded
cd ui && npm run dev # hot reload; proxies /api to a running `hermes ui` on 127.0.0.1:8787
cd ui && npm run demo-data   # regenerate the synthetic sample from examples/migration
```

A CLI built without `make ui-build` still works; `hermes ui` then serves a short "UI is not built"
page and the JSON API.

## Status

Implemented and tested with unit tests and a browser check against a real sandbox discovery.
Not implemented: connecting an AWS account from the page, starting a discovery or a migration,
approvals, and a hosted multi-user version. Those need authentication, audit and a threat model of
their own; see [limitations](../limitations.md).
