# State management

Hermes never reads or writes state itself: the engine (OpenTofu or Terraform) does, through
the backend block Hermes renders into `.hermes/build/<env>/main.tf.json`. This page covers
where state lives, how it is locked and encrypted, how the backend is bootstrapped, and how
to move state. Status: rendering is verified by golden-file tests; no backend has been
exercised against a real cloud from this repository.

## Layout: one state per environment and provider

```
<prefix>/<project>/<environment>/<provider>/terraform.tfstate      (prefix default: hermes)
```

| Backend | Rendered settings | Object |
|---|---|---|
| `s3` (aws) | `bucket`, `key`, `region`, `encrypt = true`, `use_lockfile = true` (or `dynamodb_table` when `state.lock_table` is set) | `s3://<bucket>/<prefix>/<project>/<env>/aws/terraform.tfstate` |
| `gcs` (gcp) | `bucket`, `prefix = <prefix>/<project>/<env>/gcp` | `gs://<bucket>/<prefix>/<project>/<env>/gcp/default.tfstate` (the gcs backend names the object after the workspace) |
| `oss` (alibaba) | `bucket`, `prefix = <prefix>/<project>/<env>/alibaba`, `key = terraform.tfstate`, `region`, `encrypt = true`, `tablestore_endpoint`, `tablestore_table` | `oss://<bucket>/<prefix>/<project>/<env>/alibaba/terraform.tfstate` |
| `local` | `path` | `.hermes/state/<prefix>/<project>/<env>/<provider>/terraform.tfstate` (rejected in protected environments) |

Because the provider is part of the key, the same spec can be deployed to several clouds
(see `examples/multi-cloud`: environments `awsdev`, `gcpdev`, `alidev`) without any shared
state. `hermes state show --env <env>` prints the location, locking and encryption.

**Alibaba locking.** The OSS backend locks only with Tablestore. Hermes reads the settings
from `extensions.alibaba.state_tablestore_endpoint` and `extensions.alibaba.state_tablestore_table`
(printed by `hermes state bootstrap`). Without both, `hermes validate` warns, and protected
environments are refused.

## Encryption

* **At rest, server side:** always (`encrypt = true` on s3/oss; GCS encrypts by default). The
  bootstrap modules create versioned, private, encrypted buckets with deletion protection.
* **Client side (OpenTofu only):** `state.encryption.client_side: true` with
  `state.encryption.kms_key` renders a `terraform.encryption` block: key provider `aws_kms`
  (aws) or `gcp_kms` (gcp), method `aes_gcm`, applied to state **and** plan files, with
  `enforced = true` in protected environments. Terraform has no equivalent, so Hermes refuses
  the combination; Alibaba has no OpenTofu key provider, so it is rejected there (`HCF208`).
  The JSON form of the block was checked with OpenTofu 1.13 (references must be bare
  traversals such as `"method.aes_gcm.hermes"`). Not tested with a real KMS key.
* State contains resource attributes and is sensitive even without secrets (the modules never
  put secret values in state; see docs/security/state-security.md).

## Bootstrapping the backend (the chicken-and-egg problem)

The bucket that will hold state cannot hold the state of its own creation. Hermes solves it
with a separate root module that uses **local** state:

```bash
hermes state bootstrap                       # renders .hermes/build/_bootstrap-<provider>/, saves a plan
hermes state bootstrap --apply --approve <plan-id-prefix>
```

* The root calls `infra/bootstrap/<provider>/state-backend` with `project`, `region`
  (`state.region`), `bucket_name` (`state.bucket`) and `labels`; its outputs are `bucket` and
  `backend_config`.
* Its state lives in `.hermes/state/_bootstrap/<project>/<provider>/terraform.tfstate`.
* Applying always requires `--approve` (the bucket protects every environment), and goes
  through the same stale/expiry/tamper gates as `hermes apply`. History:
  `.hermes/history/_bootstrap-<provider>.jsonl`.
* On Alibaba, copy `tablestore_endpoint`/`tablestore_table` from the printed `backend_config`
  into `extensions.alibaba.state_tablestore_*`.

**Keeping or migrating the bootstrap state.** The bootstrap state only describes the state
bucket (and lock table). Either keep the local file somewhere safe (it is needed to change or
delete the bucket later), or migrate it into the new bucket under a dedicated key:

```bash
cd .hermes/build/_bootstrap-aws
# add a backend block (e.g. backend_override.tf.json) pointing at the new bucket, key
#   hermes/_bootstrap/<project>/aws/terraform.tfstate
tofu init -migrate-state
```

Hermes re-renders the bootstrap root with local state, so after migrating, run further
bootstrap changes directly with the engine in that directory, or migrate back first.

## Moving state

* **Changing bucket, prefix or backend:** change `state.*`, run `hermes render`, then in
  `.hermes/build/<env>/` run `tofu init -migrate-state` (the engine copies the state), then
  `hermes plan`. The render digest changes, so older saved plans become stale and are refused.
* **Renaming the project or an environment** changes the key: migrate as above.
* **Locks:** a stuck lock from a crashed run is released with `tofu force-unlock <id>` in the
  build directory, after confirming no other run is active.

## Recovery

State buckets are versioned. Restoring a previous state version is a provider-console or CLI
operation outside Hermes (docs/disaster-recovery/backup-and-restore.md). After any manual state
change, run `hermes plan` and review it before applying; `hermes status --drift` reports drift
between state and reality without changing anything.
