# Example: containerized services (AWS)

Two services on ECS Fargate: a public `api` that writes uploads and publishes `orders`, and a
private `worker` that consumes the `orders-fulfilment` subscription and reads uploads.
Least-privilege grants come from each service's `access` block; secrets are injected from
Secrets Manager by reference (`secret_env`), never as values.

```bash
hermes validate -c examples/containerized/hermes.yaml
hermes portability -c examples/containerized/hermes.yaml
hermes render -c examples/containerized/hermes.yaml --env production
```

`production` is protected (the default `protected_environments`), so it gets customer-managed
keys and database deletion protection by default, and applying it needs
`hermes apply --approve <plan-id-prefix>`. The account ID and certificate ARN are placeholders.
Status: renders and validates offline; not deployed to a real AWS account.
