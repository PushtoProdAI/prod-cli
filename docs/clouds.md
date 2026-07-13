# Configuring your clouds

prod deploys with **your own credentials** — there's no prod account and nothing is
sent anywhere. For each platform, prod reads the credentials the platform's own tooling
already uses (a Fly token, `~/.aws`, `gcloud` ADC, …), exactly like `flyctl` or `aws` do.
Configure the cloud you want, then `prod "deploy this to <cloud>"`.

**New here?** If you just want to ship and aren't sure which cloud to pick, start with
[Choosing a cloud](./guides/choosing-a-cloud.md) — it walks the trade-offs and the
zero-to-first-deploy setup for each. This page is the full per-cloud reference.

**Two kinds of "environment variable" — don't conflate them.** This page covers the
credentials **prod** needs to authenticate to a cloud (a Fly token, AWS creds). The
env vars and secrets your **deployed app** needs at runtime (an API key, `DATABASE_URL`)
are a separate flow — see [Environment variables & secrets](./guides/environment-variables-and-secrets.md).

`prod doctor` checks the basics (an LLM provider + Docker). It does **not** check your
platform credentials — those are verified when you deploy. To confirm a cloud is wired up
*before* deploying, run its own whoami (see [Verify your setup](#verify-your-setup) below).

## PaaS targets

| Cloud | How prod authenticates | Get a token (if not using the CLI login) |
|-------|------------------------|------------------------------------------|
| **Fly.io** | `FLY_API_TOKEN`, or a `flyctl` session (`fly auth login`) | fly.io dashboard → **Account → Tokens** (or `fly tokens create deploy`) |
| **Render** | `RENDER_API_KEY` | Dashboard → **Account Settings → API Keys → Create API Key** |
| **Vercel** | `VERCEL_TOKEN`, or `vercel login` | Vercel → **Settings → Tokens**. Static/serverless; no Docker needed. |
| **Netlify** | `NETLIFY_AUTH_TOKEN`, or `netlify login` | Netlify → **User settings → Applications → Personal access tokens**. No Docker. |
| **Cloudflare Pages** | `CLOUDFLARE_API_TOKEN` + `CLOUDFLARE_ACCOUNT_ID` | See [Cloudflare setup](#cloudflare-pages-setup) — **static sites only**, direct upload, no `wrangler`, no Docker. |
| **Heroku** | `HEROKU_API_KEY` (or `HEROKU_AUTH_TOKEN`), or `heroku login` | `heroku authorizations:create`, or Account settings → **API Key** |

**When both a token and a CLI session exist, the environment variable wins.** So an
exported `FLY_API_TOKEN` overrides your `flyctl` session — handy in CI, surprising if you
forgot a stale token was set. The general precedence is **flags → env → config file →
built-in default**.

### Cloudflare Pages setup

Cloudflare needs **two** values, both required:

1. **`CLOUDFLARE_API_TOKEN`** — create it at `dash.cloudflare.com` → **My Profile → API
   Tokens → Create Token**. Use a **Custom token** scoped to **Account → Cloudflare Pages →
   Edit** (least privilege — that's all prod needs). Don't use the Global API Key.
2. **`CLOUDFLARE_ACCOUNT_ID`** — your account ID is in the dashboard **URL**
   (`dash.cloudflare.com/<account-id>/…`) and on the **right-hand sidebar** of the
   **Workers & Pages** overview.

```bash
export CLOUDFLARE_API_TOKEN=...      # scoped: Account → Cloudflare Pages → Edit
export CLOUDFLARE_ACCOUNT_ID=...
prod "deploy this to cloudflare"
```

If you set the token but not the account ID, prod stops and tells you the account ID is
missing rather than failing mid-deploy.

## Managed-container targets (build locally, push, deploy)

These build a container image on your machine, push it to a registry in **your** cloud
account, and create a managed service. Docker must be running.

### AWS App Runner
- **Credentials:** the standard AWS chain — `~/.aws/credentials`, environment
  (`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`/`AWS_SESSION_TOKEN`), a named profile, or
  SSO. If `aws sts get-caller-identity` works, prod works; prod verifies via STS.

  ```bash
  # however you normally configure AWS — any of these works:
  export AWS_PROFILE=my-profile        # ~/.aws/config + ~/.aws/credentials
  # or AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY, or an SSO session
  export AWS_REGION=us-east-1          # required if not set in your profile
  prod "deploy this to aws"
  ```
- prod creates the ECR repository on demand and an App Runner access role. No extra
  config. Sensitive env vars go to **Secrets Manager**; plain ones become runtime env.

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
  name — must be globally unique; derived from the resource group if unset),
  `PROD_AZURE_ACA_ENV` (Container Apps environment name, default `prod-env`).
- prod ensures the resource group + ACR + a Container Apps environment, pushes, and
  creates the app. `prod "deploy this to azure"`

## Modal (experimental)

Modal (modal.com) is serverless, Python-native, and GPU-capable — for deploying agents.
Unlike the container clouds, there's no image build or registry: prod deploys your Python
app directly via the `modal` CLI.

- **Install the CLI:** `pip install modal`.
- **Credentials:** `modal token new` (writes `~/.modal.toml`), or set `MODAL_TOKEN_ID` +
  `MODAL_TOKEN_SECRET`.
- **Entrypoint:** prod deploys the first `.py` at the project root that defines
  `modal.App(...)`; set `MODAL_ENTRYPOINT` to point at a specific file.
- `prod "deploy this to Modal"`. Rollback isn't supported yet (redeploy the previous
  version). **Experimental — not yet validated end-to-end against a live account.**

## Container registry (Render, and custom setups)

App Runner/Cloud Run/Azure push to a registry in your own cloud automatically (ECR/GAR/
ACR). Render pulls from a registry you provide — set the variables below, then deploy as
usual (`prod "deploy this to render"`).

| Variable | Required | Description |
|---|---|---|
| `PROD_REGISTRY` | no | `dockerhub` (default), `ghcr`, or `generic` |
| `PROD_REGISTRY_USERNAME` | **yes** | registry username |
| `PROD_REGISTRY_TOKEN` | **yes** | registry password / access token |
| `PROD_REGISTRY_NAMESPACE` | for `ghcr`/`generic` | user or org namespace (defaults to your username on Docker Hub) |
| `PROD_REGISTRY_HOST` | for `generic` | registry host, e.g. `registry.gitlab.com` |

```bash
# Docker Hub (the default kind) — token at Docker Hub → Account Settings →
# Security → New Access Token
export PROD_REGISTRY=dockerhub PROD_REGISTRY_USERNAME=<you> PROD_REGISTRY_TOKEN=<token>
# GitHub Container Registry — a PAT with write:packages
export PROD_REGISTRY=ghcr PROD_REGISTRY_NAMESPACE=<user-or-org> \
       PROD_REGISTRY_USERNAME=<you> PROD_REGISTRY_TOKEN=<PAT>
# Any other registry: PROD_REGISTRY=generic + PROD_REGISTRY_HOST=<host>
```

If the registry isn't configured, prod tells you exactly what to set before it does any work.

## Verify your setup

`prod doctor` confirms your LLM provider and Docker, but **platform credentials are
checked at deploy time**, not by `doctor`. To confirm a cloud is wired up first, run the
platform's own whoami — if it succeeds, prod will authenticate the same way:

| Cloud | Check |
|-------|-------|
| Fly.io | `fly auth whoami` |
| Render | `curl -s -H "Authorization: Bearer $RENDER_API_KEY" https://api.render.com/v1/owners` |
| Vercel | `vercel whoami` |
| Netlify | `netlify status` |
| Cloudflare | `curl -s -H "Authorization: Bearer $CLOUDFLARE_API_TOKEN" https://api.cloudflare.com/client/v4/user/tokens/verify` |
| Heroku | `heroku auth:whoami` |
| AWS | `aws sts get-caller-identity` |
| Google Cloud | `gcloud auth list` and `gcloud config get-value project` |
| Azure | `az account show` |
| Modal | `modal token current` |

## Plugins — any other cloud

Anything not built in can be added as a **provider plugin** — a separate binary you
install with `prod plugin install`. It configures its own credentials as it documents.
See [plugins.md](./plugins.md).

## The LLM (required for every deploy)

prod parses your request with an LLM you provide (no proxy): set `OPENAI_API_KEY` or
`ANTHROPIC_API_KEY`, or run a local **Ollama**. `prod doctor` reports which it found. See
[Bring your own LLM](./guides/bring-your-own-llm.md) for where to create each key and how
provider selection works.
