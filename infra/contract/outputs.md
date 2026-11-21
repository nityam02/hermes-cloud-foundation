# Provider composition output contract — v1alpha1

Every `infra/providers/<provider>/platform` module MUST declare exactly these outputs, with
exactly this shape. Values that do not apply are `null` (objects) or `{}` (maps), never
omitted. No output may contain a secret value; none is marked `sensitive` because none
should need to be. `tests/contracts` checks names (statically) and shapes (with
`tofu test` + mock providers, using the shared fixtures in `tests/contracts/fixtures`).

## `bindings`

Non-secret connection metadata for applications and for `hermes status` / `hermes migrate validate`.

```hcl
{
  provider    = string            # "aws" | "gcp" | "alibaba"
  region      = string
  project     = string
  environment = string

  network = {
    id   = string                 # VPC id / self link / VPC id
    name = string
    cidr = string
  }

  services = map(object({         # keyed by spec service name
    url      = string             # public URL if public, else "" (or internal URL where one exists)
    identity = string             # IAM role ARN / service account email / RAM role ARN
  }))

  database = null | object({
    endpoint        = string      # host name or private IP
    port            = number
    name            = string      # application database name
    user            = string      # application user name ("" if created out-of-band)
    auth            = string      # "iam" | "password"
    credentials_ref = string      # where the admin/app credentials live (secret ARN/name) or ""
    instance_id     = string      # native instance identifier
  })

  buckets = map(object({          # keyed by spec bucket name
    name = string                 # native globally-unique bucket name
    url  = string                 # s3://… | gs://… | oss://…
  }))

  topics = map(object({           # keyed by spec topic name
    id            = string        # SNS ARN | projects/…/topics/… | MNS topic name
    subscriptions = map(string)   # spec subscription name -> SQS queue URL | projects/…/subscriptions/… | MNS queue name
  }))

  secrets = map(string)           # spec secret name -> native reference (ARN / projects/…/secrets/… / secret name)

  observability = {
    log_destination = string      # log group prefix / log bucket / SLS project
    otlp_endpoint   = string      # endpoint services export to ("" when tracing disabled)
    alert_channel   = string      # SNS topic ARN / notification channel id / contact group, or ""
  }
}
```

## `service_environment`

`map(string service name -> map(string))` — the exact non-secret environment variables injected
into each service (the `HERMES_*` and `OTEL_*` binding contract in `docs/architecture/data-contracts.md` §2,
plus the spec's `env`). The contract test asserts that, for the same fixture, all providers expose
the **same keys** for each service (values naturally differ).

## `secret_environment`

`map(string service name -> map(string))` — env var name → native secret reference for the
service's `secret_env`. References only, never values.

---

# State backend bootstrap contract

`infra/bootstrap/<provider>/state-backend` creates the remote state location for one project
(versioned, encrypted, private, deletion-protected, with locking where the backend needs a
separate lock store). Inputs: `project`, `region`, `bucket_name`, `labels`, `kms_key` (optional).
Outputs: `bucket` (string) and `backend_config` (map of the backend settings `hermes` writes
into rendered root modules).
