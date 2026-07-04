# PR 3 — AWS deploys in the binary, on App Runner

**Goal:** deploy to AWS from the single `prod` binary using the user's **own** AWS
credentials — no backend, no central account, no CloudFormation control plane. Reuses PR 2's
container-registry adapter.

This plan deliberately does **not** reimplement the deleted SaaS orchestration (dynamic
CloudFormation template generation for VPC + RDS + ElastiCache + ECS + App Runner + IAM +
Secrets + a DB-URL Lambda). That design existed to deploy into *other people's* accounts via
cross-account STS. For a local tool deploying into the user's own account, it is incidental
complexity. See [docs/design.md](./design.md) §"AWS Deployment (Deep Dive)" for the legacy design.

## Why App Runner, not CloudFormation

[AWS App Runner](https://docs.aws.amazon.com/apprunner/) is purpose-built for
"container image → autoscaled HTTPS service," fully managed. It needs no VPC, no ECS cluster,
no load balancer, and no template generation. A deploy is three SDK calls: ensure/authenticate
an ECR repo, push the image, `CreateService` (or `StartDeployment` for updates), then poll.

This eliminates the heaviest and riskiest work (a Go reimplementation of a template-generation
control plane), deletes most of the 3,356 lines of backend-coupled AWS code rather than porting
it, and gets to a working AWS deploy far faster.

**Trade-off, accepted for v1:** App Runner is more opinionated than the old full stack — it
doesn't auto-provision RDS/Redis, and reaching a *private* database needs a VPC connector. v1
uses **bring-your-own database** (`DATABASE_URL` env var). Managed RDS via the SDK is a clean
opt-in follow-up; full VPC/RDS parity, if ever required, uses **static, parameterized
CloudFormation templates via `go:embed`** — never templates generated in Go.

## Architecture

```
prod  →  build image locally (Docker SDK, existing)
      →  ECR: ensure repo + auth (registry adapter, new "ecr" kind)  →  push (existing pushImage)
      →  App Runner CreateService/StartDeployment (AWS SDK, user's creds)  →  poll  →  service URL
```

Everything runs with the user's AWS credentials from the standard chain (`~/.aws/credentials`,
env, SSO) that the Go AWS SDK already resolves. No `aws-auth`, no STS AssumeRole, no S3.

## Stages (each a gate-verified increment, reviewed before merge)

### A — AWS credentials (small)
Resolve the user's AWS config via the AWS SDK default chain; drop the `aws-auth` backend flow
and `internal/auth/aws.go`'s cross-account/session logic. A clear error if no creds/region are
configured. No `PROD_AWS_ACCOUNT_ID`, no external ID.

### B — ECR as a registry adapter kind (medium)
Extend `internal/registry` with an `ecr` kind that, using the user's AWS creds:
- resolves the registry host `<account>.dkr.ecr.<region>.amazonaws.com`,
- **ensures the repository exists** (`CreateRepository`, idempotent) — the "ensure repo" hook
  the adapter review anticipated,
- returns push credentials from `ecr:GetAuthorizationToken`.

Then reuse the existing `DockerGenerator.pushImage`/`PushToUserRegistry`. App Runner requires a
private **ECR** image (not arbitrary Docker Hub), which is exactly why ECR is a distinct kind.
Config: `PROD_REGISTRY=ecr` (region/account inferred from the AWS creds), so the same
registry-adapter surface serves both Render and AWS.

### C — App Runner service (the core; medium)
Via the App Runner SDK:
- create/find an **ECR access role** (one IAM role App Runner assumes to pull from ECR;
  `build.apprunner.amazonaws.com` trust + `AWSAppRunnerServicePolicyForECRAccess`) — idempotent,
- `CreateService` (or `StartDeployment` when the service exists) with: the ECR image ref, port,
  CPU/memory, health check, env vars, and secrets,
- map the analyzer's `ProjectSpec` (start command, port, env) to the App Runner
  `SourceConfiguration`/`InstanceConfiguration`.

### D — Poll + result (small)
Poll `ListOperations`/`DescribeService` until the service is `RUNNING` (or failed), then return
the App Runner service URL through the existing deploy-status/output path. Health-check-driven
success replaces the CFN stack-event polling.

### E — Env vars & secrets (small–medium)
Plain env vars go into `RuntimeEnvironmentVariables`. Sensitive vars go into **Secrets Manager**
(`CreateSecret`) referenced via `RuntimeEnvironmentSecrets` — no custom Lambda, no
DB-URL-constructor. `DATABASE_URL` is a user-provided env/secret in v1 (bring-your-own DB).

### F — Un-gate AWS + delete the old path + tests
- Remove AWS from `unsupportedLocalPlatform`; gate on AWS creds + `PROD_REGISTRY=ecr` being
  resolvable, with a clear message (mirrors the Render registry gate).
- **Delete** the backend-coupled AWS code: `internal/backend/aws`, the CFN/ECS/preview/stack
  code in `internal/deployment/aws`, `internal/auth/aws.go`, the `deploy-aws-stack` /
  `get-aws-stack-status` / `run-ecs-migration` / `create-repo` / `push-token` /
  `preview-aws-template` call sites and their activities.
- Tests: the ECR adapter kind (host/repo/auth construction, pure parts), the ProjectSpec→App
  Runner mapping, the creds/registry gate. End-to-end App Runner deploy is verified manually
  against a real AWS account (not hermetic).

## What gets deleted vs. written

- **Deleted:** the entire CFN/VPC/ECS/RDS/ElastiCache/Secrets-template + DB-URL-Lambda surface
  and its CLI callers — most of the 3,356 lines.
- **Written:** an `ecr` registry-adapter kind, an App Runner client (create/deploy/poll +
  ECR-access-role + secrets), and the ProjectSpec→App Runner mapping. A fraction of the code.

## To verify before implementing (against current AWS docs)

1. App Runner's image source: confirm it still requires **private ECR** (with an access role)
   for private images, and whether public Docker Hub / ECR Public is viable as an alternative.
2. ECR access role: exact trust policy + managed policy for App Runner→ECR pulls.
3. Secrets: `RuntimeEnvironmentSecrets` referencing Secrets Manager ARNs, and the instance-role
   permission App Runner needs to read them.
4. App Runner region availability + the SDK's create/deploy/poll operation shapes.

## Deferred (follow-ups, not v1)

- Managed **RDS/ElastiCache** provisioning via the SDK (opt-in), and a **VPC connector** for
  private DB access.
- **Migrations** (a one-shot task) — v1 assumes migrations are run by the user or at container
  start.
- Static-embedded CloudFormation as a "full-control stack" alternative, if ever required.
