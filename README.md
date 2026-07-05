# prod

**Deploy apps and agents to the cloud from a sentence — one binary, no backend, no account.**

```bash
prod "deploy this to fly"
```

Anyone can now *generate* a working app in an afternoon. Almost no one can *ship* one
without fighting six dashboards, a Dockerfile, and an IAM policy. `prod` closes that gap:
you describe what you want in English, it reads your project, shows you a plan, and runs the
deploy against your own cloud account.

`prod` is a single self-contained Go binary. It runs entirely on your machine, keeps its
state in a local file, and talks straight to each platform — like `terraform`, `flyctl`, or
`pulumi`. There is no service to sign up for and nothing to stand up.

---

## Install

> Signed one-line and Homebrew installs land with the distribution work — until then, build
> from source (see [CONTRIBUTING.md](./CONTRIBUTING.md)).

```bash
# coming soon
curl -fsSL https://prod.dev/install.sh | sh
brew install pushtoprodai/tap/prod
```

Requires **Go 1.25+** and a C toolchain to build from source (`prod` links a native
dependency — see the CGO note in CONTRIBUTING).

---

## Quickstart (under 5 minutes)

**1. Point `prod` at an LLM.** Use a cloud key *or* a local model — your choice, no proxy,
nothing sent to us.

```bash
export OPENAI_API_KEY=sk-...        # or...
export ANTHROPIC_API_KEY=sk-ant-... # or run a local Ollama (no cloud key needed)
```

If neither key is set, `prod` falls back to a local **Ollama** at `http://localhost:11434`
(defaults to `llama3.1`). Override the model on any provider with `PROD_LLM_MODEL`.

**2. Make sure you're logged in to the platform you want to deploy to** — `prod` uses *your*
credentials (e.g. a Fly token / `flyctl` session). It never asks you to create a `prod`
account.

**3. Deploy.** From your project directory:

```bash
prod "deploy this to fly with a postgres"
```

`prod` parses the request, analyzes your project, shows you a plan to approve, and runs the
deploy. No signup, no backend, no config file required.

### Command surface

| Command | What it does |
|---|---|
| `prod [prompt]` | Start an interactive session, or run a one-shot deploy from the prompt |
| `prod run <prompt>` | Execute a single command and exit — for automation / scripting (set `PROD_JSON_MODE=true` for structured JSON output) |
| `prod mcp` | Start the MCP server (stdio) so AI agents can call prod as a tool |

### Use prod from an AI agent (MCP)

`prod mcp` exposes prod over the [Model Context Protocol](https://modelcontextprotocol.io) so
agents like Claude Code, Cursor, and Cline can use it. Today it serves `list_deploys` (recent
deployments) and `analyze_project` (detect a project's stack). Add to your MCP client config:

```jsonc
{ "mcpServers": { "prod": { "command": "prod", "args": ["mcp"] } } }
```

---

## How it works

- **One binary, no backend.** Everything — intent parsing, project analysis, planning, the
  deploy state machine, and every platform adapter — lives in the `prod` binary. No server
  is required to deploy.
- **No account.** Local mode needs no `prod` login. The only credentials that matter are the
  target platform's, read from where they already live on your machine.
- **BYO LLM keys, direct.** LLM calls go straight to OpenAI, Anthropic, or a local Ollama
  with your key. There's no proxy in the path.
- **Local history.** Deploy history lives in a file you can read: `~/.prod/history.json`.
- **Your cloud, your creds.** Deploys run against your own platform account using your own
  tokens — like any other local CLI.
- **No phone-home.** `prod` sends no telemetry to us — ever. Errors go to your local logs.
  If you *want* error tracking, point it at **your own** Sentry with `PROD_SENTRY_DSN`; unset
  (the default) means it's off.

---

## Supported platforms

Today, `prod` deploys directly to:

- **Fly.io**
- **Render** — needs a container registry (see below)
- **Vercel**
- **Netlify**
- **Heroku**
- **AWS** — App Runner, with your own AWS credentials (see below)

### Deploying to Render — bring your own registry

Render deploys a container image, so `prod` builds the image locally and pushes it to **a
container registry you own** (no hosted middleman). Point `prod` at your registry with these
environment variables, then deploy as usual (`prod "deploy this to render"`):

| Variable | Required | Description |
|---|---|---|
| `PROD_REGISTRY` | no | `dockerhub` (default), `ghcr`, or `generic` |
| `PROD_REGISTRY_USERNAME` | **yes** | registry username |
| `PROD_REGISTRY_TOKEN` | **yes** | registry password / access token |
| `PROD_REGISTRY_NAMESPACE` | for `ghcr`/`generic` | user or org namespace (defaults to your username on Docker Hub) |
| `PROD_REGISTRY_HOST` | for `generic` | registry host, e.g. `registry.gitlab.com` |

```bash
# Docker Hub
export PROD_REGISTRY=dockerhub
export PROD_REGISTRY_USERNAME=your-dockerhub-user
export PROD_REGISTRY_TOKEN=dckr_pat_...        # a Docker Hub access token

# GitHub Container Registry (GHCR)
export PROD_REGISTRY=ghcr
export PROD_REGISTRY_NAMESPACE=your-gh-user-or-org
export PROD_REGISTRY_USERNAME=your-gh-user
export PROD_REGISTRY_TOKEN=ghp_...             # a PAT with write:packages
```

`prod` pushes the image to that registry, registers the credentials with Render so it can pull,
and creates the service — all with **your** Render API key and **your** registry. If the
registry isn't configured, `prod` tells you exactly what to set before it does any work.

### Deploying to AWS — App Runner, your account

`prod` deploys to **AWS App Runner** (a managed container→HTTPS service) using your own AWS
credentials — no backend, no CloudFormation, no central account. It reads credentials from the
**standard AWS chain** (`~/.aws`, environment variables, or SSO), exactly like the AWS CLI, so if
`aws sts get-caller-identity` works, `prod` works. No `PROD_REGISTRY` is needed — the image goes
to your own **ECR** automatically.

```bash
# however you normally configure AWS — any of these works:
export AWS_PROFILE=my-profile          # ~/.aws/config + ~/.aws/credentials
# or AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY, or an SSO session
export AWS_REGION=us-east-1            # required if not set in your profile

prod "deploy this to aws"
```

On deploy, `prod` builds the image locally, pushes it to ECR (creating the repo on first use),
creates the App Runner service (with an IAM access role so it can pull the image), and waits for
it to come up — returning the service URL. Sensitive env vars go into **Secrets Manager**; plain
ones become runtime env. Bring your own database via `DATABASE_URL`. Redeploy to ship a new
version. (Rollback and managed RDS provisioning are planned — see the [ROADMAP](./ROADMAP.md).)

---

## Scope & status

- **Project analysis covers Node and Python today.** More languages and agent-framework
  detectors are on the roadmap.
- "Agent" means two things here — the internal deploy orchestrator, and AI agents as a deploy
  target. Both are first-class; see [CLAUDE.md](./CLAUDE.md) for the distinction.
- This is early, moving fast, and open. The plan, the open-core boundary, and what's done
  live in [ROADMAP.md](./ROADMAP.md).

---

## Contributing

Build from source, run the local checks, open a PR — see
[CONTRIBUTING.md](./CONTRIBUTING.md). We test locally, not in CI.

## License

MIT — see [LICENSE](./LICENSE).
