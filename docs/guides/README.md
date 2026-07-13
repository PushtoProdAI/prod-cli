# Guides

**Goal-shaped, start-to-finish walkthroughs.** Each guide answers a single "how do I *X*?" — pick
the outcome you want, follow the numbered steps, and you're done. Guides are the how-to layer of
prod's docs; they sit between the reference (every flag and platform) and the concept pages (why
things work the way they do).

## What a guide is (and isn't)

Following [Diátaxis](https://diataxis.fr/), prod's docs have four modes. This section is **how-to**:

| Mode | Answers | Example | Where |
|------|---------|---------|-------|
| **How-to guide** | "How do I accomplish *X*?" | "Roll back a bad deploy" | **this section** |
| **Reference** | "What are the flags/options for *Y*?" | Commands, Configuration, Clouds | website reference pages |
| **Concept** | "Why does prod work this way?" | Deploy shapes, MCP | website concept pages |
| **Tutorial** | "Teach me from zero" | Quickstart | Quickstart |

A guide is outcome-first and opinionated: it names one realistic goal, lists prerequisites, gives
numbered steps with **real `prod …` commands and realistic output**, says what success looks like,
and warns about the pitfalls that actually bite. When a guide needs the full flag list or the
per-cloud credential matrix, it **links out** to reference rather than restating it.

## The guides

Read in order for a tour, or jump to the one you need.

**Tier 1 — the core deploy workflow**

1. [Bring your own LLM](./bring-your-own-llm.md) — point prod at OpenAI, Anthropic, or a local
   Ollama; how provider selection and `PROD_LLM_MODEL` work.
2. [Environment variables & secrets](./environment-variables-and-secrets.md) — supply config and
   secrets; how sensitive values route to the platform secret store; the post-deploy checklist.
3. [Add a database](./add-a-database.md) — which platforms provision Postgres/Redis vs expect a
   bring-your-own `DATABASE_URL`; how connection strings and migrations flow.
4. [Roll back a bad deploy](./roll-back-a-bad-deploy.md) — on-demand rollback (native vs
   image-swap per cloud) and the shape-conditional automatic health-check rollback.
5. [Tear down a deployment (destroy)](./tear-down-a-deployment.md) — what destroy removes vs leaves
   behind, and the cost-hygiene cleanup for orphaned databases and images.

**Tier 2 — the differentiator**

6. [Deploy an AI agent or background worker](./deploy-an-agent-or-worker.md) — ship a URL-less,
   non-HTTP worker with no false health-check rollback; how it appears in `prod ls` / `open` /
   `logs`. **The flagship guide.**

### Why this order

Tier 1 is the spine every user walks: you need an LLM (1) before anything runs, almost every real
app needs config/secrets (2) and often a database (3), and rollback (4) and destroy (5) are the
lifecycle operations you reach for right after your first deploy. Tier 2 is prod's reason to exist
— deploying agents and workers, not just web apps — and builds on the shape model that guides 4 and
6 both lean on.

## How guides relate to the rest of the docs

- **Reference** (Commands, Configuration, Clouds, Languages): the exhaustive "what." Guides link
  here for the full flag list, the per-cloud credential matrix, and supported languages.
- **Concept** (Deploy shapes, MCP): the "why." The agent/worker guide (6) and rollback guide (4)
  depend on the deploy-shape concept; the env/secrets guide (2) and agent guide reference the MCP
  approval model.
- **Existing how-to-shaped docs already in `docs/`** — [agent-deploy.md](../agent-deploy.md) (MCP)
  and [pr-previews.md](../pr-previews.md) (CI) predate this section and read like guides. They stay
  where they are; the new guides link to them rather than duplicating them.

## Proposed nav placement

Current website nav:

> Introduction → Installation → Quickstart → Templates → Languages → Interactive UI → Commands →
> Configuration → Clouds → PR previews → MCP → Deploying agents & workers → Provider plugins →
> Changelog

**Recommendation:** add a **Guides** group (or top-level entry) **immediately after Quickstart**,
before the reference cluster — so a reader who just finished Quickstart lands on goal-shaped
how-tos before hitting exhaustive reference. Proposed nav:

> Introduction → Installation → Quickstart → **Guides** *(Bring your own LLM · Environment
> variables & secrets · Add a database · Roll back a bad deploy · Tear down a deployment · Deploy
> an AI agent or worker)* → Templates → Languages → Interactive UI → Commands → Configuration →
> Clouds → PR previews → MCP → Deploying agents & workers → Provider plugins → Changelog

Notes for the website port:

- Guide #6 (Deploy an AI agent or worker) overlaps the existing **"Deploying agents & workers"**
  nav item, which is the *concept* page for deploy shapes. Keep both: the concept page explains the
  shape model; the guide walks a worker deploy end to end and links to the concept page. Consider
  renaming the concept page to **"Deploy shapes"** to disambiguate.
- Guides cross-link to reference pages by slug (`../commands.md`, `../shapes.md`,
  `../configuration.md`, `../languages.md`). Those are **website pages, not files in `docs/`** — wire
  the links to the corresponding nav entries during the port. Links to real `docs/` files
  (`../clouds.md`, `../agent-deploy.md`, `../pr-previews.md`, `../plugins.md`) resolve as-is.

## Queued for the next batch

Not written yet; recommended order for a follow-up pass:

- **Deploy a hosted MCP server** — the `mcp-server` shape end to end, the `initialize` liveness
  handshake, and the copy-paste `mcpServers` config block. (Partly covered by
  [agent-deploy.md](../agent-deploy.md); a dedicated guide would complete the shape trio.)
- **Schedule a job (cron)** — realistic cron on Render / Modal, and the honest degradation to a
  worker elsewhere.
- **Deploy from CI** — generalize [pr-previews.md](../pr-previews.md) into `prod run --yes` +
  `PROD_JSON_MODE` for any pipeline (deploy on merge, not just PR previews).
- **Trust & credentials** — the local-tool security model: where creds live, what never leaves your
  machine, the `~/.prod` files.
- **Troubleshooting** — the env-inheritance gotcha, `prod doctor`, "needs interactive input,"
  common credential errors — consolidated.
- **Choosing a cloud** — a decision guide (PaaS vs managed-container vs static), plus focused
  per-cloud setup walkthroughs.
- **Language/framework guides** — Rails, Next.js, FastAPI, Spring — the top stacks, each a
  zero-to-deployed walkthrough that also surfaces framework-managed env vars and migrations.
