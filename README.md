# prod

[![CI](https://github.com/PushtoProdAI/prod-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/PushtoProdAI/prod-cli/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/PushtoProdAI/prod-cli)](https://github.com/PushtoProdAI/prod-cli/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/github/go-mod/go-version/PushtoProdAI/prod-cli?filename=cli%2Fgo.mod)](cli/go.mod)

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

**Homebrew or the install script** — macOS and Linux (on Windows, use WSL2):

```bash
brew install pushtoprodai/tap/prod
# or:
curl -fsSL https://raw.githubusercontent.com/PushtoProdAI/prod-cli/main/scripts/install.sh | sh
```

**Build from source** — requires **Go 1.25+** and a C toolchain (`prod` links a native
dependency — see the CGO note in [CONTRIBUTING.md](./CONTRIBUTING.md)):

```bash
git clone https://github.com/PushtoProdAI/prod-cli && cd prod-cli/cli
go build -o prod ./cmd/main.go        # → ./prod   (or `make build` for a versioned binary in ../bin)
```

On first run, `prod` downloads the BAML engine library (~56 MB, needs network + CA
certificates); `prod doctor` checks your setup.

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

**Preview first, or undo:**

```bash
prod --dry-run "deploy this to fly"   # show the plan + estimated cost, deploy nothing
prod --yes "deploy this to fly"       # skip the approval prompt (automation / CI)
prod "rollback"                       # roll back the last deploy (auto-detects the platform)
```

### Command surface

| Command | What it does |
|---|---|
| `prod [prompt]` | Start an interactive session, or run a one-shot deploy from the prompt |
| `prod new <template>` | Scaffold a deployable starter (e.g. `prod new agent-worker my-agent`) |
| `prod run <prompt>` | Execute a single command and exit — for automation / scripting (set `PROD_JSON_MODE=true` for structured JSON output) |
| `prod ls` | List recent deployments — name, platform, status, live URL (`--json`, `--platform`, `--all`) |
| `prod open <app>` | Open a deployed app's live URL, or `--console` for its platform dashboard |
| `prod logs <app>` | Tail a deployed app's logs via the platform's own CLI |
| `prod doctor` | Check prerequisites (LLM provider reachable? Docker available?) — run this if a deploy won't start |
| `prod plugin` | Install / list / remove provider plugins — add your own cloud without a fork (see [docs/plugins.md](docs/plugins.md)) |
| `prod mcp` | Start the MCP server (stdio) so AI agents can call prod as a tool |

Deploy, roll back, and tear down are natural language: `prod "deploy this to fly"`,
`prod "rollback"`, `prod "destroy this on fly"` (destroy asks for confirmation — it's permanent).

### Use prod from an AI agent (MCP)

`prod mcp` exposes prod over the [Model Context Protocol](https://modelcontextprotocol.io) so
agents like Claude Code, Cursor, and Cline can use it. The three action tools —
**`deploy`**, **`rollback`**, and **`destroy`** — are each behind a **human-approval gate**:
`confirm=false` (the default) returns the plan + estimated cost and changes *nothing*; the
agent must pass `confirm=true` to actually run it. The read-only tools let an agent answer
"what's running / where / show me the logs" in-session: **`status`** (platform, last status,
live/not-live), **`deep_link`** (live + console URLs), **`logs`** (the platform CLI command +
console URL), plus `list_deploys` and `analyze_project`/`doctor`. Add to your MCP client
config:

```jsonc
{ "mcpServers": { "prod": { "command": "prod", "args": ["mcp"] } } }
```

**→ Full walkthrough, with worked transcripts: [docs/agent-deploy.md](./docs/agent-deploy.md)** —
wire prod into Claude Code / Cursor and deploy, check status, tail logs, and roll back, all
in-session.

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

`prod` deploys directly to nine clouds with **your own credentials** — see
[docs/clouds.md](docs/clouds.md) for per-cloud setup:

- **Fly.io**, **Render**, **Vercel**, **Netlify**, **Heroku** (PaaS)
- **Cloudflare Pages** — static sites, uploaded directly via Cloudflare's API (no `wrangler`)
- **AWS App Runner**, **Google Cloud Run**, **Azure Container Apps** (managed container —
  build locally, push to a registry in your account, deploy)
- **Modal** (experimental) — serverless, Python-native, GPU-capable agents, deployed via the
  `modal` CLI

…and **anything else via a plugin**: add your own cloud or internal PaaS as a separate
binary with `prod plugin install`, no fork required — see [docs/plugins.md](docs/plugins.md).

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

- **Project analysis covers eight languages today** — Node, Python, Go, Ruby (Rails/Sinatra),
  Rust (Axum/Actix), Java (Spring Boot), C#/.NET (ASP.NET Core), and Elixir (Phoenix) — plus any
  project that ships its own `Dockerfile`. More agent-framework detectors are on the roadmap.
- "Agent" means two things here — the internal deploy orchestrator, and AI agents as a deploy
  target. Both are first-class; see [CLAUDE.md](./CLAUDE.md) for the distinction.
- This is early, moving fast, and open. The plan, the open-core boundary, and what's done
  live in [ROADMAP.md](./ROADMAP.md).

---

## Documentation

- [docs/agent-deploy.md](./docs/agent-deploy.md) — drive prod from your coding agent (MCP), with transcripts
- [docs/pr-previews.md](./docs/pr-previews.md) — per-PR preview deploys with a GitHub Action
- [docs/clouds.md](./docs/clouds.md) — per-cloud credential setup (all 8 clouds + Modal + plugins)
- [docs/plugins.md](./docs/plugins.md) — write a provider plugin to add your own cloud
- [docs/dx-roadmap.md](./docs/dx-roadmap.md) — the developer-experience roadmap (Tier 1, previews, languages)
- [docs/windows.md](./docs/windows.md) — running prod on Windows (via WSL2)
- [ROADMAP.md](./ROADMAP.md) — the plan, phases, and the open-core boundary
- [CONTRIBUTING.md](./CONTRIBUTING.md) — build, the local `make check` gate, the CGO note
- [docs/DISTRIBUTION.md](./docs/DISTRIBUTION.md) — how releases are cut (tag → automated GitHub Actions)
- [CLAUDE.md](./CLAUDE.md) — architecture, conventions, and extension points (for contributors and AI agents)

## Contributing

Build from source, run the local checks (`make check`), open a PR — see
[CONTRIBUTING.md](./CONTRIBUTING.md). CI (GitHub Actions) also builds and tests on Linux and
macOS for every PR.

## License

MIT — see [LICENSE](./LICENSE).
