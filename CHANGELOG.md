# Changelog

Notable changes to prod. Format based on [Keep a Changelog](https://keepachangelog.com/).
prod is pre-1.0 — the surface may still change.

## [0.2.8] - 2026-07-08

### Added
- Deploy **shape** (`web` | `mcp-server` | `worker` | `cron`) now reaches artifact generation,
  not just liveness. Worker/cron deploys to Fly.io are built as **portless processes** (no HTTP
  service, no health check), so a non-HTTP agent/worker is no longer failed by the platform's
  own health check. (Foundation for first-class agent deploys — see `docs/dx-roadmap.md`.)

## [0.2.1 – 0.2.7] - 2026-07-06/07

Reliability fixes surfaced by dogfooding real deploys:

### Fixed
- Respect the project's package manager — detect pnpm/yarn/bun lockfiles and skip `npm install`
  (which looped silently on a pnpm workspace); surface the real error and stop instead of retrying.
- Never echo secret env-var values to the console (masked as "set, hidden").
- Skip platform-managed vars (`NODE_ENV`, `NEXT_RUNTIME`, `NEXT_PHASE`, `VERCEL_*`, …) instead of
  prompting for them; print a post-deploy checklist of vars to set once the app is live.
- Don't fail a deploy running migrations when no `DATABASE_URL` is available — skip with
  instructions to connect a database and run them.
- Don't create **empty** env vars on Vercel — an empty `DATABASE_URL` collided with the
  Postgres/Neon integration and had to be deleted by hand.
- Revert durable workflow state to in-memory — a persisted backend silently **resumed** an
  interrupted deploy on the next unrelated `prod` run, causing a surprise double-deploy.

### Changed
- Extracted the provider-plugin SDK to its own module, `github.com/pushtoprodai/prod-plugin-sdk`,
  so plugins are `go get`-able; narrowed the plugin env deny-list so plugins keep their own cloud
  tokens (e.g. `DIGITALOCEAN_TOKEN`); pointed the plugin index at the `prod-plugins` repo.

## [0.1.0] - 2026-07-06

### Added
- Deploy to **eight clouds** from a natural-language request, with your own credentials:
  Fly.io, Render, Vercel, Netlify, Heroku, AWS App Runner, Google Cloud Run, Azure
  Container Apps. See [docs/clouds.md](docs/clouds.md).
- **Cloud-adapter framework** — a registration catalog (adding a cloud is one entry), a
  shared managed-container base, and **out-of-tree provider plugins**: add your own cloud
  or internal PaaS as a separate binary with `prod plugin install`, no fork required. See
  [docs/plugins.md](docs/plugins.md).
- **Rollback** on every revision-keeping cloud — Fly/Render/Heroku (native), Cloud Run and
  Azure Container Apps (route traffic to the previous revision).
- **Encrypted secrets** on App Runner and Azure Container Apps (sensitive env vars stored as
  the cloud's secrets, not plain config).
- **Cost previews** for the managed-container clouds.
- **Agent-native:** `prod mcp` — a stdio MCP server exposing `deploy`, `rollback`, `doctor`,
  `list_deploys`, `analyze_project` behind a human-approval gate — plus a `deployShape` model
  (web / mcp-server / worker / cron) with shape-aware liveness.
- `prod doctor`, `--dry-run` with a cost preview, `--yes` for CI.
- Cross-platform CI (Ubuntu + macOS) and a tagged-release pipeline producing Linux + macOS
  binaries for the install one-liner.

### Security
- Credentials stay local and are your own — nothing is sent to a prod backend (there isn't
  one). LLM calls go direct to OpenAI / Anthropic / local Ollama with your key.
- Plugins run in a subprocess with a **curated environment** (prod's own platform tokens and
  LLM keys are withheld), **mTLS**, and **checksum-pinned** binaries.
