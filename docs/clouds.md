# Configuring your clouds

prod deploys with **your own credentials** — there's no prod account and nothing is
sent anywhere. For each platform, prod reads the credentials the platform's own tooling
already uses (a Fly token, `~/.aws`, `gcloud` ADC, …), exactly like `flyctl` or `aws` do.
Configure the cloud you want, then `prod "deploy this to <cloud>"`.

`prod doctor` checks the basics (an LLM provider + Docker); the notes below cover
per-cloud credentials.

## PaaS targets

| Cloud | How prod authenticates | Notes |
|-------|------------------------|-------|
| **Fly.io** | `FLY_API_TOKEN`, or a `flyctl` session (`fly auth login`) | — |
| **Render** | `RENDER_API_KEY` | Render pulls from a registry — see "Container registry" below. |
| **Vercel** | `VERCEL_TOKEN`, or `vercel login` | Static/serverless; no Docker needed. |
| **Netlify** | `NETLIFY_AUTH_TOKEN`, or `netlify login` | Static/serverless; no Docker needed. |
| **Heroku** | `HEROKU_API_KEY`, or `heroku login` | — |

## Managed-container targets (build locally, push, deploy)

These build a container image on your machine, push it to a registry in **your** cloud
account, and create a managed service. Docker must be running.

### AWS App Runner
- **Credentials:** the standard AWS chain — `~/.aws/credentials`, environment
  (`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`/`AWS_SESSION_TOKEN`), or SSO. prod verifies
  via STS.
- prod creates the ECR repository on demand and an App Runner access role. No extra
  config.
- `prod "deploy this to aws"`

### Google Cloud Run
- **Credentials:** Application Default Credentials — `gcloud auth application-default
  login` (or `GOOGLE_APPLICATION_CREDENTIALS` pointing at a service-account key).
- **Project:** `GOOGLE_CLOUD_PROJECT` (or `gcloud config set project <id>`).
- **Optional:** `PROD_GCP_REGION` (default `us-central1`), `PROD_GCP_AR_REPO` (Artifact
  Registry repo name, default `prod` — created on demand).
- prod ensures the Artifact Registry repo, pushes, creates the service, and makes it
  publicly reachable. `prod "deploy this to cloud run"`

### Azure Container Apps
- **Credentials:** `DefaultAzureCredential` — `az login` (or the
  `AZURE_CLIENT_ID`/`AZURE_TENANT_ID`/`AZURE_CLIENT_SECRET` service-principal env vars).
- **Subscription:** `AZURE_SUBSCRIPTION_ID` (required; see `az account show`).
- **Optional:** `PROD_AZURE_RESOURCE_GROUP` (default `prod-apps`, created on demand),
  `PROD_AZURE_LOCATION` (default `eastus`), `PROD_AZURE_ACR` (Azure Container Registry
  name — must be globally unique; derived from the resource group if unset).
- prod ensures the resource group + ACR + a Container Apps environment, pushes, and
  creates the app. `prod "deploy this to azure"`

## Container registry (Render, and custom setups)

App Runner/Cloud Run/Azure push to a registry in your own cloud automatically (ECR/GAR/
ACR). Render pulls from a registry you provide — set one of:

```bash
# Docker Hub (the default kind)
export PROD_REGISTRY=dockerhub PROD_REGISTRY_USERNAME=<you> PROD_REGISTRY_TOKEN=<token>
# GitHub Container Registry
export PROD_REGISTRY=ghcr PROD_REGISTRY_NAMESPACE=<user-or-org> \
       PROD_REGISTRY_USERNAME=<you> PROD_REGISTRY_TOKEN=<PAT>
# Any other registry: PROD_REGISTRY=generic + PROD_REGISTRY_HOST=<host>
```

## Plugins — any other cloud

Anything not built in can be added as a **provider plugin** — a separate binary you
install with `prod plugin install`. It configures its own credentials as it documents.
See [plugins.md](./plugins.md).

## The LLM (required for every deploy)

prod parses your request with an LLM you provide (no proxy): set `OPENAI_API_KEY` or
`ANTHROPIC_API_KEY`, or run a local **Ollama**. `prod doctor` reports which it found.
