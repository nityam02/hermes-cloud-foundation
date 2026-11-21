# Example: PostgreSQL with PostGIS (Alibaba Cloud)

A private service with database access and a PostgreSQL 16 database with PostGIS, point-in-time
recovery and, in `production`, high availability and deletion protection.

```bash
hermes validate -c examples/database/hermes.yaml
hermes portability -c examples/database/hermes.yaml   # compare with AWS and GCP
```

Things to know (also reported by `hermes validate`):

- Extensions are requested, not installed: run `CREATE EXTENSION postgis;` after provisioning.
- On Alibaba Cloud the database uses password authentication (`HERMES_DB_AUTH=password`); the
  application account is created out-of-band. AWS and GCP use IAM authentication.
- The Tablestore values are placeholders; `hermes state bootstrap` prints the real ones.

Status: renders and validates offline; not deployed to a real Alibaba Cloud account.
