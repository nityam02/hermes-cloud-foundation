# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). The platform specification is versioned separately
through `apiVersion` (currently `hermes/v1alpha1`).

## [Unreleased]

### Added

- `hermes ui`: a local, read-only React UI for inventories, assessments and migration plans (loopback only, Host-checked, no outside requests). See `docs/tutorials/ui.md`.
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
