# prod — Launch Roadmap

**Purpose:** what's left before prod can go fully open-source, split hard into
**must-do to ship public** vs **must-do to be *chosen*** vs **fast-follow**. Written
to maximize the two things that win: *reasons to pick prod* and *time from install
to a live URL*.

**TL;DR:** You are ~3 small, owner-gated steps from being able to flip the repo
public — far fewer than it feels. The *big* work isn't a blocker to open-sourcing;
it's what makes people **choose** prod once they arrive. Don't conflate the two.

> **Status update — much of Part 3 has since shipped.** Google Cloud Run + Azure Container
> Apps (the full 8-cloud managed-container set), **Modal** (experimental), the L1/L2/L3
> cloud-adapter framework + **plugins**, **Go** language support, **`prod destroy`**, the
> `rollback`/`destroy`/`doctor` MCP tools, Cloud Run Secret Manager, WSL2 Windows, and the
> **automated CI + release pipeline** (with Homebrew) are all done. What genuinely remains:
> the owner-gated launch gate below (secrets purge, first release, real-account validation of
> the manual-verify clouds + Modal, the demo GIF), plus `prod logs` (still unbuilt). Treat the
> per-item "TODO" framing in Part 3 as historical where it conflicts with this.

---

## 0. Where we are (the moat is already substantial)

Shipped and merged:
- **Single self-contained Go binary.** No backend, no account, BYO credentials, local
  SQLite/JSON state in `~/.prod`. Direct LLM (OpenAI > Anthropic > local Ollama) —
  the direct-client path is wired (`directRegistry` bypasses the old proxy). No
  phone-home.
- **8 deploy targets + Modal + plugins:** Fly, Render, Vercel, Netlify, Heroku (direct API)
  + **AWS App Runner, Google Cloud Run, Azure Container Apps** (managed container, your creds)
  + **Modal** (experimental) + **out-of-tree provider plugins**. All support plan→approve→
  deploy; rollback on every revision-keeping cloud (App Runner is the exception — teardown
  only). `destroy` teardown shipped too.
- **The deploy spine works.** `prod "deploy this to fly"` runs (the flagship command
  was broken — fixed), `--yes` for CI, `--dry-run` + cost preview, `prod doctor`,
  discoverable rollback.
- **Agent-native:** `prod mcp` with a real **deploy tool behind a human-approval gate**
  (preview vs confirm); `deployShape` (web/mcp-server/worker/cron) with **shape-aware
  liveness** so non-HTTP agents aren't auto-failed. The `gar` (Google Artifact Registry)
  kind — first piece of the Cloud Run adapter — is in.
- **UI fitted to the product:** terminal-native theme, de-SaaS'd language, one decision
  block with cost, masked secret input (TUI + console), structured JSON events for agents.

**This is the differentiator.** Nobody else does *natural-language deploy of apps AND
agents, local-first, no account*. Lead with it.

---

## 1. MUST-DO to go public — the hard blockers (small, mostly owner-gated)

These are the *only* things strictly required to flip `PushtoProdAI/prod` from private
to public without harm. They are small. Do them first.

1. **[SECURITY — non-negotiable] Rotate + purge the leaked secrets.** Git history
   contains a live **Render API key** and **two Sentry DSNs** (see
   `docs/security-sweep.md`). Before public: rotate all three, then run the
   `git-filter-repo` purge. Publishing with live secrets in history is the one
   mistake you can't take back. *(Owner: rotate + purge. Effort: S, but gated on
   access to those accounts.)*
2. **[FRONT DOOR] Cut the first real release so the install one-liner actually works.**
   A `v0.0.2` tag exists and `scripts/install.sh` is in place, but the one-liner is
   worthless without a published release carrying **macOS + Linux binaries**. Cut a
   GoReleaser (darwin) + manual Linux-archive release, upload the assets, and smoke-test
   `curl -fsSL …/install.sh | sh` on a clean machine. Publish the Homebrew tap.
   *(Owner: needs a GITHUB_TOKEN + the release run. Effort: S–M; the tooling is ready.)*
3. **Flip the repo to public.** After 1–2. Verify README quickstart, LICENSE (MIT),
   CONTRIBUTING (the CGO/BAML build note), and that the docs don't reference the private
   commercial backend. *(Owner. Effort: S.)*

**That's it for "can we open-source."** Everything below is about being *worth*
open-sourcing.

---

## 2. MUST-DO to be CHOSEN — the "installed and deployed in 60 seconds" bar

If a newcomer can't get from `curl | sh` to a live URL in under a minute, none of the
breadth matters. This is the conversion funnel; treat it as P0 alongside Part 1.

1. **The install one-liner working end-to-end** (= release above). *Install → `prod
   doctor` green → deploy* must be frictionless. The first-run BAML-library download
   (~56 MB) needs network + CA certs — make that failure mode obvious (doctor should
   catch it). *(P0, gated on the release.)*
2. **A 30-second flagship demo.** A VHS/asciinema recording embedded in the README:
   `curl|sh` → `prod "deploy this to fly with a postgres"` → *approve* → live URL. This
   single asset converts more than any feature list. **Record it the day the release is
   live.** *(P0. Effort: S. Highest ROI in the whole plan.)*
3. **A second demo that shows the moat: deploy an *agent*.** `prod "deploy this MCP
   server to fly"` (works today via deployShape), and the MCP deploy-with-approval from
   inside Claude Code/Cursor. This is the *why-us*, not just *another-deploy-tool*.
   *(P0–P1. Effort: S once the shapes/MCP polish below lands.)*
4. **Verify zero-to-URL on a truly clean machine** (fresh macOS + Linux, no Go, no
   creds beyond a Fly token). The path works in dev; prove it works cold. *(P0.)*

---

## 3. FAST-FOLLOW — what maximizes "why choose us" (prioritized, not blockers)

None of this blocks going public. All of it makes prod win. Ranked by *reasons-to-choose
× reach ÷ effort*. **Validated, detailed plans (with acceptance criteria + real-cloud
validation notes) live in [fast-follows.md](./fast-follows.md)** — including the ones added
after a review pass: `prod destroy`/teardown and `prod logs` (both missing today), and the
Go language (nearly free — its Dockerfile template already exists).

### A. Cloud breadth + the adapter framework — reach *and* the extensibility play  *(highest reach)*
The registry-adapter + managed-container pattern generalizes cleanly (proven: App
Runner → Cloud Run). But adding a cloud touches **~10 dispatch switches**, and a silent
miss (`getProjectDetector`) can break every deploy — so breadth is gated by boilerplate.
Fix the framework, then breadth is cheap. Full design + acceptance criteria:
[docs/cloud-framework-plan.md](./cloud-framework-plan.md).

1. **Google Cloud Run — DONE** (`gar` registry + `gcprun` adapter + wiring). Managed
   container → HTTPS via the user's ADC. Follow-ups: real revision rollback, Secret
   Manager for secrets, a GCP cost estimator.
2. **Cloud-adapter framework, Level 1 — DONE** (`PlatformCatalog`, #253/#254/#255).
   Adding a cloud is now one registration: the dispatch/auth/detector/planning/menu
   switches, Django hosts, the rollback gate, and a single shared container workflow all
   derive from the catalog. A completeness test kills the silent-miss class.
3. **Azure Container Apps** — rides Level 1: a new `acr` (Azure Container Registry) kind
   mirroring `ecr`/`gar`, `azidentity` auth, create→poll→URL, **real revision rollback**.
   The registration itself is one PlatformSpec (ManagedContainer + DomainSuffix); the work
   is the acr kind + the aca Deployable + the resource-group/environment provisioning.
   Completes AWS/GCP/Azure managed-container parity + the PaaS five. *(M.)*
4. **Framework Level 2 — DONE** (`internal/deployment/managedcontainer`, #264). App
   Runner, Cloud Run, and Azure Container Apps share one `Run(Provider, …)` flow; a cloud
   implements only its API calls (Prepare → registry + deploy closure). The base
   guarantees the Primary CreatedResource the generic workflow needs.
5. **Framework Level 3 — out-of-tree provider plugins** (`hashicorp/go-plugin`, net/rpc
   for v1; gRPC cross-language a follow-up): a third party ships `prod-provider-x` and
   `prod "deploy to x"` works *without forking prod*. A plugin is an **L2 `Provider`
   proxied over a subprocess, registered through the L1 catalog** with a dynamic
   `Platform` value. Reviewed design + acceptance criteria (incl. env-isolation +
   Node-framework compat + the trust model): [docs/l3-plugin-plan.md](./l3-plugin-plan.md).
   The platform/ecosystem play for the devops audience; **credential isolation
   (`SkipHostEnv` + curated env) is the #1 constraint**. *(L, the big bet.)*

### B. The agent-native moat — this is the reason to *pick* prod  *(highest differentiation)*
1. **Finish the shape model:** a `LivenessChecker` interface so worker/cron adapters own
   their liveness, `SupportedShapes()` shape×platform validation in planning, and the
   MCP-server JSON-RPC handshake + a copy-pasteable `mcpServers` block on success. *(M.)*
2. **Modal adapter** — serverless **GPU** + Python-native. *"Deploy this agent to Modal
   with an A10G"* in English is a headline no competitor can match. The first genuinely
   non-container, non-HTTP target — it's why the shape work exists. *(L, the strategic
   payload.)*
3. **Grow the local MCP server into a first-class agentic citizen** — it already exposes
   `deploy` (approval-gated), `list_deploys`, `analyze_project` over stdio (any MCP client
   spawns `prod mcp`). Add `rollback` (deploy-without-rollback is unsafe for autonomy) +
   `doctor` (self-diagnose) tools and surface `deployShape` in the deploy preview. *(M.)*
4. **[COMMERCIAL] Network MCP server** (`prod mcp --http` / `prod serve`) — the MCP
   HTTP/SSE transport so *remote* tools, *multiple* clients, or a *hosted/shared* prod can
   connect (vs today's per-client stdio subprocess). This is a **paid/hosted-tier
   feature**, not OSS: a network server that can deploy (spend money, mutate clouds) needs
   auth, and it's the natural shape of the commercial tier. Keep it out of the OSS core.

### C. Reach — more languages  *(broadens the top of funnel)*
Node + Python today. Order: **Go** (self-serving, single static binary, trivial),
**Ruby/Rails** (vibe-coder favorite), **Bun/Deno** (agent/edge). Agent *frameworks*
(LangGraph/CrewAI/Mastra) matter more than raw languages for the agent story. *(S–M each.)*

### D. Polish  *(trust + craft)*
- **`prod destroy` + `prod logs`** — teardown and log access don't exist yet; table-stakes
  once an app is live. The per-adapter delete/logs logic is largely internal already. *(M
  each; details in [fast-follows.md §6](./fast-follows.md).)*
- **Cost-confidence flag** (`estimated | stale | fallback`) — needs the pricing-cache work;
  the display already says "Estimated cost." *(M.)*
- **Windows** — ✅ **WSL2 documented** ([docs/windows.md](./windows.md)) + the installer
  now points there; the Linux binary runs unchanged. Native deferred (CGO/mingw). *(Done.)*
- **Console-vs-TUI parity** — ✅ **resolved**: the confirm-path panic is gone (all
  `out.(TUIWriter)` assertions are the guarded `, ok` form with console fallbacks), and
  `TestWriterParityNoPanic` guards every writer against the full event surface. A fuller
  typed-event bus stays deferred (gold-plating). *(Done.)*

---

## 4. The opinionated sequence

1. **Unblock public** (Part 1): purge secrets → cut the release → flip public. Small,
   owner-gated, do it now.
2. **Convert** (Part 2): the 30-second install→deploy demo + the deploy-an-agent demo,
   the day the release lands. This is the single highest-ROI work in the document.
3. **Win** (Part 3): finish **Cloud Run**, then **Azure Container Apps** (cloud breadth),
   in parallel with the **agent-native completion → Modal** (the moat). Languages and
   cost-confidence trail.

**The one-sentence thesis to design every decision against:** *a developer or an AI agent
should get from a single English sentence to a live URL — for a web app **or an agent** —
in under a minute, on their own cloud, with no account.* Everything that shortens that
sentence-to-URL path or widens what "URL" can be (a GPU agent, an MCP server, a worker) is
worth doing; everything else waits.
