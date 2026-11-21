# Example: the same spec on AWS, GCP and Alibaba Cloud

The environments `awsdev`, `gcpdev` and `alidev` override only `cloud.*` and `state.bucket`.
Everything else (services, access grants, database, buckets, topics, secrets) is identical, and
each cloud has isolated state (`hermes/sample-portable/<env>/<provider>/terraform.tfstate`; on GCS the object is `.../gcp/default.tfstate`).

```bash
for env in awsdev gcpdev alidev; do hermes render -c examples/multi-cloud/hermes.yaml --env $env; done
hermes portability -c examples/multi-cloud/hermes.yaml        # measured, per requested feature
```

Applications read the same binding variables on every cloud (`HERMES_BUCKET_ASSETS`,
`HERMES_TOPIC_EVENTS`, `HERMES_DB_HOST`, ...); see docs/architecture/application-portability.md.
What differs is reported, not hidden: for example database authentication is IAM on AWS/GCP
and password on Alibaba Cloud, and public HTTPS on AWS/Alibaba needs a certificate.

Account IDs, project IDs and bucket names are placeholders. Status: renders and validates
offline for all three providers; not deployed to real accounts.
