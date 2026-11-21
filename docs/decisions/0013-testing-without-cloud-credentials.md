# ADR 0013 — Test without cloud credentials; real-cloud tests are opt-in

- Status: Accepted (2026-11-18)

## Context

The project must be testable by any contributor and in CI without cloud accounts. It must also never claim
real-cloud behaviour it has not observed.

## Decision

| Layer | How it is tested | What it proves | What it does not prove |
|---|---|---|---|
| Go CLI | `go test ./...` with a fake engine runner, golden files, and `httptest` | Validation, rendering, gates, migration logic | That the engine accepts the output (see the next row) |
| Rendered roots | `tofu init -backend=false && tofu validate` (integration tag) | Rendered JSON is valid against real provider schemas | Runtime behaviour |
| Modules | `tofu test` with `mock_provider` (`command = apply`) | Wiring, contract shape, conditional logic, least-privilege policy contents | Provider API acceptance and real-cloud behaviour |
| Contracts | Identical fixtures and assertions on every composition | Same inputs produce the same output shapes and env names | Semantic equivalence of services |
| Policies | `conftest verify` with synthetic plans | Rules fire and pass as intended | Coverage of every misconfiguration |
| Discovery | Fakes plus a **moto** server demo | Collectors, read-only guard, gaps, graph, end-to-end flow | AWS-specific edge cases that moto does not emulate (Cloud Control, Resource Explorer) |
| Real cloud | Opt-in tests behind build tags and explicit environment variables (`HERMES_IT_ALLOW_REAL_AWS=1`, …) | Real behaviour in the operator's own test account | — |

moto was chosen over LocalStack: the LocalStack community repository was archived on 2026-03-23, and its image
now needs an auth token [§8].

## Consequences

- Release notes and `docs/limitations.md` state, for each provider, which tests have run. No document claims a
  successful real-cloud deployment unless one has actually been performed and recorded.
