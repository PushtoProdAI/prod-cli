# Choosing a cloud

**What you'll accomplish:** pick the right platform for what you're shipping, then get its
credentials set up in three steps — obtain, set, verify — so your first `prod "deploy…"`
just works. No prior cloud experience assumed.

prod deploys to nine clouds (plus experimental Modal) using **your own account** on each.
There's no prod account and nothing is sent to a prod server. The only real decision is
*which* cloud — this guide makes that quick, then walks the setup.

## Prerequisites

- prod installed and an LLM key set (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, or a local
  Ollama). See [Bring your own LLM](./bring-your-own-llm.md).
- An account on whichever cloud you pick below (all have free tiers to start).

## Pick in 30 seconds

| If you're shipping… | Easiest picks | Why |
|---------------------|---------------|-----|
| **A static site** (React/Vite build, plain HTML, docs) | **Cloudflare Pages**, Netlify, Vercel | No container, no Docker, fastest. Cloudflare Pages is static-only. |
| **A web app with a server** (API, SSR, needs a database) | **Fly.io**, Render, Heroku | PaaS — you don't manage the cloud. Fly/Render provision Postgres for you. |
| **An AI agent or MCP server** | **Fly.io**, Render, **Modal** | Fly/Render run long-lived HTTP agents; Modal is Python-native and GPU-capable. |
| **Into your own cloud account** (compliance, existing infra) | **AWS App Runner**, Google **Cloud Run**, **Azure** Container Apps | prod builds locally, pushes to a registry in *your* account, creates a managed service. |
| **A background worker / cron** | **Render**, Modal, Fly.io | No URL to health-check — see [Deploy an agent or worker](./deploy-an-agent-or-worker.md). |

Still unsure? Start with **Fly.io** (full apps) or **Cloudflare Pages** (static). Both are
fast to set up and easy to tear down.

## The three families

prod's clouds fall into three groups; the setup differs by group:

1. **PaaS** (Fly.io, Render, Vercel, Netlify, Cloudflare Pages, Heroku) — the platform owns
   the infrastructure. You authenticate with a token or a CLI login and prod does the rest.
   No Docker needed for the static/serverless ones (Vercel, Netlify, Cloudflare).
2. **Managed-container in your cloud** (AWS App Runner, Google Cloud Run, Azure Container
   Apps) — prod builds a container image on your machine, pushes it to a registry in **your**
   cloud account, and creates a managed service. **Docker must be running.** You authenticate
   with that cloud's normal credentials (`~/.aws`, `gcloud`, `az`).
3. **Modal** (experimental) — serverless and Python-native; prod deploys your Python app
   directly via the `modal` CLI. No image build.

## Set up your cloud (obtain → set → verify → deploy)

Each cloud below is three steps and a deploy. For full detail and every optional variable,
see [Configuring your clouds](../clouds.md).

### Fly.io — full apps, agents, workers
1. **Obtain:** `fly auth login` (opens a browser). No token to copy for local use.
2. **Set:** nothing — the login is stored by `flyctl`. (For CI, use `fly tokens create
   deploy` and export it as `FLY_API_TOKEN`.)
3. **Verify:** `fly auth whoami`.
4. **Deploy:** `prod "deploy this to fly"`.

### Render — full apps and workers (needs a registry)
1. **Obtain:** Dashboard → **Account Settings → API Keys → Create API Key**. Render pulls
   your image from a container registry you own — also set up Docker Hub or GHCR
   ([registry setup](../clouds.md#container-registry-render-and-custom-setups)).
2. **Set:** `export RENDER_API_KEY=...` (plus the `PROD_REGISTRY_*` vars).
3. **Verify:** deploy — or `curl -s -H "Authorization: Bearer $RENDER_API_KEY" https://api.render.com/v1/owners`.
4. **Deploy:** `prod "deploy this to render"`.

### Vercel / Netlify — static & serverless (no Docker)
1. **Obtain:** `vercel login` / `netlify login` (or a token from the dashboard).
2. **Set:** nothing for the CLI login (or `VERCEL_TOKEN` / `NETLIFY_AUTH_TOKEN` for CI).
3. **Verify:** `vercel whoami` / `netlify status`.
4. **Deploy:** `prod "deploy this to vercel"` / `prod "deploy this to netlify"`.

### Cloudflare Pages — static sites (no Docker, no wrangler)
1. **Obtain:** a token at `dash.cloudflare.com` → **My Profile → API Tokens → Create Token**,
   scoped **Account → Cloudflare Pages → Edit**. Your **Account ID** is in the dashboard URL
   and the Workers & Pages sidebar.
2. **Set:** `export CLOUDFLARE_API_TOKEN=...` and `export CLOUDFLARE_ACCOUNT_ID=...` (both
   required).
3. **Verify:** `curl -s -H "Authorization: Bearer $CLOUDFLARE_API_TOKEN" https://api.cloudflare.com/client/v4/user/tokens/verify`.
4. **Deploy:** `prod "deploy this to cloudflare"`.

### Heroku — classic PaaS
1. **Obtain:** `heroku login` (or `heroku authorizations:create` for a token).
2. **Set:** nothing for the login (or `HEROKU_API_KEY` for CI).
3. **Verify:** `heroku auth:whoami`.
4. **Deploy:** `prod "deploy this to heroku"`.

### AWS App Runner / Google Cloud Run / Azure Container Apps — your own cloud
1. **Obtain:** whatever you already use for that cloud — AWS: `~/.aws` / `AWS_PROFILE` / SSO;
   GCP: `gcloud auth application-default login`; Azure: `az login`. **Start Docker.**
2. **Set:** AWS needs a region (`AWS_REGION`); GCP needs `GOOGLE_CLOUD_PROJECT`; Azure needs
   `AZURE_SUBSCRIPTION_ID`.
3. **Verify:** `aws sts get-caller-identity` / `gcloud auth list` / `az account show`.
4. **Deploy:** `prod "deploy this to aws"` / `"…to cloud run"` / `"…to azure"`.

### Modal — Python agents (experimental)
1. **Obtain:** `pip install modal` then `modal token new`.
2. **Set:** nothing (or `MODAL_TOKEN_ID` + `MODAL_TOKEN_SECRET`).
3. **Verify:** `modal token current`.
4. **Deploy:** `prod "deploy this to Modal"`.

## What success looks like

- Your chosen cloud's whoami/verify command returns your account — so prod will
  authenticate the same way.
- `prod "deploy this to <cloud>"` shows a plan, you approve it, and it returns a live URL
  (or, for a worker, a running service you can watch with `prod logs`).

## Common pitfalls

- **"Docker isn't running."** The managed-container clouds (AWS, Cloud Run, Azure) and
  Render build an image locally — start Docker Desktop first. `prod doctor` flags this.
- **Picked Cloudflare Pages for a server app.** Cloudflare Pages here is **static only**. A
  server/API needs a PaaS (Fly/Render/Heroku) or a managed-container cloud.
- **`prod doctor` says everything's fine but the deploy can't authenticate.** `doctor`
  checks your LLM and Docker, **not** platform credentials. Run the cloud's whoami
  (above) to confirm those separately.
- **A token in your environment overrides your CLI login.** An exported `FLY_API_TOKEN` (or
  similar) wins over `fly auth login`. Unset a stale token if the wrong account is used.

## See also

- [Configuring your clouds](../clouds.md) — the full per-cloud reference and every optional variable.
- [Environment variables & secrets](./environment-variables-and-secrets.md) — configuring your *app's* runtime vars.
- [Add a database](./add-a-database.md) — which clouds provision Postgres/Redis for you.
- [Troubleshooting](./troubleshooting.md) — when a deploy won't authenticate or stalls.
