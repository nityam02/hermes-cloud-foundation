# Plan policies

Rego policies evaluated by [Conftest](https://www.conftest.dev) against the JSON form of a saved
plan (`tofu show -json plan.tfplan`, identical for Terraform). `hermes plan` runs them
automatically (embedded in the binary from `policies/plan/`); you can also run them by hand:

```bash
tofu show -json plan.tfplan > plan.json
conftest test --policy policies/plan plan.json                      # defaults
conftest test --policy policies/plan --data policy-data.json plan.json
make policy-test                                                     # unit tests (see below)
```

Status: implemented & verified with synthetic plan fixtures and with plans derived from the real
provider compositions (`conftest verify`; see [Tests](#tests)). The composition-derived plans
come from mock providers, so the rules have not been evaluated against plans of real cloud
deployments.

## Rules

| Id | Kind | What it checks |
|---|---|---|
| HCF-POL-000 | deny/warn | Input is a plan (not state or arbitrary JSON) and did not error; invalid or expired exceptions (warn) |
| HCF-POL-001 | deny/warn | No public object storage: S3 public access block flags, public ACLs/grants, public bucket policies; GCS public access prevention, uniform access, allUsers/allAuthenticatedUsers on data resources; OSS ACL, public access block, public bucket policies. Warn when a bucket has no public access block/ACL resource |
| HCF-POL-002 | deny/warn | Encryption at rest and TLS where visible: RDS/Aurora, EBS/EFS, SQS, SNS, ElastiCache, Kinesis, `rds.force_ssl = 0`, Cloud SQL `ssl_mode`, ApsaraDB `ssl_action`, OSS server-side encryption, Alibaba disks |
| HCF-POL-003 | deny | No public databases: RDS `publicly_accessible`, Cloud SQL public IP / `0.0.0.0/0` networks, ApsaraDB `security_ips` with `0.0.0.0/0`, Alibaba public connection strings |
| HCF-POL-004 | deny | No ingress from `0.0.0.0/0` or `::/0`, except TCP 80/443 on resources marked `hermes-component = compute` (tag) or, for rule types without tags, `hermes-component=compute` in the description |
| HCF-POL-005 | deny/warn | Identity policies: no `Action "*"`, no Allow+NotAction, no data-plane actions on `Resource "*"`, no `iam:PassRole` on `*`, no `iam:*`/`ram:*`; GCP basic roles owner/editor (deny) and viewer (warn); unknown policy documents (warn) |
| HCF-POL-006 | deny/warn | Deletion protection on databases in protected environments; warn when the environment cannot be determined |
| HCF-POL-007 | warn | Delete or replace of stateful resources (databases, buckets, keys, secrets, queues) |
| HCF-POL-008 | deny/warn | Tags/labels `hermes-managed-by = hermes` and `hermes-environment` (deny), `hermes-project` and `hermes-component` (warn) on taggable resources |
| HCF-POL-009 | deny | No secret material in IaC: secret versions, SecureString parameters, non-placeholder `alicloud_kms_secret` data, `random_password`, `tls_private_key`, access keys, plaintext database passwords (write-only `*_wo` arguments are fine) |
| HCF-POL-010 | deny | Trust policies: no public principals without conditions; web-identity (OIDC) trust must pin an exact `sub`; workload identity pool providers need an `attribute_condition`; no whole-pool `principalSet` bindings |
| HCF-POL-011 | deny/warn | Protected environments keep database backups, PITR and final snapshots; warn when bucket versioning is off |

Messages have the form `HCF-POL-nnn: <resource address>: <explanation>` and never include
attribute values that could be secret.

### What the rules cannot see

- Values unknown until apply (for example a policy document built from the ARN of a resource
  created in the same plan) are not evaluated. HCF-POL-005 and HCF-POL-001 warn about them;
  nothing unknown is treated as compliant silently, and nothing unknown causes a deny.
- Customer-managed key usage is not enforced: a key created in the same plan is unknown.
- Resource policies (bucket, key, queue policies) are only checked for public principals.
- Static scanners (Checkov, Trivy, TFLint) complement these rules on the HCL. For Alibaba Cloud
  these rules are the only automated guardrail: TFLint has no ruleset and Trivy no checks for it.

## Data (`--data`)

Conftest does not load data files from the policy directory, so defaults live in
`policies/plan/lib.rego` and are overridden with a data file (JSON or YAML) passed with
`--data`. The CLI writes `.hermes/plans/<env>/policy-data.json` from the normalized spec:

```yaml
hermes:
  protected: true                        # this plan targets a protected environment
  protected_environments: [production]   # default ["production"]
  secret_placeholders: [set-out-of-band] # allowed alicloud_kms_secret initial values
  exceptions:                            # time-boxed, per-address exceptions
    - rule: HCF-POL-009
      address: module.platform.module.secrets.aws_secretsmanager_secret_version.bootstrap
      reason: "Bootstrap value rotated out-of-band after the first apply (ticket SEC-123)"
      expires: "2027-01-31"
```

A resource is in a protected environment when `hermes.protected` is true, when the rendered root
passes `protected = true` to the composition (read from the plan's configuration), or when its
`hermes-environment` tag/label is listed in `hermes.protected_environments`.

Exceptions need one rule id, one exact resource address (no wildcards: an address of `*` would
switch a rule off for the whole plan), a non-empty reason and an expiry date (`YYYY-MM-DD`) in
the future and at most 90 days away. Invalid or expired exceptions are ignored and reported as
HCF-POL-000 warnings; `hermes plan` goes further and refuses a data file with an invalid
exception before conftest runs, and prints every exception in effect with the plan.

With `hermes plan`, keep exceptions in a reviewed file and pass it with
`--policy-data policy-exceptions.yaml` (repeatable). Hermes adds it after the generated data,
refuses files that set the keys it derives from the spec (`hermes.project`, `environment`,
`provider`, `protected`, `protected_environments`), and records the file's sha256 in the plan's
`meta.json` and in history, together with every exception in effect (rule, address, reason,
expiry). `--policy-dir` adds team policies: they run in a separate conftest invocation from the
embedded bundle and their findings are added to it, so they can add denies but cannot replace,
disable or neutralise the embedded rules (all policies share `package main`; evaluated together,
a custom file could otherwise add a body such as `excepted(_, _) if true` to an embedded helper).

## Tests

`policies/tests/*_test.rego` with synthetic plan documents in
`policies/tests/fixtures/<name>/plan.json` (loaded as `data.fixtures.<name>`): a compliant and a
violating fixture per provider, plus non-plan input, destructive changes and unknown values. The
violation tests compare the exact set of findings, so a new false positive fails them too.

Regression fixtures derived from the compositions (not hand-written):
`policies/tests/fixtures/{aws,gcp,alibaba}_default/plan.json` are mock-provider plans of the golden
render of `examples/multi-cloud` (default configuration: non-protected environment, no
customer-managed keys, observability on, a bucket, a topic with a subscription, a database, two
services and a secret). `composition_test.rego` requires every deny rule to pass on them and checks
that the common resource types are present. `scripts/gen-policy-fixtures.sh` documents exactly how
they are derived (including how provider-computed tags/labels are recomputed); regenerate them with
`make policy-fixtures` after a module change, and `make policy-fixtures-check` (run in the infra
workflow) fails when they are stale, so a module change that an embedded policy would deny fails CI.

```bash
conftest verify --policy policies/plan --policy policies/tests --data policies/tests
```
