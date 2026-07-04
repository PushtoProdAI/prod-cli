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

Requires **Go 1.24+** and a C toolchain to build from source (`prod` links a native
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
| `prod auth ...` | Sign in to the optional managed tier — **not needed** for local use |

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

---

## Supported platforms

Today, `prod` deploys directly to:

- **Fly.io**
- **Render**
- **Vercel**
- **Netlify**
- **Heroku**

**AWS** (App Runner + ECS/Fargate + RDS) is being ported to run entirely in the binary with
your own AWS credentials — see the [ROADMAP](./ROADMAP.md).

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
