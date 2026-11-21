# ADR 0005 — Serverless containers as the default compute; Kubernetes reserved

- Status: Accepted (2026-11-18)

## Context

`workloads.compute.type: containerized` must map to a container runtime on each cloud. The candidates are
managed Kubernetes (EKS, GKE, ACK) or serverless containers (ECS on Fargate, Cloud Run, Serverless App Engine).
The project brief asks not to create "an unnecessarily large Kubernetes cluster … just to demonstrate
architectural sophistication".

## Decision

- `containerized` maps to **ECS on Fargate** (AWS), **Cloud Run** (GCP) and **Serverless App Engine** (Alibaba).
  Services run as OCI images with per-service identities, autoscaling bounds and health checks.
- `kubernetes` is **reserved** in v1alpha1. It fails validation with "not implemented" rather than half-working.
- Each provider adapter validates the CPU/memory combinations its runtime accepts, and warns when it must round up.

## Rationale

There is no control plane to operate or pay for. Small teams get production-grade scaling and patching. OCI
images keep the workload portable to Kubernetes later. Kubernetes adds value for large estates (operators,
service mesh, GitOps with Argo CD), so it is the natural v1beta1 extension, implemented as a separate capability.

## Consequences

- The runtimes differ, and the differences are recorded in the capability matrix: scale-to-zero (Cloud Run
  native; ECS min 0 stops the service), built-in HTTPS endpoints (Cloud Run yes; ECS needs an ALB with a
  certificate; SAE needs an SLB with a certificate), and sidecar support.
- Workloads that need DaemonSets, custom operators or GPUs are out of scope until the Kubernetes capability exists.
