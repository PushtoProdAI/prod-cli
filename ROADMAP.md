# prod — Open Source Roadmap

**Deploy code and agents with natural language. One binary. No backend. MIT.**

The plan to take `prod` from a hosted SaaS to an open-source standard for "describe it, deploy
it" — and to make it the deployment primitive AI coding agents reach for. Reflects decisions
made 2026-07 and a code-grounded technical/architectural/UX review.

> Companion: [CLAUDE.md](./CLAUDE.md) (contributor map) · [docs/design.md](./docs/design.md)
> (legacy deep reference — describes the SaaS backend being retired from the OSS core).

---

## Decisions locked

| Decision | Choice |
|---|---|
| **License** | **MIT** |
| **Repository** | fresh public repo **`prod-cli`**, scrubbed history — the CLI only, no backend code |
| **Name** | keep **`prod`** (binary + project); trademark policy to follow |
| **Architecture** | **single self-contained Go binary. Local state. No backend, no database, no Supabase in the OSS core.** |
| **Agents** | AI deployment story ships **in the launch** |
| **Commercial line** | the multi-tenant hosted backend is the *only* paid/optional component; the tool is complete without it |

---

## The core idea: collapse to one binary

Today `prod` sprawls across three languages (Go CLI, Deno Edge Functions, Node Lambda) and a
six-service Supabase backend. **Almost none of that is needed to deploy an app.** It exists to
serve the *multi-tenant SaaS model* — deploying other people's apps from a central service
without holding their credentials. Strip that assumption out of the open-source core and the
whole system collapses to a single binary.

The principle: **the OSS tool has no backend.** It runs entirely on the user's machine, holds
state locally, and talks directly to each platform — exactly like `terraform`, `pulumi`, `flyctl`,
or `aws sam`. A server reappears only for the *commercial* hosted tier (teams, shared history,
metering), where multi-tenancy genuinely needs one.

| Concern | Today (SaaS) | OSS core (single binary) |
|---|---|---|
| LLM calls | proxied through an Edge Function (metering) | **direct** to OpenAI / Anthropic / Ollama with the user's key |
| Deploy history | Postgres table | **local SQLite** in `~/.prod` |
| Fly / Render / Vercel / Netlify / Heroku | CLI → Edge Function → platform | **CLI → platform** directly with the user's token |
| **AWS** | Edge Functions `AssumeRole` into a central prod account | **CLI → AWS directly with the user's own creds** (Go AWS SDK already vendored) |
| CFN templates / Lambda | hosted in an S3 bucket | **embedded in the binary** (`go:embed`) |
| Auth | browser OAuth to the hosted service | **none** — platform tokens live locally in `~/.prod` |
| Durable retries | go-workflows on Postgres | go-workflows on **local SQLite** — same resumability, no server |

**Result:** one language, one process, one stack trace. `git clone && go run`. Fewer moving
pieces to debug, nothing to stand up to contribute — and *faster*, because every LLM call and
deploy step drops a network hop.

---

## Thesis

Anyone can now *generate* a working app in an afternoon. Almost no one can *ship* one without
fighting six dashboards, a Dockerfile, and an IAM policy. That gap is the wedge. `prod` wins on
the axis incumbents can't pivot to:

1. **Natural language**, not YAML — English → a typed, reviewable plan (via BAML).
2. **Platform-neutral**, not lock-in — one grammar, many clouds; the Rosetta Stone for deploy.
3. **Agent-native** — the AI writes the app; `prod` is how it goes live, exposed as an MCP tool.

Be the verb every coding agent calls to ship, and be genuinely useful to a solo developer with
nothing but the binary and their own API keys.

---

## The open-core boundary

Clean and simple, because the architecture makes it clean:

### Open (MIT) — the binary
Everything needed to deploy from your own machine:
- The entire CLI + TUI: intent parsing, planning, the FSM, every platform adapter.
- **Direct LLM** — OpenAI, Anthropic, or local Ollama with your key. No proxy.
- **Local state** — deploy history + resumable workflows in a local SQLite file.
- **Direct-to-platform deploys**, including **AWS with your own credentials** (embedded templates).
- The **MCP server** (same binary, `prod mcp`) and the platform-adapter SDK.
- Agent-native deploy shapes and scaffolds.

### Commercial (optional hosted service) — the backend
The tool is complete without this. It's the business, not a dependency:
- Multi-tenant hosted backend: shared team deploy history, RBAC, audit, SSO.
- Managed cross-account AWS (deploy *on your behalf* without you holding creds — the current
  Deno/CFN/STS control plane, run for you).
- Metered LLM with spend controls; web dashboard; preview environments; drift & cost insight.

Two run modes, and the default needs no account:

| Mode | Needs an account? | State | For |
|---|---|---|---|
| **local** (default) | no | local SQLite, BYO keys | everyone; the product |
| **managed** (opt-in) | yes | synced to the hosted backend | teams wanting shared history/SSO/metering |

There is **no "self-hosted backend" tier to operate** — that entire category of complexity is
gone. "Self-hosted" just means "you ran the binary."

---

## What the review found (and how this architecture answers it)

The code-grounded review flagged these; the single-binary pivot resolves most by deletion:

- **"Deploy is backend-mandatory at three layers"** (boot fatal, forced OAuth, `logDeploymentStart`
  as step one of every workflow) → **removed** in Phase 0: local state, no auth gate, no boot dep.
- **"AWS is a hosted control plane; self-host means standing up a whole SaaS"** → **removed**: AWS
  moves into the CLI using the user's own creds (Phase 2 port). No central account, no S3 bucket.
- **"BYO-keys is `WithClientRegistry`, not regeneration; today no-backend = broken LLM"** →
  Phase 0 builds the registry so direct LLM is the default path.
- **Still true and still handled:** success = HTTP 200 today (so non-HTTP worker agents are a
  later shape), Node/Python-only analysis, the **forked go-workflows** engine, and **CGO
  cross-compile already blocking Linux/Windows** in the existing darwin-only `.goreleaser.yml`.

---

## Phases

Timeboxes assume a small team and are a *sequence*, not a calendar. Each phase gates the next.

### Phase 0 — Collapse to one binary `[GATE — blocks everything]`  · ~Weeks 1–3

Make the OSS core a self-contained binary that deploys with no backend and no account. This is
the load-bearing work.

**Sever the backend from the deploy path** — ✅ done

- [x] **Local state store** — JSON history at `~/.prod/history.json` (`internal/history`); a file
      the user can read beats a database for a single-user CLI. `logDeploymentStart` /
      `updateDeploymentStatus` / `sendProjectStats` and `/deploys` route through it in local mode.
      (go-workflows keeps its own sqlite for durable workflow state.)
- [x] **Boot with no backend** — removed the `SUPABASE_*` requirement / `log.Fatal`; runs standalone.
- [x] **Drop the auth gate** — `ensureAuthenticated` short-circuits in local mode; only the target
      platform's credentials matter, read locally.
- [x] **Direct LLM by default** — BAML `ClientRegistry` in `llm.getCallOptions` selects OpenAI /
      Anthropic / Ollama at call time (no `.baml` edits, no regen); `PROD_LLM_MODEL` override.

**Legalize & de-identify**
- [ ] **Secret sweep & rotate** — purge `.env`; scan **full git history** (a real JWT anon key is
      present); **rotate the Supabase anon + service-role keys and the Sentry DSN**. A fresh repo
      does not protect old history.
- [ ] **MIT `LICENSE`** + `NOTICE` + `SECURITY.md`; remove the 1Password reference from docs.
- [x] **Rename module** `github.com/meroxa/prod/cli` → `github.com/pushtoprodai/prod-cli` across
      all sources, `go.mod`, `baml_src/generators.baml`, `cli/Makefile`, and `.goreleaser.yml`
      (ldflags + `owner`). Build + tests green under the new path.
- [x] `config.go` — hard-coded Supabase ref removed; backend resolved from `PROD_BACKEND_URL` /
      `SUPABASE_URL` env or ldflags. **Remaining:** `scripts/install.sh` still points at the old
      Supabase storage bucket and needs reworking for the GoReleaser/brew distribution.

**De-risk the toolchain (spikes with their own proof)**
- [ ] **CGO cross-compile** — BAML is a native dep; the existing `.goreleaser.yml` is darwin-only
      because Linux/Windows cross-compile is broken. Fix with native per-OS CI runners. The
      clean-room exit needs a Linux binary, so this is a gate.
- [ ] **`go-workflows` fork** — re-home under `github.com/pushtoprodai/go-workflows` with a
      **tagged release** (or upstream the patch); pin to a tag; document the delta. It's the last
      big "moving piece" — keep it (resumable deploys are real UX) but own it.

**Repo split** — ✅ done
- [x] **`prod-cli` = the binary only.** The Supabase/Deno backend, `infra/`, and `lambda/` were
      removed from the tree (preserved in git history + `docs/design.md`). The repo is now `cli/`
      + docs + templates + local-first tooling.

**Exit:** on a machine with **no account and no backend**, `go build` produces a **Linux** binary
that deploys a real app to **Fly.io** — proven by a CI test with all `SUPABASE_*` unset asserting
no fatal, no browser, real deploy.

---

### Phase 1 — Public launch: "deploy code *and* agents in one line"  · ~Weeks 4–8

Launch the single binary with the direct-API platforms and the agent story. **AWS is not in this
phase** (it's the Go port in Phase 2) — the five direct-API platforms carry the launch.

**Distribution & onboarding**
- [ ] Install in one line: `curl | sh`, Homebrew tap, Scoop, `docker run`, signed binaries
      (**cosign + SLSA**) via **GoReleaser** — Linux/Windows restored by the Phase-0 CGO fix.
- [ ] **Two docs, not the current stitched README:** `README.md` (install → first deploy in
      <5 min, no account) and `CONTRIBUTING.md` (build, CGO caveat, the output-writer rule, the
      two meanings of "agent"). No `SELF-HOSTING.md` needed — there's no backend to host. Delete
      the old `fmt.Fprintf` "Output Pattern Guide" (it contradicts CLAUDE.md §6).
- [ ] Opt-in, transparent telemetry (off by default).

**First-deploy UX (small but launch-critical)**
- [ ] **Fix "deploy this" with no platform** — today `DEPLOY + UNKNOWN` dead-ends into prose.
      Route it into a platform picker (reuse the rollback-only `selectPlatform`), ranked by what
      the analyzer detected, with a per-project default.
- [ ] **`ConsoleWriter` parity + fix the console-mode panic** (`out.(TUIWriter)` assertion). Drive
      TUI/console/JSON from one canonical event; add a golden cross-writer test (also the MCP
      anti-drift guarantee).

**The AI / agent deployment story**
- [ ] **`prod mcp`** — MCP server over the existing JSON event stream as a **stateful adapter**
      (session/correlation, not a thin passthrough). Tools `deploy`/`plan`/`status` map to
      existing flows; **`rollback`/`estimate_cost`/`list_deploys`/`tail_logs` are net-new**.
      stdio + streamable HTTP.
- [ ] **Design the approval state machine first** (see MCP section): a real `confirm` param + a
      `--yes` flag (neither exists today).
- [ ] **Two HTTP-shaped launch shapes:** a **web app** and a **hosted MCP server** (deploy an MCP
      server to a URL in one line). Both fit the existing HTTP liveness model. Add a `deployShape`
      field on `Intent`/`DeploymentSpec` (`web` | `mcp-server`, later `worker` | `cron`). The
      non-HTTP worker is deferred to Phase 2.
- [ ] **Flagship demo** (recorded with **VHS**): an agent writes an app, then deploys it with
      `prod`. For the MCP-server shape, success output includes the live URL **plus a
      copy-pasteable `mcpServers` config block**.

**Launch beats** — Show HN + Product Hunt + a "why we open-sourced prod" post.

**Exit:** a stranger installs the binary and deploys an app **and** a hosted MCP server with no
account; "deploy this" works hands-free inside Claude Code / Cursor.

---

### Phase 2 — AWS in the binary, deeper agents & ecosystem  · ~Weeks 8–16

Land the one platform that needs a real port, plus the deeper agent runtimes.

- [ ] **AWS deploy in-CLI (Go)** — port the CloudFormation generation + ECR + ECS/App Runner
      orchestration from the Deno Edge Functions into the CLI, using the **user's own AWS creds**;
      **embed the templates and the `database-url-constructor` Lambda** in the binary (`go:embed`).
      No central account, no S3 bucket, no ExternalId dance. This deletes the largest remaining
      backend surface from the user's world.
- [ ] **True non-HTTP shapes** — long-running **workers**, **cron**, **queue** consumers: a
      shape-aware liveness model (process-running / log-heartbeat instead of HTTP 200), portless
      artifact generation, and health-check auto-rollback made conditional on shape.
- [ ] **Agent framework detectors** — LangGraph, CrewAI, OpenAI Agents SDK, Mastra; agent secret
      roles (`ANTHROPIC_API_KEY` etc.) as first-class env-var handling.
- [ ] **More languages** — Go, Ruby, etc. (today: Node + Python only — state this in the README).
- [ ] **Adapter SDK**, **GitHub Action** (`prod deploy` in CI), **preview environments**, a
      searchable **docs site** (Nextra/Mintlify), VS Code polish + JetBrains.

**Exit:** AWS deploys from the binary with the user's own creds; a community adapter merges
without core-team help; a non-HTTP worker agent deploys and stays green.

---

### Phase 3 — The commercial hosted tier  · Quarter 2+

The optional backend — the business. Everything a solo user does stays free and local.

- [ ] Hosted multi-tenant backend GA: shared team history, RBAC, SSO, audit.
- [ ] Managed cross-account AWS (deploy-on-your-behalf) for teams that don't want creds on laptops.
- [ ] Metered LLM + spend controls; web dashboard; preview environments as a service.
- [ ] Public roadmap + RFCs; trademark policy; sponsorship/OpenCollective; case studies.

**Exit:** first paying team converts from the open tool.

---

## The MCP server (design)

The substrate is real: `PROD_JSON_MODE` already emits structured deploy events and `prod run`
reads stdin back into the agent. But the server is a **session-managing adapter**, not a thin
passthrough — the event stream is emit-only and untyped, approval is a single event + a stdin
line, and four of the seven tools have no flow yet. Ships as `prod mcp` in the same binary.

| Tool | Purpose | Status |
|---|---|---|
| `deploy(prompt, path?, platform?)` | NL deploy; streams plan → approval → progress → URL | existing FSM |
| `plan(prompt, path?)` | Dry-run: typed plan + cost estimate, no side effects | existing FSM |
| `status(resource?)` | Health & current state | existing FSM |
| `rollback(resource, target?)` | Revert to a previous release | **net-new** |
| `estimate_cost(prompt, path?)` | Monthly cost projection | **net-new + see perf note** |
| `list_deploys(filter?)` | History (from the local store) | **net-new** |
| `tail_logs(resource)` | Stream runtime logs into the agent's context | **net-new** |

**Resources:** `prod://deployments`, `prod://project/analysis`. **Transport:** stdio + streamable
HTTP. **Auth:** none in local mode (platform creds are local); session only in managed mode.

**Approval — design before coding.** MCP calls are request/response with no human at the CLI's
stdin, so the current "block on stdin for `approved`" model has no counterpart. Spec it: a
`confirm: bool` parameter + an out-of-band affordance (**MCP elicitation** where supported, with
a **`plan(...)` → `deploy(..., confirm:true)`** two-call fallback). Unify TUI/JSON/MCP on one
plan event; fix the `out.(TUIWriter)` panic. `deploy`/`rollback` require explicit approval by
default; an agent passes `confirm`/`--yes` (to be built) to skip.

```jsonc
{ "mcpServers": { "prod": { "command": "prod", "args": ["mcp"] } } }
```

---

## Performance & correctness watch-items

- **Cost estimation is a latency/cost trap.** Except AWS, pricing is a live web-scrape + an LLM
  call per service, per estimate, uncached (`pricing/service.go`, `flyio/pricing.go`) — and
  non-deterministic. Before exposing `estimate_cost` to agents: cache with a TTL keyed by
  (provider, service, plan) across all platforms; prefer static, periodically-refreshed tables;
  surface an `estimated | stale | fallback` confidence flag.
- **Language coverage is Node + Python only.** Say so in the README; more is a Phase-2 line.
- **Local-first is the perf win** — no proxy hop for LLM or deploy steps; instant local history.

---

## Developer-experience tooling

| Area | Picks |
|---|---|
| Release & distribution | **GoReleaser** (fix CGO/per-OS runners first), **cosign + SLSA**, Homebrew tap, Scoop, winget, Nix flake |
| Contributor onramp | `golangci-lint` + **govulncheck** + pre-commit, **testcontainers-go** (for adapter tests), **release-please** — no devcontainer/Compose needed, there's nothing to stand up |
| Docs & demos | **VHS** (terminal GIFs; on-brand with the Bubble Tea TUI), Nextra/Mintlify, Algolia DocSearch |
| Runtime | **Ollama / BYO keys** (no-account first run), **OpenTelemetry** (opt-in), Discord + GitHub Discussions |

---

## Risks & watch-items

- **Phase 0 is the load-bearing work.** Severing the backend from the deploy path (local state +
  no auth gate + direct LLM) is what makes every "no account needed" claim true. CI clean-room
  test = definition of done.
- **AWS port is the one real lift** — a Deno→Go port of CFN/ECR/ECS orchestration. Scoped to
  Phase 2 so it doesn't gate the launch; the five direct-API platforms carry Phase 1.
- **Agent shape scope** — ship two HTTP shapes (web + MCP server) at launch; the non-HTTP worker
  is a Phase-2 epic touching analyzer + monitoring + adapters.
- **CGO / cross-compile** already blocks Linux/Windows; solve in Phase 0.
- **Forked durability engine** — a supply-chain/trust risk until it's under our org and tagged;
  also the main remaining piece of complexity — keep it only while resumable deploys earn it.
- **Destructive tools + agents** — autonomous `deploy`/`rollback` need approval + cost ceilings.

---

## The next 14 days

| Days | Action |
|---|---|
| 1–2 | Secret sweep & **rotate anon + service-role + Sentry** keys (JWT anon key is in old history). Nothing public until clean. |
| 2–3 | Add MIT `LICENSE`; stand up the **local SQLite state store**; remove the `SUPABASE_*` boot dependency. |
| 3–6 | Drop the deploy-path auth gate; wire the BAML **`ClientRegistry`** for direct LLM + key plumbing; replace `deployment-logger` calls with local writes. |
| 5–8 | **Clean-room CI proof** — no account, no backend, Linux build, `prod "deploy this to fly"`: no fatal, no browser, real deploy. Includes the CGO fix. |
| 8–11 | Rename module → `pushtoprodai/prod-cli` (+ Makefile/goreleaser, regen BAML); re-home the `go-workflows` fork with a tag; split the backend out into the separate repo. |
| 11–14 | Spike `prod mcp` (`deploy` + `status`) **and the approval state machine**; prove the loop inside Claude Code. |

_A living document, corrected against a code-grounded review. Update it as phases land._
