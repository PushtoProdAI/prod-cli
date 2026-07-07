# Changelog

Notable changes to prod. Format based on [Keep a Changelog](https://keepachangelog.com/).
prod is pre-1.0 — the surface may still change.

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
