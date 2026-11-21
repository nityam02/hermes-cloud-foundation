# Quickstart

From an empty directory to a rendered, reviewable root module in five commands, with no cloud credentials. Then
we look at what `plan` and `apply` will do, and the gates that stop them.

Every output block below was captured on 2026-11-19 with a `dev` build of `hermes` (paths shortened to `<dir>`).
Nothing in this tutorial contacts a cloud. The `plan` refusals shown in step 7 happen before any engine runs.

Prerequisites: [installation](installation.md) (Go and OpenTofu ≥ 1.10). If your machine holds real cloud
credentials, use the [credential-isolation prefix](installation.md#working-without-touching-real-clouds).

## 1. Create a spec

```bash
mkdir demo && cd demo
hermes init --name demo --provider aws --account 123456789012
```

```text
Wrote hermes.yaml (template basic, provider aws).
Added .hermes/ to .gitignore.
  warning HCF207 at extensions.aws.acm_certificate_arn: before deploying this environment: public services in protected environment "production" require extensions.aws.acm_certificate_arn (HTTPS)
Next: replace REPLACE-ME values, then `hermes validate`, `hermes state bootstrap`, `hermes plan`.
```

The generated `hermes.yaml` (templates: `basic`, `containerized`, `database`, `autonomous-logistics`):

```yaml
apiVersion: hermes/v1alpha1
kind: Platform

project:
  name: demo
  environment: dev

cloud:
  provider: aws
  region: eu-north-1
  account: "123456789012"

state:
  bucket: demo-hermes-state   # create it with `hermes state bootstrap`

workloads:
  compute:
    services:
      - name: web
        image: ghcr.io/example/web:1.0.0   # REPLACE-ME: your image, pinned tag or @sha256 digest
        public: true
  observability:
    enabled: true

environments:
  dev: {}
  production: {}
```

`production` is protected by default (`project.protected_environments` defaults to `[production]`).

## 2. Validate

```bash
hermes validate
```

```text
environment dev (aws): valid
environment production (aws, protected): INVALID
  error HCF207 at extensions.aws.acm_certificate_arn: public services in protected environment "production" require extensions.aws.acm_certificate_arn (HTTPS)
```

Exit code 2. Without `--env`, every declared environment is validated. Protected environments are stricter:
HTTPS is mandatory for public services, images must be pinned, state must be remote, and so on (see
[diagnostics](../reference/diagnostics.md)). For this tutorial, we work on `dev`:

```bash
hermes validate --env dev        # environment dev (aws): valid   (exit 0)
```

To make `production` valid, set `extensions.aws.acm_certificate_arn` to a certificate in the same region.

## 3. Measure portability

```bash
hermes portability
```

```text
Portability of demo/dev (7 requested features)
PROVIDER  SCORE  IMPLEMENTED  PARTIAL  NOT-IMPLEMENTED  NOTES
alibaba   71.4   4            2        1
aws *     100.0  7            0        0
gcp       100.0  7            0        0                1 sizing/extension finding(s)

Differences that matter:
  compute.otel_sidecar
    alibaba  not-implemented  no collector sidecar
    aws      implemented      AWS Distro for OpenTelemetry collector sidecar per task
    gcp      implemented      OpenTelemetry Collector sidecar container exporting to Cloud Trace/Monitoring
  identity.per_service
    alibaba  partial          a RAM role is created per service, trusted by the SAE service principal (or extensions.alibaba.workload_role_trusted_principals, never the account root); the provider cannot attach it to the SAE application (manual step)
    ...
  [gcp] warning HCF202 at workloads.compute.services[0]: Cloud Run sizing: service web requests 0.5 vCPU / 1024 MiB and will run with 1 vCPU / 1024 MiB ...

* = selected provider. score = 100 * sum(weight(status of each requested feature)) / count(requested features); implemented = 1, partial = 0.5, not-implemented = 0. ...
Note: statuses describe the Hermes OpenTofu modules as designed and mock-tested; none is verified against a real cloud account.
```

Each feature the spec relies on is scored against each provider's capability matrix
([comparison matrix](../providers/comparison-matrix.md)). The score measures Hermes' modules, not the clouds
([ADR 0015](../decisions/0015-measurable-portability.md)).

## 4. See the normalized spec

`render --print` shows the spec after the overlay and every default are applied, which is exactly what the
renderer uses (excerpt):

```yaml
environment: dev
protected: false
state:
  backend: s3
  bucket: demo-hermes-state
  prefix: hermes
  region: eu-north-1
network:
  cidr: 10.20.0.0/16
  zones: 2
  nat: true
workloads:
  compute:
    services:
      - name: web
        port: 8080
        cpu: 0.5
        memory_mb: 1024
        min_instances: 1
        max_instances: 3
        health_check_path: /healthz
  observability:
    enabled: true
    log_retention_days: 30
    tracing: true
```

## 5. Render the root module

```bash
hermes render
```

```text
Rendered <dir>/.hermes/build/dev/main.tf.json (dev, aws, engine tofu)
  digest  3796b5c211ff71423973c141e90f0b59ae9ff800cfe80ae901eeb8447e970e35
  modules <dir>/.hermes/modules/dev (f1ad7c96005b)
  state   s3://demo-hermes-state/hermes/demo/dev/aws/terraform.tfstate
```

`.hermes/build/dev/main.tf.json` is an ordinary OpenTofu root module in JSON syntax. It contains:

- `terraform.required_providers` with **exact** pins (`hashicorp/aws` 6.73.0, `hashicorp/random` 3.9.1) and
  `required_version >= 1.10.0`;
- the S3 backend (`key = hermes/demo/dev/aws/terraform.tfstate`, `encrypt = true`, `use_lockfile = true`);
- the `aws` provider block with `allowed_account_ids = ["123456789012"]` and the `hermes-*` default tags;
- one `module "platform"` call to the AWS composition, extracted from the binary into `.hermes/modules/<version>/`,
  with every contract variable filled in;
- the three contract outputs: `bindings`, `service_environment` and `secret_environment`.

The digest identifies exactly what a saved plan was made from. You can read the file, but never edit it: change the
spec and re-render.

## 6. Check where state lives

```bash
hermes state show
hermes status
```

```text
environment dev
  backend:    s3
  location:   s3://demo-hermes-state/hermes/demo/dev/aws/terraform.tfstate
  locking:    S3 native lockfile (use_lockfile)
  encryption: backend server-side encryption

environment dev: aws eu-north-1
  state: s3://demo-hermes-state/hermes/demo/dev/aws/terraform.tfstate
  last plan: none
  note: root module not initialized; run `hermes plan`
```

The bucket does not exist yet. `hermes state bootstrap` plans it, and `hermes state bootstrap --apply --approve
<prefix>` creates it. That is a real-cloud action, covered in
[first production environment](first-production-environment.md).

## 7. What `plan` will do (and the checks that run first)

`hermes plan` is the first command that talks to the cloud with **your** credentials. In order, it:

1. loads and validates the spec for the environment (the same rules as `validate`);
2. runs the provider's **plan checks**: account/project present (protected environments, GCP), state bucket set
   for remote backends, Tablestore locking for protected Alibaba environments;
3. detects the engine (`tofu`, then `terraform`, at or above the minimum version);
4. renders the root module (as in step 5);
5. runs `init` (backend configured) → `plan -out plan.tfplan` → `show -json`;
6. classifies every change (create, update, replace, delete, import);
7. evaluates the plan policies with conftest (`policies/plan`, embedded);
8. saves `plan.tfplan`, `plan.json` and `meta.json` under `.hermes/plans/dev/`. The `plan_id` is the SHA-256 of
   the plan file.

Steps 1–3 fail fast and offline. These refusals are real, captured with no engine on `PATH`:

```text
$ hermes plan -c nobucket.yaml          # the same spec without a state block
error HCF212 at state.bucket: state.bucket is required to plan with the s3 backend (hint: run `hermes state bootstrap` to create one, then set state.bucket)
1 error(s), 0 warning(s)
Error: environment dev cannot be planned                                  (exit 2)

$ hermes plan --env production
error HCF207 at extensions.aws.acm_certificate_arn: public services in protected environment "production" require extensions.aws.acm_certificate_arn (HTTPS)
Error: the spec hermes.yaml is invalid for environment production         (exit 2)

$ hermes plan                            # valid, but no OpenTofu/Terraform installed
Error: no infrastructure engine found: install OpenTofu (https://opentofu.org/docs/intro/install/) or Terraform   (exit 1)
```

A successful plan prints this shape (from `internal/cli/plan.go`; not captured, because no real plan has been
run):

```text
apply plan for dev (aws)
  <n> to create, <n> to update, <n> to replace, <n> to delete, <n> to import
  policy: passed (<n> warning(s))
  plan id: <64 hex characters>
  saved:   <dir>/.hermes/plans/dev
Next: hermes apply --env dev
```

`--detailed-exitcode` makes `plan` exit 3 when there are changes. A failed policy saves the plan but marks it
unappliable (exit 2).

## 8. What `apply` will do, and how it refuses

`apply` takes **no plan of its own**. It applies the saved plan or refuses. With no plan:

```text
$ hermes apply
Refused: HCF501: no saved plan in <dir>/.hermes/plans/dev (run `hermes plan` first)          (exit 2)
```

With a saved plan, it checks every gate in this order (full state machine in the
[detailed design](../architecture/detailed-design.md#3-safety-gate-state-machine)):

| Gate | Refusal |
|---|---|
| Plan kind and environment match | `HCF511` |
| Not already applied | `HCF510` |
| Spec and modules unchanged since planning (re-rendered digest) | `HCF502` stale |
| Younger than `--max-plan-age` (24 h) | `HCF503` |
| Plan file hash equals `plan_id` | `HCF504` tampered |
| Policy passed | `HCF508` |
| No delete/replace, or `--allow-destructive` | `HCF505` |
| Protected environment: `--approve <first ≥ 8 chars of plan_id>` | `HCF506` |
| Non-protected: `y` at the prompt, or `--yes` | `HCF509` |

Every apply and refusal is appended to `.hermes/history/<env>.jsonl`, for example:

```json
{"time":"2026-11-19T18:31:00Z","environment":"dev","action":"plan","result":"refused","reason":"environment dev cannot be planned","user":"alice (alice)"}
```

After a successful apply, `hermes bindings --env dev -o json` prints the non-secret connection metadata, and
`hermes bindings -o env --service web` prints the `HERMES_*` variables for running the service locally
([application portability](../architecture/application-portability.md)).

## Next steps

- Deploy the same spec to three clouds: [multi-cloud tutorial](multi-cloud.md).
- Prepare a real environment: [first production environment](first-production-environment.md).
- If something fails: [troubleshooting](../operations/troubleshooting.md).
