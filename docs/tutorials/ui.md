# The Hermes UI

A local web page that walks you through connecting AWS and Google Cloud, checks both, runs discovery,
assessment and planning, and shows the results. It runs on your machine from the `hermes` binary and
uses your own cloud sign-ins. It never asks for access keys or key files.

```bash
hermes ui --open
```

`hermes ui` prints a link with a private session token. Open that link (`--open` does it for you).

## The flow

1. **Connect AWS.** Pick the sign-in Hermes should use (an AWS profile). Hermes shows who you are and
   refuses the root user, because AWS never lets root assume a role. Hermes then generates an external ID
   and gives you a CloudFormation template that creates a **read-only** role trusting only your
   sign-in. Create the stack in the console (or with the command shown), paste the role ARN, and
   Hermes checks it: the role can be assumed, the account can be read, and which read calls were denied.
2. **Connect Google Cloud.** Enter the project ID and region. Hermes uses Application Default
   Credentials (`gcloud auth application-default login`) and checks the project, billing, the APIs that
   must be on, and which of the permissions Hermes needs your account has. Missing deploy permissions
   are a warning, not a failure, because discovery and planning do not need them.
3. **Discover and plan.** One click runs discovery, assessment and planning as a job with named steps.
   The results appear on the Overview, Inventory, Plan and Gaps pages. Files are saved under
   `.hermes/ui/runs/<time>/` so the CLI can use them too.

Every check explains a failure in plain language and says how to fix it.

## What each page shows

| Page | Shows |
|---|---|
| Connect | The three steps above |
| Overview | Portability score and how it is calculated, blockers and risks, data volume, indicative transfer cost, migration waves |
| Inventory | Every resource with where it lands on the target, fit, effort and wave; search and filters |
| Plan | The phases as a route, each step with its gate, whether it changes the source, whether it is reversible, its checks, risks and rollback |
| Gaps | Calls the read-only role could not make, and resources without a ready target |

You can also open existing documents: `hermes ui --inventory inventory.json --assessment assessment.json --plan plan.yaml`.
Add `?demo` to the address to see a synthetic sample without any cloud access.

## Security model

- **Loopback only.** The server refuses any listen address that is not `127.0.0.1`, `::1` or `localhost`.
- **Token and cookie.** The printed link carries a random 256-bit token. The server exchanges it for an
  `HttpOnly`, `SameSite=Strict` cookie and removes it from the address. Every API call needs the cookie.
- **Cross-site protection.** Every `POST` needs a custom header, a JSON content type and a same-origin
  `Origin`, so a web page on another site cannot trigger an action. Requests whose `Host` header is not
  loopback are refused (DNS rebinding).
- **No outside requests.** The page loads nothing from other sites (CSP `default-src 'self'`, system fonts).
- **No stored credentials.** Hermes uses your sign-in at the moment of the call. The external ID is an
  anti-confusion value that AWS does not treat as secret; it is kept in the tab's session storage.
- **Read-only against your clouds.** The AWS reads go through the same guard as the CLI: an operation
  allowlist is enforced before any request is signed. The Google checks only read project metadata and
  call `testIamPermissions`.
- **Plan integrity.** A plan opened with `--plan` has its `plan_id` verified against its body.

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

A CLI built without `make ui-build` still works; `hermes ui` then serves a short "UI is not built" page.

## Status

| | |
|---|---|
| Connect AWS and Google Cloud with live checks | implemented. The AWS path was checked against a real sandbox account (role assumed, 9 resources read, a missing permission reported). The Google path is tested against a fake Google API only, because no Google credentials were available |
| Discovery, assessment and plan from the page | implemented; discovery verified on a real sandbox, the job runner tested with fakes |
| Deploy a new service from a form (spec, validation, rendered OpenTofu, plan) | designed, not implemented |
| Apply, destroy and approvals from the page | designed, not implemented |
| Migration execution (target provisioning, data movement, cutover) | designed, not implemented |
| Hosted multi-user version | not supported |

The design and the reasons are in [ADR 0016](../decisions/0016-ui-control-plane.md).
