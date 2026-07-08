# prod DX Roadmap — Tier 1, PR Previews, Languages, OSS Hygiene

_Synthesized from five code-grounded analyses (codebase mechanics, language feasibility,
Tier-1 differentiators, PR previews, OSS hygiene), then revised per an adversarial review that
re-verified every load-bearing claim against the tree. Effort: **S** ≈ <½ day, **M** ≈ 1–3 days,
**L** ≈ ~1 week._

## Thesis
Don't chase Vercel/Railway feature parity — compound prod's actual moat: **a coding agent can
drive it (MCP) and it deploys the agents you build.** Keep the single-binary/local-first
simplicity and make every failure actionable. Everything below is filtered through that.

## Status (updated 2026-07-08)
**Shipped:** Phase 0 hygiene (all agent-doable items, across all 3 repos); **F1** (Shape on
DeploymentSpec); **F2** (`--name`, headless JSON `--yes`, `deployment_complete{id,name,duration}`,
`--env`/`--env-file` with sensitive→secrets routing); **F3** (Docker Linux release, live-validated);
**F4** (reuse the project's own Dockerfile); **F5** (idempotent-by-name; fail-loud on a pinned
collision); **F7** (URL-less worker records render in ls/open/status); **2A** (`prod new` + 5
templates); **2B** (Fly + Render worker/cron artifacts); **2C** (agent-deploy walkthrough); **3B**
(PR preview deploys — GitHub Action + workflow + docs); **3A** languages **Ruby** + **Rust**.

**Remaining:** **3A** languages Java, C#, Elixir (same playbook, deferred); **F6** the credentialed
nightly smoke harness (the long pole — turns hermetically-tested cloud work into *proven*);
**F8 → 2D** shape-aware plugin SDK → Daytona/E2B agent-sandbox plugins; **Phase 4** (maintainer-led:
demo GIF, Discussions, examples gallery, launch post, logo).

## The synthesis insight: build foundations once
The five workstreams look independent but collapse onto a small shared base. Build these
**foundations** first and most downstream work becomes parallel and cheap:

| Foundation | Unblocks | Also fixes |
|---|---|---|
| **F1 — `Shape` on `DeploymentSpec`** (plumb shape into *artifact generation*, not just liveness) | first-class agent deploys, the worker/mcp `prod new` templates | the bug where a worker is deployed as a web service and the platform's own health check fails it |
| **F2 — CI escape-hatch flags**: `--name`, `--env`, and `deployment_complete{id,duration}` + JSON `--yes` | the GitHub Action, per-PR previews (naming + destroy-by-name), headless deploys | the interactive-env pain (pressing Enter through every var) + a `duration_ms` that's always 0 |
| **F3 — Attach Linux release archives + `setup-prod`** (build exists via `make dist-linux`; just not attached to releases) | any CI / GitHub Action | macOS-only distribution |
| **F4 — Port generalization + reuse-existing-`Dockerfile`** | every new language | brittle hardcoded 8080/8000 port assumptions |
| **F5 — Idempotent create-or-update by name; kill Fly's silent suffix-rename** | safe per-PR update-on-push | an orphaned-resource risk (a global name collision silently creates an unmanaged app) |
| **F6 — Credentialed nightly smoke harness** | verifiable end-to-end ACs for every differentiator | "deploys correctly" claims staying theoretical (the real long pole) |
| **F7 — URL-less deploy records render sanely** | worker/cron `prod ls/open/logs`/`status` | `prod open` breaking on a no-URL worker |
| **F8 — Shape-aware plugin SDK** (Meta gains shape; plugin deploy path allows no-URL) | agent-sandbox runtime plugins (Daytona/E2B/Modal-sandbox) — see 2D | plugins being HTTP-service-only (a worker plugin hits "returned no URL") |

---

## Phase 0 — Cheap high-signal wins (ship first; parallel; agent-doable)
Low effort, high trust signal, no dependencies. Several are *corrections* (a claim that isn't
true), which matter more than additions.

| # | Item | AC | Effort |
|---|---|---|---|
| 0.1 | **README badges** (CI, license, latest release, Go version) | ≥4 working shields on all three repos; CI badge reflects `ci.yml` | S |
| 0.2 | **Issue + PR templates** in `prod-cli/.github/` | Bug/Feature chooser captures OS + `prod --version` + platform + repro; PR template has a `make check` checkbox; `config.yml` links Discussions/security | S |
| 0.3 | **Fix the false CI claim in prod-plugins** — `CONTRIBUTING.md` says a schema check validates `plugins.json` on every PR, but no such workflow exists | a `pull_request` workflow validates `plugins.json` against a committed JSON Schema; fails on a malformed entry, passes on the current file | S–M |
| 0.4 | **Add CI to prod-plugin-sdk** — the public contract module ships `rpc_ctx_test.go` but runs no CI | `go test ./...` on push/PR (Ubuntu), green | S |
| 0.5 | **Add `LICENSE` to prod-plugins** (README claims MIT, no file); add SECURITY/CoC pointers to both siblings | files present (may be one-line links to canonical prod-cli versions) | S |
| 0.6 | **Reconcile CHANGELOG + contact** — CHANGELOG top is `0.1.0` while brew ships v0.2.x; CoC uses a personal email, SECURITY a project one | CHANGELOG top matches the released version; one canonical reporting address | S _(needs maintainer to pick the email)_ |

---

## Phase 1 — Foundations (unblock everything downstream)
Largely parallel (different subsystems). This is the critical path; land it before Phase 2/3.

### F1 — `Shape` on `DeploymentSpec` (S) — mostly wiring over existing machinery
The shape machinery already exists (`deployment/shape.go`, `analyzer/shape.go` `DetectAgentShape`,
`ProjectSpec.DetectedShape`, `DeployPlan.Shape` set in `planning.go:157-172` and threaded to
liveness). The only gap is that shape reaches *liveness* but not *artifact generation*. So F1 is:
add `Shape DeployShape` to `deployment.DeploymentSpec` (`deployment.go:75-90`); set it in
`DeploymentBuilder.Build` (`deployment.go:182-219`); and pass the plan shape into **every**
`NewDeploymentBuilder(&input.Spec, …)` call — the full list is `workflow_flyio.go`,
`workflow_container.go`, `workflow_render.go`, `workflow_vercel.go` (**omitted originally — it
consumes `input.Shape` for liveness at `:136`**), `workflow_modal.go`, `workflow_heroku.go`,
`workflow_netlify.go`, plus the non-web builders `planning.go:223`, `workflow_rollback.go:72`,
`workflow_destroy.go:34` (pass `ShapeWeb`/plan shape intentionally).
**AC:** an adapter reads `spec.Shape` at artifact time; existing web deploys unchanged; unit
test that a `worker` spec reaches the Fly/Render adapters and a Vercel deploy still carries its
shape.

### F2 — CI escape-hatch flags (M each, parallel)
- **`--name`** on `prod run` → resolves `plan.Spec.Name` *before* the analyzer default; plan
  validation + rollback/destroy platform-detection use it. **AC:** `prod run --name x-pr-7 …`
  deploys/destroys the exactly-named app; empty name still falls back to the analyzer.
- **`--name` precedence** — must win over BOTH the analyzer default AND the intent's name
  (`plan.Spec.Name`). Resolve it before either.
- **`--env KEY=VAL` (repeatable) / `--env-file`** → feeds the categorize/backfill step so those
  vars never go `pending`. **Define `--env` vs `.env` precedence** (recommend flag-wins,
  documented) so CI and local don't disagree silently. **CRITICAL (security):** an `--env` value
  flagged sensitive must be written to the **platform's secret store** (Fly secrets via
  `SetSecretsStep` / Render secret env), NOT plaintext `[env]` in fly.toml — today only DB/Redis
  creds route to secrets (`flyio/api_steps.go:371-409`); user-supplied secrets have no route, so
  the flagship agent story (runtime `OPENAI_API_KEY`/`ANTHROPIC_API_KEY`) would leak keys into a
  committed-looking config (`flyctl_client.go:939-941`). **AC:** a headless deploy supplies
  required vars via `--env` with no `env_var_prompt`; sensitive values reach the platform as
  secrets and never appear in the event stream or config files.
- **`deployment_complete{id,duration}` + JSON `--yes`** → add `id`+`name` (populate the id from
  the history `operationId`, `planning.go:417`), capture a start time in the workflow so real
  `duration_ms` replaces the hardcoded `0` (`planning.go:468`), and wire `--yes` auto-approval
  into the JSON path (`run.go:51-65` currently drives approval over stdin and ignores `flags.Yes`
  in JSON mode). Add the new fields to all four writers + ProxyWriter and update the **existing**
  `writer_parity_test.go` (`exerciseAllEvents`). _Note: the CLAUDE.md §6 console-panic/no-op
  warning is already resolved in-tree (`agent.go:263-265`, writers implement the events) — this
  is a field-addition + parity-test update, not a panic fix._ **AC:** `PROD_JSON_MODE=true prod
  run --yes …` reaches `deployment_complete` with a non-empty `id`, `url`, and real `duration_ms`,
  no stdin scripting; parity test green.

### F3 — Attach Linux release archives + `setup-prod` (M) — NOT a build blocker
The Linux binary already builds — `make -C cli dist-linux` cross-builds linux/{amd64,arm64}
with CGO via Docker (`.goreleaser.yml` is darwin-only only because GoReleaser OSS can't
cross-compile CGO). The work is: **attach** those archives + checksums to the release, write a
`setup-prod` action that installs them, and pre-fetch/cache the ~56 MB BAML engine. **AC:** a
fresh Linux GitHub runner installs prod and `prod doctor` passes; the engine fetch is cached.

### F6 — Credentialed nightly smoke harness (M) — the real long pole; promote it here
Nearly every differentiator AC ("deploys end-to-end with the correct shape") is only verifiable
against a live cloud. Build ONE scheduled, credentialed CI job (Fly + Render + Modal accounts)
that runs the `templates-check` matrix and the worker/mcp-server/preview smoke deploys. This is
the actual critical dependency for 2A/2B/3A/3B — without it the moat claims stay theoretical.
Land it alongside F3 (it needs the Linux binary). **AC:** nightly job deploys each template +
a worker + an mcp-server to real clouds and asserts liveness/shape; failures alert; hermetic
unit tests still gate every merge (the nightly is the end-to-end backstop, not the merge gate).

### F7 — URL-less deploy records (worker/cron) render sanely (S) — its own item, not a sub-clause of 2B
`deploytarget.Resolve` and `prod ls/open/logs` are built on per-cloud HTTP assumptions; a worker
with no `LiveURL` can break `prod open` and read poorly in `prod ls`/`status`. Make the resolver
+ CLI degrade gracefully for a no-URL record (`prod open` → open the console URL or say "this is a
worker, no URL"; `prod logs` still resolves `LogsCmd`; `prod ls` shows a "worker" marker instead
of a blank URL). **AC:** a worker record renders correctly across `ls`/`open`/`status`/`logs` with
no error and no blank/garbage URL. _Gate for 2B's "status/logs render sanely" AC._

### F4 — Language prerequisites (M)
- **Generalize port handling** — replace the hardcoded 8080/8000 with a per-language default +
  a `$PORT`-binding convention surfaced in templates. **AC:** each language template binds the
  right port; container-cloud health checks hit it.
- **Detect + reuse an existing repo `Dockerfile`** — Rails 8 and `mix phx.gen.release --docker`
  ship production Dockerfiles; honoring them is less work and more correct than regenerating.
  **AC:** a project with a root `Dockerfile` deploys using it (with a status note), skipping
  generation.

### F5 — Idempotent create-or-update by name (M)
Named deploys update-in-place by name across adapters. Distinguish two paths at
`flyio/api_steps.go:44-93`: a **`--name`/CI deploy NEVER auto-suffixes** — a global name
collision **fails loudly** (silently creating `<name>-<suffix>` forks an unmanaged, orphaned
app); an **interactive first deploy keeps** the suffix-retry convenience for humans. **AC:** a
second `prod run --name x` updates the same app (no new app, no suffix); a named global
collision errors clearly; interactive deploys retain the auto-suffix. _(Deploys are
retry-from-scratch — in-process durable state — so idempotency must be real or CI retries
orphan resources.)_

---

## Phase 2 — The differentiators (the moat)

### 2A — `prod new <template>` (L) — depends on F1 for worker/mcp templates
A top-level `prod new <template> <name>` mirroring `prod plugin new`. **Templates embedded via
`go:embed`** (keeps the single-binary promise; reject a templates repo/network fetch for v1).
Each template carries a `prod.template.yaml` (shape + env + suggested platforms); the
scaffolder writes `.env.example` and prints the exact deploy prompt. The template's *code*
carries the dependency signal `DetectAgentShape` keys on, so shape is guaranteed regardless of
prompt phrasing.
**v1 templates:** `mcp-server` (TS, streamable-HTTP), `agent-worker` (Python LangGraph, no web
server → worker), `nextjs`, `fastapi`, `web`.
**Files:** new `cmd/new/` (command, embedded `templates/`, manifest parser); register in
`cmd/root/root.go`; set `DetectedShape` in `analyzer/go.go` (currently missing).
**AC:** each template scaffolds → builds locally → **deploys end-to-end with the correct shape**
(`mcp-server` passes the `initialize` handshake; `agent-worker` deploys with no HTTP probe and
no false rollback; the three HTTP templates serve + pass `isURLLive`); `agent-worker`
classifies as `worker` on "deploy this" alone; a `make templates-check` CI matrix stays green.
_Parallel: the 5 templates are independent once the `cmd/new` skeleton exists; the 3 HTTP ones
don't need F1._

### 2B — First-class agent deploys (L) — depends on F1
- **`mcp-server` → Fly/Render/container** — already HTTP-shaped; needs validation + confirm the
  artifact exposes the MCP endpoint on the expected port (prefer root-mounted for the handshake).
- **`worker` → Fly/Render** (native background workers), **not** container clouds (they require
  an HTTP URL — route worker/cron there to a clear "use Fly/Render/Modal" message rather than a
  mid-workflow `no URL` error). Fly: emit a `[processes]` app with no `[http_service]`; Render:
  emit `background_worker`. Liveness already skips worker/cron correctly.
- **GPU agent → Modal** (already GPU-capable; validating it removes the `Experimental` flag).
**AC:** a real `agent-worker` deploy to Fly and Render runs with no HTTP probe and no false
rollback; a real `mcp-server` deploy is live only after the handshake; worker-on-container-cloud
gives the redirect message; `status`/`logs` render sanely for a URL-less worker record.
_Cron is a fast-follow (schedule plumbing). Agent-sandbox runtimes like Daytona are their own
category — see 2D._

### 2D — Agent-sandbox runtimes (Daytona / E2B / Modal sandboxes) — a distinct category, via a shape-aware plugin
This is where "AI platform" workers like **Daytona** go — and it's the most ICP-differentiated
thing prod can offer: *deploy an autonomous agent into an isolated computer.* Two decisions:

**1. It's a plugin, not core.** Daytona is an agent-sandbox runtime (build image → snapshot →
sandbox → run the agent), not a persistent web service — so it ships as an out-of-tree
`prod-provider-daytona` (`prod plugin new daytona`, the six `plugin.Provider` methods), keeping
the sandbox concern out of the binary and respecting the low-dependency-core preference. `Deploy`
= push image → create snapshot → create sandbox (auto-stop disabled for a durable agent) → start
the process → return a preview URL (or none for a pure worker); rollback = recreate from a prior
snapshot (image-swap). **E2B and Modal-sandbox mode are the same category** and would be sibling
plugins.

**2. It exposes a missing foundation — the plugin SDK is HTTP-service-only.** Verified: the SDK's
`Meta` carries no shape (`prod-plugin-sdk/provider.go:53-58` — only Name/Aliases/DomainSuffix/
SupportsRollback), and the plugin deploy path routes through the container workflow which
**hard-requires a URL** (`workflow_container.go:94`, "returned no URL"). So a worker/agent plugin
fails today exactly like a worker on a container cloud (2B). **New foundation — F8: shape-aware
plugin SDK.** Extend F1's shape work to the plugin boundary: add `Shapes []DeployShape` (or a
per-`Deploy` shape) to `plugin.Meta` so a plugin declares it's a worker/agent runtime; relax the
plugin deploy path's URL requirement for non-HTTP shapes; route liveness by shape (skip the HTTP
probe for a sandbox agent, as `verifyLiveness` already does for core workers). Bump
`plugin.ProtocolVersion` (it's a contract change) so old plugins are rejected cleanly.

**Dependency chain:** `F1 (core shape) → 2B (worker deploys real, no-URL relaxed) → F7 (URL-less
records render) → F8 (shape-aware plugin SDK) → prod-provider-daytona`. Daytona lands **after**
the worker foundation is proven — a plugin in the ecosystem, not a core adapter. Effort: **F8 M**
(contract + SDK + protocol bump + host relax); the Daytona plugin itself **M–L**, parallel once
F8 lands. Needs a Daytona account to validate.

### 2C — Agent-deploy walkthrough docs (S–M) — independent, start now
`docs/agent-deploy.md`: the "your coding agent ships it" story — exact `prod mcp` config for
Claude Code + Cursor (flag env-inheritance as the #1 gotcha), the 9-tool table with the
preview-first safety model, and three reproducible transcripts (happy-path deploy w/ approval
gate; full lifecycle incl. an mcp-server + rollback; a safety refusal). **AC:** a fresh tester
wires `prod mcp` and deploys in <5 min following only the doc; every command/error string
matches code. _The mcp-server transcript is most compelling after 2B._

---

## Phase 3 — Reach (languages + PR previews)

### 3A — Languages (each M–L) — depend on F4; parallelizable; sequence by ROI
Ranked by (reach × container-fit ÷ effort). Each = analyzer file + Docker template +
registrations + tests (land the template in the **same PR** as the analyzer — `GenerateDockerfile`
hard-errors on an unknown language, so an analyzer without a template breaks Fly + all container
clouds). Shared machinery: **compiled-binary** (Rust reuses `go.dockerfile` + cargo-chef →
distroless), **JVM fat-jar** (Java; Quarkus/Micronaut later), **runtime-interpreter** (Ruby +
Elixir reuse Python's interpreter-base + pre-deploy-migration pattern).

| Order | Language | Priority framework | Effort | Notes |
|---|---|---|---|---|
| 1 | **Ruby** | Rails (Sinatra 2nd) | M–L | Biggest "deploy my web app" audience; Rails 8 ships its own Dockerfile (reuse via F4); Ruby migration patterns already stubbed in `migration.go`; assets-precompile (`SECRET_KEY_BASE_DUMMY=1`) + `db:migrate` pre-deploy |
| 2 | **Rust** | Axum (Actix 2nd) | S–M | Cleanest container fit; distroless + cargo-chef; copies `go.dockerfile` thinking; migrations optional |
| 3 | **Java** | Spring Boot | M–L | Highest enterprise reach; dual Maven+Gradle parse; layered fat-jar; Flyway/Liquibase often auto-run on startup (simpler migration story) |
| 4 | **C#/.NET** | ASP.NET Core | M | Very clean (chiseled image, non-root, 8080 default); EF migrations need a bundled `efbundle` (runtime image lacks the SDK) |
| 5 | **Elixir** | Phoenix | M | Strategic/agents narrative; `mix release`; release-based `bin/migrate` is a one-off pattern |

**Per-language AC (template):** analyzer detects it (no false-match vs Node/Python/Go); correct
services/env/routes/build/start; a real deploy to **Fly + one container cloud** passes `web`
liveness; migrations run where applicable; framework host-config applied where needed; shape
detection for an MCP/agent fixture. _Live cloud needed for the deploy leg (hermetic tests gate
merges)._

### 3B — PR preview deploys + GitHub Action (L) — depends on F2, F3, F5
- **`pushtoprodai/deploy-action`** (separate repo, composite) + `setup-prod` (F3): on PR,
  install prod, deploy headless over the JSON substrate (F2), post a sticky PR comment with the
  live URL + cost. Creds passed as **env**, never step args.
- **Per-PR lifecycle:** `<base>-pr-<N>` naming (F2 `--name`); Track A (Vercel/Netlify) uses
  native preview URLs (ship first, minimal work); Track B (Fly/Render/containers) creates on
  open, updates in place on push (F5), destroys on close (`prod destroy --name …`), with an
  optional scheduled sweep as an orphan safety net (history gains `pr_number/repo/sha` metadata).
**Destroy guardrails (irreversible + fires on every close, incl. closed-unmerged/reopened):**
destroy ONLY an app whose history metadata carries the matching `pr_number`+`repo` AND whose
name matches the `<base>-pr-<N>` pattern — so a mislabeled event can never nuke a production app
sharing the org. **Cost guardrail:** N open PRs = N billed apps in the user's *own* account —
add an explicit max-open-previews cap (opt-in beyond it) + default to the smallest instance tier,
not just a cost comment.
**AC:** open → unique preview URL commented; push → same app/URL updated; close/merge → destroyed
(only if metadata+name-pattern match), no orphans; two concurrent PRs get non-colliding previews;
exceeding the preview cap warns rather than silently billing; secrets never logged; a failed
deploy still comments clearly (not a stale "success"). _Live account needed to validate headless
auth + the full create→update→destroy cycle per platform._

---

## Phase 4 — Community + launch (maintainer-led; gated on assets)
| Item | AC | Effort | Owner |
|---|---|---|---|
| **Demo GIF** (asciinema→GIF of `prod "deploy this to fly"` → URL) — highest-leverage growth asset; gates Show HN/PH | autoplaying <2 MB GIF near README top of a real deploy | M | Maintainer records |
| Curated `good first issue` set (8–12 scoped starters) | labels + ≥8 issues each with context + AC + file pointer | M | Maintainer files; agent drafts |
| Examples gallery (`docs/examples/`) — 3–4 "deploy this stack to this cloud" | ≥3 runnable, each with prompt + expected plan + result | M–L | Parallel; agent drafts |
| GitHub Discussions (+ optional Discord) | enabled; linked from README + issue config | S enable / ongoing | Maintainer |
| Launch post + Show HN + Product Hunt | published; depends on the GIF | L | Maintainer |
| Logo / brand + social card | mark + README header | L | Design |

---

## Parallelization plan

```
Phase 0 (all parallel, week 1) ─────────────────────────────────────────────┐
                                                                             │
Phase 1 foundations (parallel):                                              │
  F1 Shape ─────────────┐                                                    │
  F2 --name/--env/json ─┤ (3 sub-streams parallel; json needs parity test)   │
  F3 Linux binary ──────┤                                                    │
  F4 port + Dockerfile ─┤                                                    │
  F5 idempotency ───────┘                                                    │
        │            │              │                │                       │
        ▼            ▼              ▼                ▼                       ▼
   ┌─ 2A prod new ─ 2B agent deploys   3A languages     3B PR previews    2C docs +
   │  (F1)          (F1)               (F4; ranked,      (F2+F3+F5)        Phase-4 assets
   │                                    parallel)                          (independent)
   └─ HTTP templates need no F1 → start with Phase 1
```

- **Start immediately, zero blockers:** all of Phase 0; 2C docs; the 3 HTTP `prod new`
  templates; 2B mcp-server *validation*; Track A (Vercel/Netlify) previews.
- **Critical path to the differentiator:** F1 (now ~S — the shape machinery already exists) →
  branch the Fly artifact generator (skip `[[services]]`, emit `[processes]` when
  `!shape.HTTPShaped()`) → validate on Fly with the Python `agent-worker` template (needs no
  `go.go` change; python.go already sets `DetectedShape`). That single thread proves "first-class
  agent deploys, driven by your coding agent" end-to-end and makes 2C's best transcript real —
  it's a **2–3 day proof, not a phase**. **Do this slice first.**
- **The real long pole is F6 (the credentialed nightly smoke harness), not F3.** Every
  "deploys end-to-end with the correct shape" AC is unverifiable without it. Build it early
  (alongside F3) or the moat claims stay theoretical.
- **Critical path to PR previews:** F3 (Linux) + F2 (`--name`,`--env`,json) + F5 → 3B.
- **Languages** fan out after F4; sequence Ruby → Rust → Java → C# → Elixir but they're
  independent PRs.
- **Needs a live cloud account (all flagged):** every end-to-end deploy AC in 2A/2B/3A/3B, the
  Modal-GPU/Daytona legs, and the <5-min doc timing. Recommend one shared credentialed nightly
  CI job (Fly + Render + Modal) running `templates-check` + the worker/mcp/preview smoke deploys.

## Recommended first slice (smallest thing that proves the moat)
**Phase 0 (parallel) + F1 (~S) + the Fly worker-artifact branch + the Python `agent-worker`
template + `docs/agent-deploy.md`, validated on Fly** (which needs a minimal credentialed smoke
job — the seed of F6). That ships the cheap trust wins, fixes the worker-as-web-service bug, and
makes the agent-native story real and demonstrable in one 2–3 day thread — before spreading into
CI/PR-previews, languages, and the rest. F2 (`--name`/`--env`/JSON) can run in parallel but is
the on-ramp for PR previews, not this slice.

## Open decisions
- Canonical security/contact email (0.6).
- Does `prod new` also `git init`/install deps, or scaffold-only + print commands? _(Recommend
  scaffold-only, matching `plugin new`'s restraint.)_
- Per the maintainer's stated preference: **no DB auto-provisioning / new-credential
  integrations** — keep "detect → do what we can → clear instructions." (Applies to F4 and 3A
  migration handling: instruct, don't provision.)
