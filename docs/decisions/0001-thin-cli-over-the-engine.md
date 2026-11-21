# ADR 0001 — A thin CLI over the IaC engine

- Status: Accepted (2026-11-18)
- Deciders: platform architecture

## Context

Hermes must let a team describe infrastructure once and deploy it to AWS, GCP or Alibaba Cloud, and must help
migrate between clouds. Mature tools already solve parts of this:

- **Terragrunt** (MIT) and **Cloud Posse Atmos** (Apache-2.0) orchestrate OpenTofu/Terraform: DRY backends,
  environment layering, stacks, workflows [§3].
- **Crossplane** (Apache-2.0) turns cloud APIs into Kubernetes resources reconciled by a control plane [§4].
- **Score**, **Radius** and **KubeVela** describe workloads independently of the platform [§4].

The project brief explicitly warns against building "a superficial wrapper around Terraform".

## Decision

Hermes is a **thin Go CLI** that owns only what no existing tool provides together:

1. a cloud-independent, versioned capability spec (`kind: Platform`) with schema and semantic validation;
2. provider selection and rendering of a plain OpenTofu/Terraform root module that calls the right provider
   composition;
3. engine-neutral safety gates on the saved plan (ADR 0007);
4. measurable portability (ADR 0015);
5. a Terraform-independent migration toolkit (ADR 0009, 0010, 0014).

All planning, applying, state, locking and import work is delegated to the engine binary. Hermes does **not**
depend on Atmos, Terragrunt or Crossplane.

## Alternatives considered

| Option | Why not chosen |
|---|---|
| **Atmos as the foundation** | Atmos is cloud-neutral, but it would add a second configuration model (stacks, inheritance, component vendoring) on top of the Hermes spec, so developers would learn two layers. Its component library is AWS-only (183 `aws-*` repos, no GCP or Alibaba components) [§3], so the provider modules would still have to be written. It has no capability-level spec, no provider selection, and no migration workflow. |
| **Terragrunt as the foundation** | Terragrunt removes backend/provider duplication, which Hermes's renderer already does for its generated roots. Its backend bootstrap supports S3, GCS and (experimentally) azurerm, but **not OSS** [§3], leaving Alibaba unsolved. It adds a second HCL dialect and does not address portability or migration. |
| **Crossplane** | It requires a Kubernetes control plane for every adopter, which is disproportionate for a small team. Its Alibaba provider is young (≈27 API groups, ≈5 regular contributors) [§4], and official provider builds published after 2025-03-25 require a subscription [§4]. It is a different execution model (continuous reconciliation instead of reviewed plans), which conflicts with the gated-plan safety model. |
| **Score / Radius / KubeVela** | They are workload-centric and Kubernetes- or Compose-targeted, with no first-party managed-service targets on all three clouds [§4]. Score is a candidate future *import/export format* for the `services` section. |
| **A full custom engine** | Reimplementing plan/apply/state is unjustifiable; OpenTofu and Terraform are mature. |

## Consequences

- **Positive:** A small surface area and a single binary. The modules are plain HCL, so an organisation already
  using Atmos or Terragrunt can call `infra/providers/<provider>/platform` directly (expected to work through the
  standard module interface; not yet tested — see `docs/limitations.md`).
- **Negative:** Hermes implements its own environment overlay (a deliberately small deep-merge), and it does not
  offer Atmos-style multi-component orchestration. Large estates with many independent stacks may still want
  Atmos or Terragrunt around Hermes-generated roots.
- **Guardrail:** Any new CLI feature must add something the engine does not (CLAUDE.md rule 7). If Hermes ever
  needs continuous reconciliation, revisit Crossplane rather than building a reconciler.
