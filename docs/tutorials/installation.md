# Installation

Hermes is one Go binary. The modules, schemas and policies are embedded in it. It needs an IaC engine to plan
and apply, and it optionally uses scanners and cost tools. Nothing here needs cloud credentials, except
`plan`/`apply` against a real account, which this project has never done ([limitations](../limitations.md)).

## Required

| Tool | Minimum | Tested with | Why this minimum |
|---|---|---|---|
| Go | 1.26 (`go.mod`) | 1.27.4 | Builds the CLI |
| OpenTofu (default engine) | 1.10 | 1.13.9 | S3 native state locking (`use_lockfile`) needs 1.10. `hermes doctor` warns below 1.12, the oldest line still receiving security fixes ([research §10 C1](../research/open-source-landscape.md#10-where-the-evidence-contradicts-or-strains-the-current-architecture)) |
| **or** Terraform | 1.11 | 1.15.6 | `use_lockfile` is GA from 1.11. Client-side state encryption is OpenTofu-only |

The engine is chosen with `engine.binary: auto | tofu | terraform` in the spec. `auto` prefers `tofu`.

## Optional

| Tool | Tested with | Used by | Without it |
|---|---|---|---|
| conftest | 0.71.1 | `hermes plan` policy check, `make policy-test` | warning in non-protected environments; **plan refused in protected environments** unless `--skip-policy` (recorded) |
| infracost | not run | `hermes cost` (AWS and GCP only) | `cost` explains what is missing; it never invents numbers |
| tflint | 0.66 | `make security`, CI | skipped |
| trivy | 0.76.2 | `make security`, CI | skipped. **Never use v0.69.4** (the compromised release) |
| checkov | not run in this project | `make security` | skipped |
| gitleaks | 8.30 | `make security` | skipped |
| golangci-lint | 2.15 | `make lint` | skipped with a note |
| lychee | 0.24 | `make docs-links` | — |
| moto (Python) | 5.2.5 (CI pin) | `make demo-discovery` (local AWS emulation) | demo unavailable |
| GNU Make | **≥ 4.3** | all `make` targets | macOS ships 3.81, which fails to parse the `Makefile` (see below) |

Hermes does **not** use the `aws`, `gcloud` or `aliyun` CLIs. `hermes doctor` only reports whether they exist.

## Install the engine with checksum verification

Download release binaries from the vendor and verify the published checksums before you run them. Do not pipe an
install script into a shell.

```bash
# OpenTofu: every release has a SHA256SUMS file (and cosign signatures).
V=1.13.9; OS=linux; ARCH=amd64          # darwin/arm64 etc.
curl -fsSLO "https://github.com/opentofu/opentofu/releases/download/v${V}/tofu_${V}_${OS}_${ARCH}.zip"
curl -fsSLO "https://github.com/opentofu/opentofu/releases/download/v${V}/tofu_${V}_SHA256SUMS"
grep " tofu_${V}_${OS}_${ARCH}.zip\$" "tofu_${V}_SHA256SUMS" | sha256sum --check --strict   # macOS: shasum -a 256 -c
unzip "tofu_${V}_${OS}_${ARCH}.zip" tofu && install -m 0755 tofu ~/.local/bin/tofu

# Terraform: releases.hashicorp.com publishes terraform_<v>_SHA256SUMS (GPG-signed by HashiCorp).
```

Verify the signatures too (cosign for OpenTofu, GPG for Terraform) as described in each vendor's installation
documentation. For the security tools, [`scripts/ci/install-tools.sh`](../../scripts/ci/install-tools.sh) is a
working example: version **and** SHA-256 pinned, so a moved tag or a replaced asset cannot change what runs. It
pins Linux x86_64 only.

## Build Hermes

```bash
git clone <this repository> hermes-cloud-foundation && cd hermes-cloud-foundation
go build -trimpath -o bin/hermes ./cmd/hermes      # works everywhere
# or, with GNU Make >= 4.3:
make build                                          # adds version/commit/date via -ldflags
./bin/hermes version
```

`hermes version` prints the embedded modules digest and the exact provider pins. A build without `-ldflags`
reports version `dev`:

```text
hermes dev (commit none, built unknown)
embedded modules f1ad7c96005b
engines: OpenTofu >= 1.10.0, Terraform >= 1.11.0
  aliyun/alicloud 2.2.0
  hashicorp/aws 6.73.0
  hashicorp/google 8.12.0
  hashicorp/google-beta 8.12.0
  hashicorp/random 3.9.1
```

> **macOS and `make`:** `/usr/bin/make` is GNU Make 3.81. Line 32 of the `Makefile` has a `#` inside
> `$(shell …)`, which Make < 4.3 reads as a comment start, so every target fails with
> `unterminated call to function 'shell'`. Use `go build` as shown above, or install GNU Make ≥ 4.3 and run it as
> `gmake`. This is tracked in the [limitations register](../limitations.md#1-cli-and-platform).

## Shared provider plugin cache

Every module directory would otherwise download its own copy of each provider (hundreds of MB each). Use one
cache:

```bash
export TF_PLUGIN_CACHE_DIR="$HOME/.cache/hermes-tf-plugins"
mkdir -p "$TF_PLUGIN_CACHE_DIR"
```

The `Makefile` sets this by default. `hermes doctor` warns when less than 5 GiB is free.

## Check the installation

```bash
hermes doctor
```

Example output (paths shortened; captured 2026-11-19):

```text
STATUS  CATEGORY     CHECK       DETAIL
ok      engine       tofu        tofu 1.13.9 at /usr/local/bin/tofu
ok      tool         conftest    found at /usr/local/bin/conftest
info    tool         checkov     not found (optional: IaC security scanning)
ok      tool         tflint      found at /usr/local/bin/tflint
ok      tool         trivy       found at /usr/local/bin/trivy
info    tool         infracost   not found (optional: `hermes cost` estimates (AWS and GCP only))
info    credentials  aws         credential sources present (values not read): ~/.aws/credentials, ~/.aws/config
info    config       spec        hermes.yaml not found (run `hermes init`)
ok      workspace    .gitignore  .hermes/ is ignored
ok      workspace    disk        6.8 GiB free
```

`doctor` never calls a cloud API and never reads credential values. It reports only environment-variable names
and whether well-known credential files exist. It exits 2 when a required tool is missing or the spec is
invalid.

## Working without touching real clouds

If your machine holds real credentials, run engine and test commands with credential isolation, as the
`Makefile` does:

```bash
env AWS_CONFIG_FILE=/dev/null AWS_SHARED_CREDENTIALS_FILE=/dev/null AWS_PROFILE= \
    AWS_EC2_METADATA_DISABLED=true GOOGLE_APPLICATION_CREDENTIALS=/dev/null CLOUDSDK_CONFIG=/dev/null \
    ALIBABA_CLOUD_CREDENTIALS_FILE=/dev/null  <command>
```

`validate`, `portability`, `render`, `providers`, `doctor`, `init`, `state show` and every `migrate` sub-command
except `validate` with network checks and `cutover --execute` work fully offline.

Next: the [quickstart](quickstart.md).
