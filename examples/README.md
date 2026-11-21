# Examples

Every example validates and renders **offline**. No example has been deployed to a real cloud. Account IDs,
project IDs, bucket names, certificates and hosts are placeholders (`123456789012`, `example.com`, …).

Run the commands from the repository root with a built CLI (`go build -o bin/hermes ./cmd/hermes`, with
`bin/` on `PATH`). `validate` and `render` need no credentials. `plan` and `apply` would use your cloud
credentials, and need real account values and an existing state bucket first
([first production environment](../docs/tutorials/first-production-environment.md)).

## Platform specs

| Example | Provider | Environments | Demonstrates | `hermes validate` result (2026-11-19) |
|---|---|---|---|---|
| [basic](basic/) | GCP | `production` (protected) | The minimal spec from data-contracts §1, verbatim: one service, an HA PostgreSQL database, a bucket, a topic, secrets and observability, all with defaults | valid, with warnings: `HCF102` (no `apiVersion`), `HCF113` (no `cloud.account`), `HCF116` (no `state.bucket`). Plan would refuse until the last two are set |
| [containerized](containerized/) | AWS | `dev`, `production` | Two ECS Fargate services: a public `api` and a private `worker`; least-privilege `access` grants; `secret_env` references; an HTTPS certificate for production | valid (both environments) |
| [database](database/) | Alibaba | `dev`, `production` | A private service with database access; PostgreSQL 16 with PostGIS and PITR; HA and deletion protection in production | valid, with `HCF210` warnings: no OTel sidecar, no DB IAM auth (password), PITR only on the HA Edition (dev) |
| [multi-cloud](multi-cloud/) | AWS, GCP, Alibaba | `awsdev`, `gcpdev`, `alidev` | One spec on three clouds, overriding only `cloud.*` and `state.bucket`; isolated state per environment and provider; the same binding variables everywhere | valid (all three); Alibaba `HCF210` warnings for the sidecar, DB IAM auth, secret injection and PITR |
| [autonomous-logistics](autonomous-logistics/) | AWS | `dev`, `production` | The cloud side of the [reference architecture](../docs/architecture/autonomous-logistics-reference-architecture.md): nine services, PostgreSQL with PostGIS, three buckets, four topics. Safety-critical functions stay outside the cloud by design | valid (both environments) |

Try them:

```bash
hermes validate    -c examples/containerized/hermes.yaml                 # every environment
hermes portability -c examples/database/hermes.yaml                      # compare Alibaba with AWS and GCP
hermes render      -c examples/containerized/hermes.yaml --env production
hermes render      -c examples/basic/hermes.yaml --print                 # normalized spec, every default filled in
hermes init --name demo --template autonomous-logistics --provider gcp   # start your own from a template
```

The [multi-cloud tutorial](../docs/tutorials/multi-cloud.md) walks through `multi-cloud/` with real output. The
rendered roots of every example environment are golden-tested (`internal/render/testdata/golden/`).
`make integration` runs `tofu init -backend=false` and `tofu validate` on them.

## Migration toolkit ([migration/](migration/))

| File | What it is | How to use it |
|---|---|---|
| [sample-inventory.json](migration/sample-inventory.json) | A generic, hand-built AWS estate as an `Inventory`: 62 resources, 86 relationships, 4 discovery gaps | Input for `migrate assess`; also used by the unit tests |
| [checks.yaml](migration/checks.yaml) | `ValidationChecks`: spec coverage, bindings, HTTP health, TCP, DNS and a command check | `hermes migrate validate`. **The `http`, `tcp` and `dns` checks contact the hosts named in the file.** For an offline run, keep only the `spec-coverage` check |
| [cutover-hooks.yaml](migration/cutover-hooks.yaml) | `CutoverHooks`: argv to run (and roll back) for a plan step | `hermes migrate cutover --hooks …`. The scripts it names are illustrative and not included |
| [aws-discovery-demo/](migration/aws-discovery-demo/) | End-to-end discovery against a **local moto server** (fake credentials; no real AWS is reachable) | `make demo-discovery`, or see its [README](migration/aws-discovery-demo/README.md). Needs a Python venv with moto (~180 MB) |

Offline pipeline on the sample (real output, 2026-11-20; every command writes its document with `--out`):

```bash
hermes migrate assess   --inventory examples/migration/sample-inventory.json --target gcp --out assessment.json --markdown assessment.md
#  Assessed 62 resource(s) for gcp: portability score 31.0%, 1 blocker(s), 7 risk(s), 4 discovery gap(s), 6 wave(s).
hermes migrate generate --assessment assessment.json --out target
#  Wrote target/hermes.yaml, target/MAPPING.md, target/UNSUPPORTED.md (DRAFT — requires human review).
#  37 resource(s) mapped, 25 listed in UNSUPPORTED.md.
hermes migrate plan     --assessment assessment.json --out migration-plan.yaml --markdown plan.md
#  Plan ebd525afd5cd: 218 step(s) in 8 phase(s); 132 approval gate(s), 20 validation gate(s), 61 step(s) touch the source.
hermes migrate validate --plan migration-plan.yaml --checks <offline-checks.yaml> --out validation-report.json
#  PASS  spec-coverage  target-spec-coverage  all 28 in-scope source resources are covered
hermes migrate cutover  --plan migration-plan.yaml --report validation-report.json   # dry-run: gates and runbook, nothing executed
hermes validate -c target/hermes.yaml                                                # the draft spec is a normal Platform spec
```

With `--target alibaba`, the same sample scores 39.3%. Command reference:
[migration commands](../docs/migration/migration-commands.md).
