# prod — Launch Roadmap

**Purpose:** what's left before prod can go fully open-source, split hard into
**must-do to ship public** vs **must-do to be *chosen*** vs **fast-follow**. Written
to maximize the two things that win: *reasons to pick prod* and *time from install
to a live URL*.

**TL;DR:** You are ~3 small, owner-gated steps from being able to flip the repo
public — far fewer than it feels. The *big* work isn't a blocker to open-sourcing;
it's what makes people **choose** prod once they arrive. Don't conflate the two.

---

## 0. Where we are (the moat is already substantial)

Shipped and merged:
- **Single self-contained Go binary.** No backend, no account, BYO credentials, local
  SQLite/JSON state in `~/.prod`. Direct LLM (OpenAI > Anthropic > local Ollama) —
  the direct-client path is wired (`directRegistry` bypasses the old proxy). No
  phone-home.
- **7 deploy targets:** Fly, Render, Vercel, Netlify, Heroku (direct API) + **AWS App
  Runner** (managed container, your creds). Every one supports the plan→approve→deploy
  flow with rollback.
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
× reach ÷ effort*.

### A. Cloud breadth — managed-container parity across the big three  *(highest reach)*
The registry-adapter + managed-container pattern generalizes cleanly (proven by App
Runner → GAR). Finish the set:
1. **Google Cloud Run** — the `gar` kind is done; remaining is the `gcprun` Deployable
   (Cloud Run v2 API: create/update service → poll Ready → URL) + the 5-switch wiring +
   the `Platform.GoogleCloudRun` enum. ~80% an App Runner port. **Real revision-based
   rollback** (App Runner's is a stub). *(M. Finish this before Azure — it's already
   half-built.)*
2. **Azure Container Apps** — the same port a third time: a new `acr` (Azure Container
   Registry) kind mirroring `ecr`/`gar`, Azure AD auth (`azidentity`), the
   create→poll→URL flow, **real revision rollback**. Completes AWS/GCP/Azure
   managed-container parity + the PaaS five = "deploy anywhere, in English." *(M.)*

### B. The agent-native moat — this is the reason to *pick* prod  *(highest differentiation)*
1. **Finish the shape model:** a `LivenessChecker` interface so worker/cron adapters own
   their liveness, `SupportedShapes()` shape×platform validation in planning, and the
   MCP-server JSON-RPC handshake + a copy-pasteable `mcpServers` block on success. *(M.)*
2. **Modal adapter** — serverless **GPU** + Python-native. *"Deploy this agent to Modal
   with an A10G"* in English is a headline no competitor can match. The first genuinely
   non-container, non-HTTP target — it's why the shape work exists. *(L, the strategic
   payload.)*
3. **Grow the MCP toolset** to `plan`/`status`/`rollback` (an agent that can ship but not
   recover is unsafe) + a `shape` param on `deploy`. *(M.)*

### C. Reach — more languages  *(broadens the top of funnel)*
Node + Python today. Order: **Go** (self-serving, single static binary, trivial),
**Ruby/Rails** (vibe-coder favorite), **Bun/Deno** (agent/edge). Agent *frameworks*
(LangGraph/CrewAI/Mastra) matter more than raw languages for the agent story. *(S–M each.)*

### D. Polish  *(trust + craft)*
- **Cost-confidence flag** (`estimated | stale | fallback`) — needs the pricing-cache
  work; the display already says "Estimated cost." *(M.)*
- **Windows** — cover via WSL2 (the Linux binary works today); defer native (CGO/mingw
  pain) until demand. *(Defer.)*
- **Console-vs-TUI**: the deferred typed-event bus — only if drift becomes a real cost
  (it's blocked by a `deployment→output` import cycle; gold-plating for now). *(Defer.)*

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
