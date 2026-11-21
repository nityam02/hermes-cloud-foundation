# ADR 0014 — Cutover runs only user-declared hooks; decommissioning is never automated

- Status: Accepted (2026-11-18)

## Context

Traffic cutover and source decommissioning are the riskiest steps of a migration. Each organisation's DNS,
traffic management, database promotion and change process differ. A generic tool that "does the cutover" would
either be dangerously opinionated or a placeholder.

## Decision

- `hermes migrate cutover` is a **gated state machine with an audit trail** and runs as a **dry run by default**.
- Execution requires all of the following:
  - a passing validation report for the same `plan_id` that is newer than its maximum age;
  - `--approve <plan-id-prefix>`;
  - `--confirm-target <environment>`;
  - for any step that touches the source, `--approve-source-step <id>`.
- Hermes has **no built-in action** that changes DNS, databases or source resources. During cutover it runs only
  the hook commands that the operator declared for each step (an argv list, no shell). Operators can record
  manual steps as completed. Rollback runs the declared rollback hooks of completed reversible steps, and lists
  the irreversible ones. An example of an irreversible step is accepting writes on the target after promotion.
- Decommissioning the source is **planned and recorded, never executed** by Hermes.

## Consequences

- Teams wire Hermes to their own tooling: weighted DNS changes, a database promotion script, a feature-flag flip.
  Hermes enforces ordering, gates and evidence.
- This is less "one-command" than a fully automated cutover. That is intentional: the brief requires that
  production cutover is never automatic.
