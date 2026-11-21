# ADR 0003 — Capability contracts with per-provider compositions

- Status: Accepted (2026-11-18)

## Context

"Define once, deploy anywhere" fails in two well-known ways. One is a fictional universal cloud API that hides
real differences until production. The other is a lowest-common-denominator abstraction that removes useful
native capabilities.

## Decision

1. The spec (`kind: Platform`, `docs/architecture/data-contracts.md` §1) describes **capabilities**: network,
   containerized services, PostgreSQL, buckets, topics with subscriptions, secrets, and observability. It never
   describes provider resource types.
2. Each provider implements the capabilities in a **composition module**, `infra/providers/<provider>/platform`,
   built from native resources in `infra/modules/<provider>/*`. Every composition declares the identical variable
   contract (`infra/contract/variables.tf`, copied verbatim) and identical outputs (`infra/contract/outputs.md`).
3. Contract tests (`tests/contracts`) run the same fixtures and assertions against every composition with mock
   providers. They also verify that the variable files and the canonical test section are identical across
   providers.
4. Provider-specific needs go in `extensions.<provider>`, which only that provider adapter reads and validates.
5. Where providers differ materially, the difference is surfaced rather than hidden. Examples: Pub/Sub retention
   limits, Cloud Run fractional-CPU rules, and the lack of IAM database authentication on Alibaba RDS.
   Differences are reported through validation warnings and the capability matrix (ADR 0015). Portable bounds are
   chosen deliberately: for example, the 7-day maximum message retention reflects Pub/Sub subscription and MNS
   queue limits.

## Alternatives considered

- **Generic "resource" spec mirroring provider resources**: this is not portable, and it duplicates HCL.
- **Pure lowest-common-denominator**: this would remove HA, PITR, CMK and scale-to-zero where a provider supports
  them. Instead, features exist in the contract when at least two providers implement them well, and the matrix
  records the rest.

## Consequences

- Adding a provider means implementing one composition, passing the contract tests, and adding a Go adapter with
  its capability matrix (see `docs/providers/implementing-a-provider.md`).
- The contract evolves only through versioned spec changes (`apiVersion`).
