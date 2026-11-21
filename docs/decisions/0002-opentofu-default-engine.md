# ADR 0002 — OpenTofu as the default engine; Terraform supported

- Status: Accepted (2026-11-18)

## Context

OpenTofu and Terraform share HCL and the provider protocol, but have diverged since Terraform moved to the
Business Source License (BUSL-1.1) in 2023 [§2]. Hermes is an open-source project intended for unrelated
organisations, some of which have standardised on Terraform.

Evidence [§2]:

- OpenTofu is MPL-2.0, a Linux Foundation project, CNCF Sandbox since 2025-04-23, and published 35 stable
  releases in the 12 months to November 2026. The current line is 1.13 (1.13.9 on 2026-11-05).
- Only OpenTofu offers **client-side state encryption** (key providers include `aws_kms` and `gcp_kms`; there is
  none for Alibaba KMS).
- Both engines support `mock_provider` tests, `import` blocks, `-generate-config-out` (experimental), S3 native
  locking (OpenTofu ≥ 1.10, Terraform ≥ 1.11), and write-only arguments (≥ 1.11).
- Terraform ≥ 1.14 has `query`/`list` bulk discovery; OpenTofu does not (issue #3787).
- OpenTofu 1.8 lost security support on 2025-12-09; lines before 1.12 are out of support or nearly so.

## Decision

- The default engine is **OpenTofu** (`engine.binary: auto` prefers `tofu`, falls back to `terraform`).
- **Terraform is supported**: provider modules use only features common to both engines. Engine-specific
  features appear only in the rendered root, and only when that engine is selected. The one such feature today
  is OpenTofu state encryption.
- Minimum versions: **OpenTofu ≥ 1.10, Terraform ≥ 1.11** (set by S3 native locking). `hermes doctor` warns when
  the OpenTofu line no longer receives security fixes (currently anything older than 1.12).
- CI validates modules with OpenTofu 1.13 and the provider compositions additionally with Terraform.
- Hermes does not use `terraform query`; it generates `import` blocks from its own inventory (ADR 0009), which
  works with both engines.

## Alternatives considered

- **Terraform only**: rejected. The BUSL license is a concern for some adopters, and it would lose client-side
  state encryption.
- **OpenTofu only**: rejected. It would needlessly exclude organisations standardised on Terraform; the
  compatible subset is large.

## Consequences

- Modules avoid OpenTofu-only syntax (`enabled` meta-argument, dynamic `prevent_destroy`) and Terraform-only
  syntax (variables in `required_providers`, native policies).
- Client-side state encryption is unavailable when the engine is Terraform, and unavailable on Alibaba with
  either engine. Validation reports both cases explicitly.
