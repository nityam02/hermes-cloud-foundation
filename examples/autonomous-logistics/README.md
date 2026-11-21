# Example: autonomous drone logistics (cloud side)

The spec from the [reference architecture](../../docs/architecture/autonomous-logistics-reference-architecture.md)
§3, plus the ACM certificate the protected `production` environment needs for its public
services. Nine services (public API, partner gateway, telemetry ingest and ops dashboard;
private orchestrator, scheduler, fleet, telemetry writer and notifier), PostgreSQL with PostGIS,
three buckets and four topics that keep control-plane events apart from telemetry.

**Safety boundary.** Flight safety never depends on any cloud provider, on Hermes, or on
anything this spec deploys: flight control, geofencing, emergency procedures, remote ID and
the OCC's command-and-control path are outside the cloud (reference architecture §0). Dispatch
fails closed when cloud state cannot be confirmed.

```bash
hermes validate -c examples/autonomous-logistics/hermes.yaml
hermes portability -c examples/autonomous-logistics/hermes.yaml
hermes render -c examples/autonomous-logistics/hermes.yaml --env production
```

Out of scope for v1alpha1 (see reference architecture §4): device-facing MQTT, C2 relays,
time-series databases, WORM retention, cross-region DR, DNS and SSO in front of the dashboard.
Status: validates and renders offline; not deployed to a real cloud account.
