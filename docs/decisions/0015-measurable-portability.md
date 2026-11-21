# ADR 0015 — Portability is measured, not promised

- Status: Accepted (2026-11-18)

## Context

"Works on any cloud" claims are usually untested and hide meaningful differences. The brief requires portability
to be "explicit and measurable".

## Decision

- Each provider adapter publishes a **capability matrix**: feature → `implemented` | `partial` |
  `not-implemented`, with a note. Example features are `database.high_availability`, `compute.scale_to_zero`,
  `messaging.dead_letter` and `state.client_side_encryption`.
- The matrix is reconciled with what the HCL compositions actually implement and test. Each
  `docs/providers/<provider>.md` page carries the same matrix with its test evidence.
- `hermes portability` scores a spec against every provider. The score is the weighted share of *requested*
  features (implemented = 1, partial = 0.5), followed by the specific differences that matter for that spec.
- For migrations, `hermes migrate assess` computes a portability score of the *source estate* against the target
  from the service catalog (`compatibility` × `hermes_support`). It also lists blockers and undiscovered scope.

## Consequences

- A feature is never silently dropped on a provider. Either validation fails, or the matrix and a warning say
  what will differ.
- Keeping the matrix truthful is part of the definition of done for any provider change.
