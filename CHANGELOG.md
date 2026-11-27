# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). The platform specification is versioned separately
through `apiVersion` (currently `hermes/v1alpha1`).

## [Unreleased]

### Added

- Hermes UI, connect without a terminal: sign in to AWS (`aws login` / `aws sso login`) and Google Cloud (`gcloud auth application-default login`) from the page, install the Google Cloud CLI with Homebrew, create, update or remove the read-only role stack with one button, pick a Google project from a list, turn on the required Google APIs, and have checks run by themselves. Both clouds are shown side by side; the connection is remembered in `.hermes/ui/connection.json` (no credentials). `hermes ui` now opens the browser by default. `make ui-dev` serves the UI over pretend clouds. A system diagram is in `docs/architecture/hermes-system.svg`.
- `hermes ui`: a local React UI that connects AWS (read-only role with a generated external ID) and Google Cloud (Application Default Credentials), checks both with plain-language fixes, runs discovery, assessment and planning as a job, and shows the results. Loopback only, token and cookie, CSRF-protected posts, no outside requests, no access keys. See `docs/tutorials/ui.md` and ADR 0016.
- The cloud-independent platform specification `hermes/v1alpha1` with a JSON Schema and semantic validation.
- The `hermes` CLI, with these commands: `init`, `doctor`, `validate`, `portability`, `render`, `plan`, `apply`,
  `status`, `bindings`, `destroy`, `state bootstrap`, `providers`, `cost` and `version`.
- Provider compositions for AWS (ECS Fargate, RDS PostgreSQL, S3, SNS+SQS, Secrets Manager, CloudWatch), GCP
  (Cloud Run, Cloud SQL, GCS, Pub/Sub, Secret Manager, Cloud Monitoring) and Alibaba Cloud (SAE, ApsaraDB RDS
  PostgreSQL, OSS, MNS, KMS Secrets Manager, SLS). The compositions are tested with mock providers only; see
  `docs/limitations.md`.
- Agentless, read-only AWS discovery (`hermes inventory aws`) through a cross-account role with an ExternalId.
  A read-only guard enforces read-only access, and the inventory records a dependency graph and permission gaps.
- Migration toolkit: `hermes migrate catalog|assess|generate|plan|validate|cutover` and `hermes adopt aws`
  (import-only IaC adoption).
- Service catalog (AWS to GCP and Alibaba), migration workflows, pricing assumptions, and runbooks.
- Conftest policies, scanner configuration, and GitHub Actions workflows with OIDC.
- Architecture documentation, ADRs 0001–0015, and provider, migration, security and operations guides.
