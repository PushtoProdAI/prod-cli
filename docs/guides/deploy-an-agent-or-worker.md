# Deploy an AI agent or background worker

**What you'll accomplish:** ship a program that has **no web server and no public URL** — an
autonomous LLM agent, a queue consumer, a polling loop — and have prod treat it correctly: no false
"is the URL live?" check, no bogus auto-rollback because it never answered an HTTP probe, and a
sensible entry in `prod ls` / `prod open`.

This is prod's differentiator. Most deploy tools assume "app" means "web app" and gate success on an
HTTP 200. A background worker never serves HTTP, so that assumption would fail every healthy worker
and roll it back. prod picks a **deploy shape** and applies the right liveness strategy for it.

> **Maturity note.** The two HTTP shapes — **web** and **mcp-server** — are the launch shapes and
> are fully supported. The non-HTTP **worker** and **cron** shapes are an actively evolving area
> (ROADMAP Phase 2): shape detection, the skip-the-HTTP-probe behavior, and the `prod ls`/`open`
> handling described here are implemented today, while richer process-level/log-heartbeat liveness
> and portless artifact generation are still landing. Where behavior is partial, this guide says so.
> If you deploy a worker and something looks web-shaped, that's the gap — check
> [ROADMAP.md](../../ROADMAP.md).

## The four deploy shapes

prod classifies every deploy into one shape, which decides how it verifies the deploy is up:

| Shape | What it is | Liveness check |
|-------|------------|----------------|
| `web` | serves HTTP | GET the URL — any response that isn't a 5xx or connection failure is "live" |
| `mcp-server` | an MCP server over HTTP | GET isn't enough — must complete a JSON-RPC `initialize` handshake |
| `worker` | a continuous non-HTTP process (agent loop, consumer) | **no HTTP probe** — liveness is the platform's concern |
| `cron` | a scheduled/periodic job | **no HTTP probe** (see scheduling caveats below) |

The key line for this guide: **for `worker` and `cron`, prod skips the HTTP liveness check
entirely.** It does not GET a URL, so it never fails a worker for "not responding," and the
health-check auto-rollback that guards web deploys simply doesn't apply. A healthy worker stays
green.

## How prod picks the shape (you usually don't have to say)

Shape is decided from two signals, with **code winning over words**:

1. **Your words.** The LLM reads your request — "deploy this **worker**," "run this agent," "every
   night at 2am" — and proposes a shape.
2. **Your code (authoritative).** prod's analyzer inspects your dependencies. A conclusive signal
   overrides the LLM:
   - an **MCP SDK** (`mcp`, `fastmcp`, `@modelcontextprotocol/sdk`, …) ⇒ `mcp-server`
   - an **agent framework with no web server** (LangChain, LangGraph, CrewAI, AutoGen, LlamaIndex,
     Agno, smolagents, OpenAI Agents SDK, Mastra, …) ⇒ `worker`
   - a **web server dependency** present (FastAPI, Flask, Django, Express, Next, Rails, Axum,
     Spring Boot, Phoenix, ASP.NET, …) keeps it `web` even if an agent framework is also present

So a LangChain script with no FastAPI is detected as a worker from the code alone — you don't have
to remember to say "worker." A LangChain app that *also* runs FastAPI stays `web` (it has a URL to
serve and to probe).

You can see the chosen shape in the plan before you approve, and in `prod ls`.

## Deploy it

Deploying a worker is the same one-liner as anything else — prod does the shape work for you:

```bash
prod "deploy this agent to fly"
```

A representative run for a URL-less agent:

```
🔍 Analyzing project… detected Python, agent framework (langchain), no web server
📦 Plan: deploy worker to Fly.io  ·  ~$5.70/month
   Shape: worker (no public URL — background process)
Proceed? [y/N] y
…
✅ worker deploy — no HTTP liveness check
✅ Deployed to Fly.io
```

That `✅ worker deploy — no HTTP liveness check` line is the whole point: prod recognized there's no
URL to probe and didn't invent one. Compare a `web` deploy, which ends with `✅ URL is live` after
GETting the URL, and an `mcp-server` deploy, which ends with `✅ MCP server answered initialize`.

**Set the agent's secrets the usual way.** An agent almost always needs `OPENAI_API_KEY` /
`ANTHROPIC_API_KEY` and other provider keys at *runtime* — these are the app's own env, separate
from the key prod uses to parse your request. prod prompts for them (or accept them headlessly with
`--env`), and because they look sensitive they route to the platform's encrypted secret store. See
[Environment variables & secrets](./environment-variables-and-secrets.md).

## How a worker shows up afterward

A worker has no public URL by design, and prod's launcher commands handle that gracefully instead
of showing a broken empty cell:

**`prod ls`** — the URL column shows the shape, not a blank:

```
NAME                 PLATFORM         STATUS    AGE      URL
nightly-agent        flyio            ✅ ok     3m       worker
```

**`prod open nightly-agent`** — there's no live URL to open, so prod says so and falls back to the
platform console:

```
nightly-agent has no public URL (it's a worker). Opening the console instead…
```

(For a `cron` shape it says "it's a cron job.") If there's no console link recorded either, it
points you at the logs instead of erroring.

**`prod logs nightly-agent`** — this is how you actually confirm a worker is doing its job. Since
there's no URL to hit, logs are your liveness signal. prod prints and runs the platform's own logs
command (e.g. `fly logs -a nightly-agent`):

```bash
prod logs nightly-agent
```

```
$ fly logs -a nightly-agent
2026-07-12T18:03:11Z app[…] processed 12 tasks, sleeping 60s
```

## Cron: read this before you rely on a schedule

prod **never shows you a schedule it won't actually honor.** Whether a `cron` shape truly runs on a
schedule depends on the platform:

- **Render** — supports real scheduled jobs. Give a parseable 5-field cron expression (say "every
  night at 2am" and prod derives `0 2 * * *`); prod deploys it as a scheduled job. If it can't
  parse a schedule, it **degrades to a continuous worker** and tells you: *"No schedule detected —
  deploying as a continuous worker."*
- **Modal** — the schedule lives in your Python function decorator
  (`@app.function(schedule=modal.Cron("0 2 * * *"))`), so prod deploys your code as-is and doesn't
  invent a schedule. prod reminds you where the schedule belongs.
- **Every other platform (Fly, AWS, Cloud Run, Azure, …)** — prod can't run a scheduled job through
  these for you, so a `cron` request **degrades to a continuous worker** with an honest message:
  *"<platform> can't run scheduled jobs through prod — deploying as a continuous worker. (Render
  supports real cron jobs.)"*

The design rule: if prod can't express your schedule on the target, it won't claim `Schedule: 0 2 *
* *` and then quietly run 24/7 — it degrades to a worker and says so. Pick **Render** (or **Modal**
with a decorator) when you need a genuine schedule.

## What success looks like

- The plan shows `Shape: worker` (or `cron`) before you approve.
- The deploy finishes with `✅ worker deploy — no HTTP liveness check` — not an HTTP probe.
- `prod ls` shows the app with `worker`/`cron` in the URL column and `✅ ok` status.
- `prod logs <app>` streams the process's output, which is your real health signal.
- No auto-rollback fires just because the worker didn't answer HTTP.

## Common pitfalls

- **You expected a URL.** Workers don't get one — that's correct. Use `prod logs` to verify it's
  running, and `prod open --console` to reach the platform dashboard.
- **You wanted a schedule but deployed to Fly/AWS.** It ran as a continuous worker (prod told you
  in the output). Redeploy to Render, or put the schedule in a Modal decorator.
- **Your agent also serves HTTP.** Then it's a `web` deploy, not a worker — prod keeps it `web` when
  it detects a web-server dependency, and it *will* HTTP-probe. If you want it treated as a worker,
  remove/avoid the web-server dependency or deploy the non-HTTP entrypoint.
- **The worker "deployed" but does nothing.** A successful deploy means the process started, not
  that your logic is correct — prod can't probe behavior it can't see. Watch `prod logs` for the
  first cycle of real work.
- **Non-HTTP support is still maturing.** If shape detection mislabels your project, name the shape
  explicitly in your request ("deploy this **worker**") — the LLM signal still feeds in — and file
  it against ROADMAP Phase 2.

## See also

- [Deploy shapes](../shapes.md) *(concept)* — the shape model in depth.
- [Your coding agent ships it](../agent-deploy.md) — deploying via MCP, including the `mcp-server`
  shape and its `initialize` handshake.
- [Environment variables & secrets](./environment-variables-and-secrets.md) — routing the agent's
  own provider keys to the secret store.
- [Roll back a bad deploy](./roll-back-a-bad-deploy.md) — why auto-rollback is shape-conditional.
