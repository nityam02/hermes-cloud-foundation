# ADR 0016 — The UI is a local control plane over the CLI, not a second engine

- Status: Accepted (2026-11-27)

## Context

The first UI (`hermes ui`) was read-only. Users want one interface to connect AWS and GCP, check that
the connection works, run discovery and assessment, review the plan, and later deploy a new service or
run a migration, with Terraform generated underneath. The research this ADR rests on:

- HashiCorp's no-code provisioning (form in, module run out) shows the pattern: users never write HCL,
  a platform team owns the module, and the run is the audit trail. Hermes already has the equivalent:
  a validated `hermes.yaml` spec rendered to OpenTofu.
- AWS says the third party generates the external ID and the customer's role trusts one principal
  with that condition. Google's Storage Transfer Service can read S3 through a role ARN (federated
  identity, no access keys), so the role-based approach used for discovery also covers data movement.
  Google's Migration Center asks for AWS access keys, which Hermes avoids by design.
- Google's `testIamPermissions` reports which permissions the caller holds on a project, which makes a
  preflight possible; it reports permissions, not a guarantee that a deployment succeeds.
- Jupyter's model for local servers: a random token that becomes a cookie, plus Host checking, is the
  accepted way to expose a powerful local service in a browser.

## Decision

1. The UI stays a **thin layer over the same Go packages the CLI uses**. It adds no deployment logic. A
   UI action runs the same discovery, assessment, planning, validation, rendering and gated apply.
2. The UI server is **loopback only and authenticated**: a random token printed by `hermes ui`,
   exchanged for an HttpOnly `SameSite=Strict` cookie, plus Host and Origin checks, and a required custom
   header on every `POST` so a web page on another site cannot trigger an action.
3. **No long-lived cloud credentials pass through the UI.** AWS: the operator signs in with their own
   identity (`aws login`, SSO or a profile) and Hermes assumes the customer's read-only role with the
   external ID. GCP: Application Default Credentials (`gcloud auth application-default login`), service
   account impersonation or workload identity federation. The UI never accepts access keys or JSON keys.
4. **Connection checks are read-only and explain failures.** Each check is a list of steps with a status,
   what was found and how to fix it (for example: root cannot assume a role).
5. **Changes keep the safety gates.** Discovery, assessment and planning are read-only. Anything that
   creates, changes or deletes cloud resources is a saved plan that a person approves by typing the plan
   id prefix, exactly as on the CLI. Cutover and decommissioning stay out of automatic flows.
6. Long operations run as **jobs** with named steps, so the page shows progress instead of a spinner.

## Phases

| Phase | Scope | State |
|---|---|---|
| 1 | Connect AWS and GCP with live checks; run discovery, assessment and plan as a job; read-only views | this change |
| 2 | Deploy a new service: form builds `hermes.yaml`, validation, rendered OpenTofu preview, plan with policy results | designed |
| 3 | Apply from the UI: approval by plan id, streamed logs, destroy with confirmation | designed |
| 4 | Migration execution: provision target from the draft spec, data movement through managed services, validation, gated cutover | designed |

## Consequences

- The UI cannot do anything the CLI cannot, and the CLI stays the source of truth for gates.
- A hosted multi-user version is still out of scope: it would hold cross-account access for many people.
- Checks call real cloud APIs on the user's own accounts when the user clicks. They are read-only.
