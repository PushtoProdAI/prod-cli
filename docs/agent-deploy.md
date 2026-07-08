# Your coding agent ships it

prod runs as a [Model Context Protocol](https://modelcontextprotocol.io) server (`prod mcp`),
so the coding agent you already use — Claude Code, Cursor, Cline — can deploy your app, check
its status, tail its logs, roll it back, and tear it down **without leaving the session**. You
describe intent in English to your agent; the agent calls prod; prod does the deploy against
**your own cloud account** with **your own credentials**. Nothing runs on a server, and every
destructive action is gated behind explicit human approval — the agent has to show you the plan
and the cost before it can ship anything.

## 1. What you get

Nine tools over stdio. Your agent can recall what it's shipped (`list_deploys`), read a
project's stack (`analyze_project`), self-check the environment (`doctor`), and — the
money verbs — `deploy`, `rollback`, and `destroy`, each of which **previews first and refuses
to execute until you approve**. Read-only helpers (`status`, `deep_link`, `logs`) let it report
where an app is running and how to watch it. It's the same tested deploy path as the `prod` CLI;
the MCP layer just drives it and enforces the approval gate.

## 2. Prerequisites

**Install prod** (macOS or Linux; on Windows use WSL2):

```bash
brew install pushtoprodai/tap/prod
# or:
curl -fsSL https://raw.githubusercontent.com/PushtoProdAI/prod-cli/main/scripts/install.sh | sh
```

**One LLM key** — prod parses your intent with your own model, direct, no proxy:

```bash
export OPENAI_API_KEY=sk-...          # or...
export ANTHROPIC_API_KEY=sk-ant-...   # or run a local Ollama (no cloud key needed)
```

With neither key set, prod falls back to a local Ollama at `http://localhost:11434`.

**One platform credential**, held the way that platform's own CLI holds it — a Fly token, your
`~/.aws` chain, a `RENDER_API_KEY`, a `heroku login`, and so on. prod never transmits these
anywhere; it uses them exactly like `flyctl` or `terraform` would.

**Verify before wiring anything up:**

```bash
prod doctor
```

`doctor` is read-only. It reports which LLM provider is configured and reachable, and whether
Docker is available (needed for container builds — Render and the AWS/Cloud Run/Azure targets).
It exits non-zero if no usable LLM is found, so `prod doctor && ...` short-circuits cleanly.

## 3. Wire it into your agent

The MCP server config is the same everywhere:

```json
{ "mcpServers": { "prod": { "command": "prod", "args": ["mcp"] } } }
```

**Claude Code** — one command:

```bash
claude mcp add prod -- prod mcp
```

or commit a `.mcp.json` at your repo root with the block above so your whole team gets it.

**Cursor** — drop the same block into `~/.cursor/mcp.json` (global) or `.cursor/mcp.json`
(per-project).

> ### The one gotcha that bites everyone
> **Your editor launches prod with the editor's environment, not your shell's.** If you export
> `OPENAI_API_KEY` and your Fly token in `~/.zshrc` but start Cursor from the macOS Dock (or
> Claude Code from a GUI launcher), prod sees **none of them** — deploys fail with "no LLM
> configured" or a credential error, and it looks like prod is broken when the env just didn't
> travel. Fix it by launching the editor from a terminal that has the vars exported, or by
> setting them where the editor can see them (a login-shell profile the GUI reads, or your
> editor's own env config). When in doubt, have the agent call `doctor` first — it reports the
> environment prod actually sees.

## 4. The tools

| Tool | What it does | Gated? |
|------|--------------|--------|
| `deploy` | Deploy the project in `path` from a natural-language request. | **Yes** — preview → confirm |
| `rollback` | Revert the most recent deploy on a `platform` to its previous version. | **Yes** — preview → confirm |
| `destroy` | Permanently delete a deployment and its resources (irreversible). | **Yes** — preview → confirm |
| `status` | An app's platform, last status, live URL, and whether it's currently responding. | Read-only |
| `deep_link` | The app's live URL + its platform-console (dashboard) URL. | Read-only |
| `logs` | The runnable platform-CLI command (`fly logs -a …`) + console URL for an app's logs. | Read-only |
| `list_deploys` | Recent deployments from local history (`~/.prod/history.json`), most recent first. | Read-only |
| `analyze_project` | Detect a project's language, build/start commands, and required services. | Read-only |
| `doctor` | Read-only environment self-check (LLM provider + Docker). | Read-only |

**The safety model.** `deploy`, `rollback`, and `destroy` all default to `confirm=false`, which
**previews and changes nothing** — it returns the plan (action, platform, deploy shape, a
summary, and estimated monthly cost) and stops. Only `confirm=true` executes, and only after a
human has seen that plan.

`deploy` adds a second lock: a preview returns a `planDigest`, and `confirm=true` must echo the
digest from a prior preview **of the same prompt and path**. The digest is salted per server
session, so an agent can't fabricate one — it is structurally forced to preview (and show you the
plan) before it can ship. `logs` deliberately returns the *command*, never raw log bytes: logs
can carry secrets, and a stdio tool shouldn't stream them.

## 5. Worked transcripts

These run against the current binary. Tool calls are shown as `tool(args) → result`.

### (a) Deploy a web app, with the approval gate

```
You →  "Deploy this to Fly."

Agent → deploy({ prompt: "deploy this to fly", confirm: false })
     ←  { status: "preview",
          plan: { action: "deploy", platform: "flyio", shape: "web",
                  summary: "Deploy Node web service to Fly.io, 1 machine",
                  estimatedMonthlyCostUsd: 5.7 },
          planDigest: "9f2c1a7b4e0d8f31" }

Agent → "Here's the plan: a Node web service on Fly.io, ~$5.70/month. Ship it?"

You →  "Yes."

Agent → deploy({ prompt: "deploy this to fly", confirm: true,
                 planDigest: "9f2c1a7b4e0d8f31" })
     ←  { deployed: true, status: "success",
          url: "https://your-app.fly.dev" }

Agent → "Live at https://your-app.fly.dev."
```

The agent could not have reached the second call without the `planDigest` from the first — which
it could only get by previewing.

### (b) Full lifecycle: deploy an MCP server, watch it, roll it back

An `mcp-server` deploy is only marked live after prod POSTs a JSON-RPC `initialize` handshake and
gets back a response advertising `serverInfo`, `capabilities`, or a `protocolVersion` — a plain
HTTP 200 is **not** enough, so a container that serves but doesn't actually speak MCP won't pass.

```
You →  "Deploy this MCP server to Fly."

Agent → deploy({ prompt: "deploy this mcp server to fly", confirm: false })
     ←  { status: "preview",
          plan: { action: "deploy", platform: "flyio", shape: "mcp-server",
                  summary: "Deploy MCP server to Fly.io", estimatedMonthlyCostUsd: 5.7 },
          planDigest: "b1d90c33aa27e6f4" }

You →  "Go."

Agent → deploy({ prompt: "deploy this mcp server to fly", confirm: true,
                 planDigest: "b1d90c33aa27e6f4" })
     ←  { deployed: true, status: "success", url: "https://your-mcp.fly.dev" }

--- later ---

You →  "Is it up, and how do I watch it?"

Agent → status({ app: "your-mcp" })
     ←  { found: true, platform: "flyio", status: "success",
          liveUrl: "https://your-mcp.fly.dev", live: "live", canRollback: true }

Agent → logs({ app: "your-mcp" })
     ←  { found: true, logsCmd: "fly logs -a your-mcp",
          consoleUrl: "https://fly.io/apps/your-mcp" }

Agent → "It's live. Tail it with: fly logs -a your-mcp"

--- a bad change ships ---

You →  "That broke it — roll back Fly."

Agent → rollback({ platform: "fly", confirm: false })
     ←  { status: "preview",
          plan: { action: "rollback", platform: "flyio",
                  summary: "Revert your-mcp to the previous release" } }

Agent → "This reverts your-mcp to its previous Fly release. Confirm?"

You →  "Do it."

Agent → rollback({ platform: "fly", confirm: true })
     ←  { rolledBack: true, status: "success" }
```

### (c) The guardrail is real: a refused confirm

If an agent tries to skip the preview — or reuses a stale digest — the server refuses:

```
Agent → deploy({ prompt: "deploy this to fly", confirm: true })   # no prior preview

     ←  status: "preview-required"
        error: "preview first: call deploy with confirm=false to get a planDigest,
                show the human the plan + estimated cost, then call again with
                confirm=true and that planDigest"

Agent → deploy({ prompt: "deploy this to fly", confirm: false })
     ←  { status: "preview", plan: { ... }, planDigest: "77aa10c9de23bb05" }

Agent → "I need your sign-off first — here's the plan and the cost. OK to deploy?"

You →  "Approved."

Agent → deploy({ prompt: "deploy this to fly", confirm: true,
                 planDigest: "77aa10c9de23bb05" })
     ←  { deployed: true, status: "success", url: "https://your-app.fly.dev" }
```

There is no code path from "the agent wants to deploy" to a running deploy that doesn't pass
through a preview a human can see.

## 6. Troubleshooting

**"this deploy needs interactive input (e.g. environment-variable values); run it from the CLI:
prod …"** — the deploy hit a prompt (an unset environment variable, an auth step) that a
headless MCP call can't answer. Run that exact `prod "…"` command in your terminal once to supply
the values, then let the agent drive subsequent deploys.

**"deploy produced no result (is an LLM configured? run `prod doctor`)"** — prod couldn't parse
your intent because no LLM is reachable. Almost always the [env-inheritance
gotcha](#the-one-gotcha-that-bites-everyone): the key is in your shell but not in the environment
your editor launched prod with. Have the agent call `doctor`, or run `prod doctor` from the same
context, and set `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` (or start Ollama) where the editor can
see it.

**`status: "preview-required"` / "preview first: …"** — expected, not a bug. `deploy` with
`confirm=true` requires the `planDigest` from a prior `confirm=false` preview of the *same* prompt
and path. Preview, show the human the plan and cost, then confirm with that digest. (The digest
resets each time the MCP server restarts, so a digest from a previous session won't work —
re-preview.)

**A platform credential error on deploy, but the CLI works fine** — same env-inheritance issue,
from the credential side. Your Fly token / `~/.aws` / `RENDER_API_KEY` reached your shell but not
the editor's environment. Launch the editor from a terminal that has them, or set them where the
editor reads its environment.

**`status`/`logs`/`deep_link` returns "identifier not recorded — redeploy to enable console +
logs links"** — that deploy predates the metadata prod now records per platform. Redeploy once and
the console URL and logs command will resolve.
