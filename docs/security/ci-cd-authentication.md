# CI/CD authentication

GitHub Actions authenticates to AWS, GCP and Alibaba Cloud with **OIDC federation**. No cloud
secret is stored in GitHub. Status: identity modules implemented and mock-tested; workflows written
but not yet run against a real cloud.

## 1. How it works

1. A job with `permissions: id-token: write` requests a token from GitHub. Its `sub` claim is:
   - `repo:<owner>/<repo>:pull_request` for pull requests,
   - `repo:<owner>/<repo>:ref:refs/heads/<branch>` for pushes and dispatches on a branch,
   - `repo:<owner>/<repo>:environment:<env>` for any job that declares `environment: <env>`.
2. The cloud verifies the token (issuer `https://token.actions.githubusercontent.com`, audience,
   subject) and returns credentials for one hour.
3. **Plan** jobs run without an environment and get the plan identity. **Apply** jobs declare the
   GitHub environment, wait for its required reviewers, and get that environment's apply identity.

GitHub does not give OIDC tokens to pull requests from forks, and the trust policies name this
repository exactly, so forks cannot federate.

## 2. Setup

1. Bootstrap with an operator SSO session (local state is fine; keep it private):

   ```bash
   cd infra/bootstrap/aws/ci-identity      # or gcp / alibaba
   tofu init && tofu plan -out ci.tfplan    # review the trust policies and permissions
   tofu apply ci.tfplan
   ```

   Inputs: `project`, `github_repository` (`owner/repo`, exact case), `environments`, state bucket
   and prefix; optional `plan_branches` (default `main`), `plan_on_pull_request` (default true).
   GCP: `gcp_project_id`, optional `ci_project_id` and `github_repository_id`. Alibaba:
   `oidc_fingerprints` (below), optional Tablestore lock instance. AWS: `create_oidc_provider =
   false` plus `oidc_provider_arn` when the account already has a GitHub provider; optional
   permissions boundary.
2. In GitHub, create one **environment per Hermes environment** with required reviewers,
   "prevent self-review" and deployment branches limited to `main`. The integration workflow
   refuses to apply into an environment without required reviewers.
3. Set repository variables and environment variables (names are listed at the top of
   `.github/workflows/integration.yml` and in each module's outputs).
4. Protect `main` (reviews, status checks) and require CODEOWNERS review for `.github/`,
   `infra/bootstrap/` and `policies/`.

### Alibaba fingerprints

RAM requires the issuer certificate fingerprints. Obtain them yourself and verify them out of
band; they change when GitHub rotates certificates (update the module input when they do):

```bash
openssl s_client -connect token.actions.githubusercontent.com:443 -servername token.actions.githubusercontent.com -showcerts </dev/null 2>/dev/null \
  | awk '/BEGIN CERT/,/END CERT/' > chain.pem
# split chain.pem into certificates, then for each: openssl x509 -in cert.pem -noout -fingerprint -sha1
```

## 3. Workflows

| Workflow | Trigger | Cloud access |
|---|---|---|
| `ci.yml`, `infra.yml`, `security.yml`, `docs.yml`, `discovery-demo.yml` | push, pull request, schedule | none (mock providers, moto) |
| `integration.yml` | `workflow_dispatch` only, `main` only, never on forks | plan identity; apply identity after environment approval |

`integration.yml` defaults to plan. An apply run uses the saved-plan model: the plan job plans
once and writes the plan summary (actions and addresses, never values) and the plan id to the
job summary, then uploads the saved plan directory `.hermes/plans/<env>/` as an artifact with a
**1-day retention**. The apply job runs in the GitHub environment; its **required reviewers are
the human approval** of the plan in that summary. After approval it downloads that artifact,
refuses unless the sha256 of `plan.tfplan` equals the reviewed plan id, and runs
`hermes apply --approve <reviewed plan id>` on it: no re-plan, so exactly the reviewed plan is
applied (the engine refuses a saved plan whose state changed meanwhile, and hermes refuses plans
older than 24 h). Destructive changes need the `allow_destructive` input. Plan-only runs upload
nothing.

**The plan artifact is sensitive**: `plan.tfplan` and `plan.json` contain the full planned values,
including sensitive ones. Run `integration.yml` only in a private repository, restrict who can
download Actions artifacts, and keep the 1-day retention. *Designed and statically checked
(YAML, pinned actions, checkov); the workflow has never run on GitHub.*

All workflows use `permissions: contents: read` by default, grant `id-token: write` only to the
integration jobs and `security-events: write` only to the SARIF upload job, pin every action to a
commit SHA, disable checkout credential persistence, and set timeouts and concurrency groups.

## 4. Hardening options

- **Customise the subject template** to include `repository_id` (GitHub API
  `PUT /repos/{owner}/{repo}/actions/oidc/customization/sub`) and update the trust values; this
  defeats repository-name reuse. On GCP set `github_repository_id`.
- **Pin the workflow**: for AWS and Alibaba, trust can additionally require `job_workflow_ref`
  once the subject template includes it, so only `integration.yml` on `main` can assume apply.
- **Separate accounts/projects per environment**, and a separate GCP project for CI identities.
- **Alert** on any use of the apply identities outside GitHub's IP ranges or outside a run of
  `integration.yml` (CloudTrail `AssumeRoleWithWebIdentity`, GCP STS audit logs, ActionTrail).

## 5. Limits

- Reviewers approve a job; the job applies the exact saved plan whose summary and id they saw
  (sha256-checked), but the summary lists actions and addresses, not values: a reviewer who needs
  the values must download the (sensitive) artifact.
- Repository admins can change environment protection; treat admin rights as deploy rights.
- The plan identity is assumable from any branch of the repository through a pull request: it
  is read-only but sees state and metadata of every environment.
