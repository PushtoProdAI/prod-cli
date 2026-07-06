# prod — Agentic-deploy plan: MCP surface, deploy launcher, reliability, plugins

**Status:** plan (for review before build). Sequences four bodies of work behind one
north star — **prod is the deploy layer for agentic coding platforms** (Claude Code,
Cursor, Cline via MCP). A developer tells their agent "deploy this," the agent deploys to
the developer's own cloud behind a human-approval gate, then answers "where is it running
/ show me the logs" — all as MCP tools, with a matching human CLI surface. Builds on
[agent-native-plan.md](./agent-native-plan.md) (design-only, predates the shipped MCP
server), [cloud-framework-plan.md](./cloud-framework-plan.md), and
[l3-plugin-plan.md](./l3-plugin-plan.md).

**Method:** synthesized from four code-grounded subsystem reads (deploy FSM, local
state/adapters, MCP server, plugin host). All file:line references below were verified
against the current tree. Where this contradicts an older plan, the contradiction is
called out explicitly under "Ground-truth corrections."

---

## 0. The ICP and the wedge

The ideal customer is a terminal-comfortable, increasingly agent-driven builder who
generates apps/agents fast and wants to ship to **their own** cloud without DevOps, and
without adopting another SaaS/control-plane. They already have the platforms' dashboards
for logs and metrics. So:

- **Lead surface = MCP.** The differentiator no PaaS has: an agent can plan → get
  human approval → deploy → report the URL → answer "what's running / where are the logs"
  without leaving the coding session.
- **prod is a router, not an observability product.** We do **not** rebuild logs/metrics
  (ROADMAP open-core boundary; the hosted web dashboard is the *commercial* tier). The
  local/agent surfaces index deploys and **deep-link out** to each platform's native
  console/CLI.
- **Everything stays local, no account, no phone-home.** The CLI reads local history; the
  MCP tools return data (URLs, commands), they don't act on the host.

---

## 1. Ground-truth corrections (things older docs get wrong)

These were verified in the current code and should be treated as authoritative; the named
docs are stale on these points.

1. **The one-shot deploy path already works.** `docs/ui-modernization-plan.md` §0 ("no
   plain-terminal one-shot deploy completes today") is **stale**. `cmd/main.go:129` sets
   `cmd.Args = cobra.ArbitraryArgs`; `root.go:88-91`/`:142` run the prompt via
   `Agent.DriveOneShot`; `run.go:70` reads y/n from the TTY; `--yes`/`-y` (`root.go:35`,
   `run.go:23`) sets `interactive=false` and reaches auto-approve (`agent.go:626,723`).
   **Do not re-scope reliability work as "make deploy work."**
2. **Deploy history is JSON, not SQLite.** `~/.prod/history.json` (`history/store.go:17-28`,
   `ROADMAP.md:130`). The only SQLite is go-workflows' durable backend, and it is **dormant**
   — `SQLitePath` is never set, so `workflowext.go:84` runs `NewInMemoryBackend`. Workflow
   state is in-process/ephemeral today.
3. **Container clouds drop their service id before history.** `managedcontainer.Run`
   (`managedcontainer.go:70-78`) wraps the cloud's `DeployResult{ID,Name,URL}` into a
   resource carrying only `{url}`. Render's `srv-…`, App Runner's ARN, Cloud Run's
   project+region, and Azure's resource-group never reach `history.json`. This is a
   data-loss bug and the keystone blocker for the launcher.
4. **`Record.Platform` casing is inconsistent.** Deploy writes lowercase literals
   (`"flyio"`, `"aws"`, `"googlecloudrun"`); rollback/destroy write the stringer
   (`"FlyIO"`, `"AWS"`, `"GoogleCloudRun"`). Any grouping/lookup must normalize.
5. **There is no auto-rollback on liveness failure.** `ui-modernization-plan.md` implies
   one; the code marks the deploy `failed` and leaves the unhealthy service running
   (`workflow_render.go:299`, `workflow_container.go:118`).
6. **The MCP surface is already 6 tools, and `destroy` is undocumented.** `deploy`,
   `rollback`, `destroy`, `doctor`, `list_deploys`, `analyze_project`
   (`mcpserver/server.go`, `mcpserver/tools.go`). Neither `mcp.go`'s tool list nor the
   docs mention `destroy`.
7. **Plugins are net/rpc + AutoMTLS, not gRPC.** `hashicorp/go-plugin` over net/rpc + gob
   (`pkg/plugin/rpc.go`, `pluginhost/client.go:28`). `l3-plugin-plan.md`'s `Meta.ProtocolVersion`
   field does **not** exist in the shipped `pkg/plugin`. Context is not propagated to
   plugins (cancellation is a no-op, `rpc.go:39-41`).

---

## 2. Item A (keystone) — persist deploy identifiers + a `deploytarget` resolver

**Why first:** every richer surface (agent `status`/`logs`/`deep_link`, human
`ls`/`open`/`logs`, and auto-rollback) needs to answer "for this deploy, what's the live
URL, the console URL, the logs command, and can it roll back?" Today the data to answer
that is dropped for Render, AWS, GCP, and Azure. Fix the data, then build one resolver
everything reuses.

**Current persisted identifiers (verified):**

| Cloud | In `history.json` today | Enough for console + logs? |
|---|---|---|
| Fly, Heroku, Netlify | id + url (+ region / admin_url) | ✅ |
| Vercel | deployment_url, project_id | ⚠️ console needs team slug |
| Render | url + platform only (drops `srv-…`) | ❌ |
| AWS App Runner | url only (drops ARN → region/account) | ❌ |
| GCP Cloud Run | url only (drops project + region) | ❌ |
| Azure ACA | url only (RG/subscription never captured) | ❌ |
| Modal | name + url (no workspace) | ⚠️ CLI ok, console needs workspace |

**Build:**
1. Forward `DeployResult.ID` (+ region/project/resource-group) through
   `managedcontainer.Run` and each container adapter into `history` metadata; add Render's
   service id; capture Azure RG/subscription/location at deploy time.
2. Normalize `Platform` to one canonical lowercase token on write (fix the deploy-vs-rollback
   split).
3. New internal package `deploytarget`: `Resolve(record) → {LiveURL, ConsoleURL, LogsCmd,
   CanRollback, RollbackKind}` with per-platform knowledge in one place.

**Acceptance criteria:**
- ACA.1 — After a deploy to any of the 8 clouds + Modal, `history.json` contains enough to
  build that platform's console URL and logs command (service id/ARN, region, project/RG
  as applicable), verified per cloud.
- ACA.2 — `Platform` is stored as one canonical token; a deploy record and a later rollback
  record for the same app share casing; historical mixed-case records normalize on read.
- ACA.3 — `deploytarget.Resolve` returns correct live URL, console deep-link, and logs
  command for each platform; unknown/legacy records degrade to "identifier not recorded"
  rather than a broken link.
- ACA.4 — No behavior change to the deploy path itself (identifiers are additive metadata).

**Edge cases:** legacy records missing ids (degrade, don't error); Vercel/Modal console
links lacking team/workspace slug (fall back to account-level console or live URL with a
note); container clouds where the id encodes region+account (parse, don't re-query).

---

## 3. Item B (the wedge) — deepen the agent-native / MCP surface

**Current state (verified):** 6 tools; the approval gate is structurally sound — the MCP
layer sends `approved\n` only when `confirm=true` (`deploy.go:57`), and `confirm=false`
(zero value) previews and deploys nothing. Deploys run via subprocess in `PROD_JSON_MODE`.
Gaps: `list_deploys` hides `Metadata` so the URL isn't returned (`server.go:100`); no
`status`/`logs`/deep-link tools; preview-first is prose-only (an agent could call
`deploy(confirm=true)` on turn one); the only logs access anywhere is Fly's `GetAppLogs`,
and logs carry secrets with no redaction.

**Build (Phase 0 items need no dependency on Item A; the rest reuse `deploytarget`):**
1. **[no dep] `list_deploys` returns the live URL + status** per record; document the schema.
2. **[no dep] Preview-first hardening:** a `deploy` preview returns a short `plan_digest`
   (bound to prompt+path+platform); `confirm=true` must echo a matching digest or the tool
   refuses. Structural nudge toward "show the human a preview, then confirm." Prose stays.
3. **[no dep] Document `destroy`** in `mcp.go` and the site's MCP docs.
4. **[needs A] `status(app)`** — read-only: the persisted record + (when reachable) a
   live/not-live signal via `deploytarget`, without spinning the deploy pipeline.
5. **[needs A] `deep_link(app)`** — returns `{liveURL, consoleURL}`. Never opens a GUI (a
   stdio server can't and shouldn't).
6. **[needs A] `logs(app)`** — returns the **runnable platform-CLI command + console URL**,
   not raw log bytes (avoids secret egress; a stdio call can't stream a live tail). A future
   opt-in could allow bounded, redacted content.

**Acceptance criteria:**
- ACB.1 — `list_deploys` returns live URL + status; schema documented in `mcp.go`.
- ACB.2 — `deploy(confirm=true)` without a matching prior preview `plan_digest` is refused
  with "preview first"; a normal preview→confirm sequence succeeds; `confirm=false` still
  deploys nothing.
- ACB.3 — `status(app)` returns the record and, when reachable, a live signal, with a short
  timeout and no full-pipeline spin-up.
- ACB.4 — `deep_link(app)` returns `{liveURL, consoleURL}` and never attempts to open a
  browser.
- ACB.5 — `logs(app)` returns a runnable command + console URL and, by default, streams no
  raw logs to the agent; the tool description says so.
- ACB.6 — `destroy` appears in the documented tool list; any future sandbox tools live in
  their own namespace, not under `deploy` (per `agent-native-plan.md` §5).

**Edge cases:** forged/stale `plan_digest` → refuse (short-lived, bound); legacy record
with no ids → return record + "limited detail" note; usage-billed platforms (Modal) → never
present per-second cost as a fixed monthly total (`agent-native-plan.md:305`); `status` on a
dead platform → short timeout, never blocks.

---

## 4. Item C (human surface) — local deploy launcher (`prod ls` / `open` / `logs`)

The human mirror of Item B, reusing `deploytarget`. Today only the TUI `/deploys` slash
command lists history (`slash_commands.go:25,89`), and it renders without deep-linking;
there is no `prod ls`/`open`/`logs` subcommand.

**Build:**
1. **`prod ls`** — recent deploys: name, canonical platform, status glyph, relative age,
   URL, rollback indicator. `--all`, `--platform`, `--json`.
2. **`prod open <app>`** — open the live URL; `--console` opens the platform dashboard
   deep-link.
3. **`prod logs <app>`** — shell out to the platform's own CLI (`fly logs -a`,
   `heroku logs --tail -a`, `gcloud run services logs read`, `az containerapp logs show`,
   `modal app logs`, …); if the CLI isn't installed, print the install/command hint.
4. Upgrade the TUI `/deploys` handler to deep-link via the same resolver.

**Acceptance criteria:**
- ACC.1 — `prod ls` lists deploys most-recent-first with correct grouping/counts despite
  historical mixed-case records; `--json` emits a stable schema; the listing needs no network.
- ACC.2 — `prod open <app>` opens the live URL; `--console` opens the correct platform
  dashboard for that service; unknown app → actionable error with close matches.
- ACC.3 — `prod logs <app>` runs the correct platform CLI with the right identifiers; a
  missing platform CLI yields the exact install hint, not a raw exec error; the command it
  runs is printed so users learn it.
- ACC.4 — Multiple deploys sharing a name resolve to the latest successful record; ambiguity
  offers a chooser, never guesses silently.

**Edge cases:** legacy records missing ids (`open --console`/`logs` degrade with "redeploy
to enable"); failed/rolled-back records (shown with correct status; `open` warns URL may be
down); platform CLI present but unauthenticated (surface its own auth error verbatim).

---

## 5. Item D (reliability) — liveness, conditional auto-rollback, durable state

**Verified fragilities:** liveness is a single 10s GET treating any status >300 as dead
(`monitoring.go:106`) — a 302→login or 401 on a protected app reads as a failed deploy;
retry budgets are inconsistent (Render/container 15 attempts vs default 10 elsewhere); no
auto-rollback on liveness failure; `mcp-server` liveness == web liveness (no JSON-RPC
handshake); workflow state is in-memory (crash mid-deploy loses everything).

**Build:**
1. **401/403 = reachable** (app is up, just auth-walled); follow one redirect; unify the
   retry/timeout policy across all platforms; make the ready-probe timeout shape-aware.
2. **Conditional auto-rollback:** on liveness failure, if
   `SupportsRollback && hasPrevious && isUpdate`, revert and report it; otherwise report
   `failed` + remediation (never hang, never silently leave broken).
3. **`mcp-server` liveness handshake:** JSON-RPC `initialize`, expect
   `serverInfo`/`capabilities` (the deferred `LivenessChecker` in `agent-native-plan.md`).
4. **Durable workflow state:** set `SQLitePath` to `~/.prod/workflows.db`; GC completed
   instances; fall back to in-memory (with a warning) where `$HOME` is absent/read-only.

**Acceptance criteria:**
- ACD.1 — An app returning 401/403 at `/` is reported **live**; only connection failures,
  5xx, and timeouts are not-live; one 3xx is followed before judging.
- ACD.2 — A failed-liveness *update* on a rollback-capable platform ends with the previous
  revision serving and the outcome stated in CLI/JSON/MCP; a first-ever deploy or a
  no-rollback platform (App Runner, pre-fix Modal) reports failed + remediation, never hangs.
- ACD.3 — All platforms share one liveness retry/timeout policy.
- ACD.4 — A project with an MCP entrypoint is planned `shape=mcp-server` and liveness does a
  JSON-RPC `initialize`; a plain 200 with no handshake is not-live. Worker/cron shapes never
  fail for lack of a URL.
- ACD.5 — SIGKILL mid-deploy then re-run resumes or cleanly restarts without orphaning
  resources beyond what a fresh deploy reconciles; `~/.prod/workflows.db` exists and is GC'd;
  CI without `$HOME` falls back cleanly.

**Edge cases:** auth-walled *and* broken (500 behind auth) → 5xx still not-live; durable
SQLite replay of non-idempotent activities → rely on adapter idempotency (Modal create-or-
update, Render `IsUpdate`); LLM says `web` but analyzer detects `mcp-server` → code signal
wins for shape, LLM wins for platform/intent, log the override.

---

## 6. Item E (coverage + ecosystem) — agent detection, Modal GA, plugin authoring + index

**6a. Analyzer agent-detection.** Today `deployShape` is inferred by the LLM from the prompt
text only (`planning.go:163`); the analyzer never inspects code, and there is zero
agent-framework detection. Add detectors: `fastmcp`/MCP SDK (Py/TS) → `mcp-server`;
`langchain`/`llamaindex`/`crewai`/`autogen` with no web server → `worker`. Feed as a strong
prior to the LLM (ties into ACD.4).

**6b. Modal to GA.** Live token validation (today accepts any non-empty string,
`auth/modal.go:38`); store `SecretEnv` as `modal secret` (today env passthrough only);
redeploy-as-rollback via `modal app history`; pre-flight `modal` version check. Drop the
`Experimental: true` flag (`platforms.go:232`) **and** the site/docs badge together only
after a live-account e2e passes.

**6c. Plugin authoring polish.** Biggest gap: no scaffolding. Add **`prod plugin new <name>`**
generating an external-module plugin (real `pkg/plugin` import, six methods stubbed, build
line producing `prod-provider-<name>`). Naming convention is currently unenforced; port is
hardcoded 8080 (`bridge.go:21`).

**6d. Git-native plugin index (no backend).** A curated `plugins.json` in a GitHub repo
(name, repo, maintainer, per-release/os-arch checksums, install line). `prod plugin search`/
`--available` reads it from a pinned raw URL. **`prod plugin install github.com/org/repo`**
resolves to a GitHub Release asset over HTTPS and **requires a checksum verified before first
execution** (from `--checksum` or the index), prints publisher + checksum, requires explicit
confirmation, own install timeout — exactly the `l3-plugin-plan.md:39` bar. A hosted/signed
registry is a *commercial-tier* decision, not this.

**6e. Runtime hardening (open `l3` security items).** Bound RPC response sizes (gob decode is
unbounded, `rpc.go`); enforce the deploy-ctx timeout per call; extend the curated-env
deny-list toward an allow-list or at least add `GITHUB_TOKEN`, `DIGITALOCEAN_TOKEN`, DB URLs
(today only prod's own tokens + `AWS_`/`PROD_` are stripped, `client.go:53-63`).

**Acceptance criteria:**
- ACE.1 — A project with an MCP/agent-framework entrypoint is detected and planned with the
  right shape without the user naming it.
- ACE.2 — Modal: token validated before work; `SecretEnv` stored as Modal secrets; second
  deploy recognized as update; experimental badge removed in code + docs + site together.
- ACE.3 — `prod plugin new acme` produces a buildable external module that installs and
  deploys end-to-end without editing prod.
- ACE.4 — `prod plugin install github.com/org/repo[@version]` downloads over HTTPS, refuses
  to run until a checksum matches, shows publisher + checksum, and requires confirmation; a
  mismatch aborts before the binary runs.
- ACE.5 — `prod plugin search <term>` lists curated-index entries with install commands from
  a plain GitHub-hosted file; the PR flow for third parties to list a plugin is documented.
- ACE.6 — A plugin returning an oversized RPC response or hanging is bounded and fails
  cleanly instead of OOMing/hanging the host.

**Edge cases:** GitHub ref with no matching os/arch asset → clear error, no partial install;
checksum absent from both flag and index → refuse (no silent trust-on-first-use); name
collision with a built-in/other plugin → surfaced at install; offline/rate-limited GitHub →
`search` degrades, cached-binary install still works.

---

## 7. Cohesiveness — build once, use many

- **Item A is the keystone.** The same persisted identifiers + `deploytarget` resolver power
  Item B (agent `status`/`logs`/`deep_link`), Item C (human `ls`/`open`/`logs`), the TUI
  `/deploys` upgrade, and Item D's auto-rollback decision (`CanRollback`). Encode per-cloud
  console/logs/rollback knowledge **once**, or it drifts (it already drifts on casing).
- **`logs` is a router in both surfaces.** CLI shells out to the platform CLI; MCP returns
  the command + console URL. Neither ships raw logs by default — consistent with "not a logs
  product" and with the no-secret-egress posture. Reuses the Netlify/Vercel CLI-shell-out
  precedent (`agent-native-plan.md` §2).
- **Shape detection (6a) feeds the mcp-server handshake (D.3) and the agent story (B).**
- **The verify-then-run checksum path (E.4) is one primitive** whether the source is a URL or
  the curated index.

---

## 8. Sequenced roadmap (agent-native first)

- **Phase 0 — immediate agent wins (no deps):** ACB.1 (`list_deploys` returns URL/status),
  ACB.2 (preview-first `plan_digest`), ACB.6 (document `destroy`), ACD.1 (401/403 liveness —
  one-function change, outsized reliability payoff).
- **Phase 1 — keystone:** Item A (persist identifiers + normalize casing + `deploytarget`).
  Fixes a data-loss bug and unblocks everything.
- **Phase 2 — the wedge:** Item B rich tools (`status`, `deep_link`, `logs`). The ICP's
  sharpest differentiator; mostly reuses A.
- **Phase 3 — human launcher:** Item C (`ls`/`open`/`logs`, TUI `/deploys` upgrade).
- **Phase 4 — reliability:** Item D auto-rollback (conditional) + durable state.
- **Phase 5 — coverage + ecosystem:** Item E — `prod plugin new` early (cheap; unblocks
  external contributors → plugin supply); agent detection + Modal GA + mcp-server handshake as
  a live account allows; curated git index next; remote install + runtime hardening gated on
  the `l3` security bar. **No hosted plugin registry and no hosted dashboard** — both are
  commercial-tier decisions on the far side of the open-core line.

**Do-not-build (this plan):** a logs/metrics product; a hosted plugin registry service; the
web dashboard (all commercial-tier per the ROADMAP open-core boundary).

---

## 9. Load-bearing file map (verified)

- Deploy FSM + one-shot: `cli/cmd/main.go:129`, `cli/cmd/root/root.go:88-142`,
  `cli/cmd/run/run.go:70`, `cli/internal/agent/agent.go` (`DriveOneShot` :166, `proceedWithPlan`
  :588, `executeDeployment` :1228)
- History store + schema: `cli/internal/history/store.go:17-28`; writers
  `cli/internal/agent/planning.go:336-455`; local-mode gate `cli/internal/config/config.go:52`
- Adapter interfaces: `cli/internal/deployment/deployment.go:33-66`; container id-drop seam
  `cli/internal/deployment/managedcontainer/managedcontainer.go:70-78`
- Per-cloud success metadata: `workflow_flyio.go:260`, `_heroku.go:239`, `_netlify.go:207`,
  `_vercel.go:199`, `_render.go:323`, `_container.go:130`, `_modal.go:81`
- Rollback support + auto-detect: `cli/internal/agent/platforms.go:137-239`,
  `cli/internal/agent/planning.go:570-628`, `cli/internal/agent/workflow_rollback.go`
- Liveness: `cli/internal/agent/monitoring.go:85-111`
- Workflow durability (dormant): `cli/internal/workflowext/workflowext.go:46-86`,
  `cli/cmd/main.go:51-54`
- MCP server: `cli/internal/mcpserver/server.go`, `tools.go`, `deploy.go` (gate :42-72);
  entry `cli/cmd/mcp/mcp.go`
- Plugin host + SDK: `cli/pkg/plugin/{provider.go,rpc.go,serve.go}`,
  `cli/internal/pluginhost/{client.go,install.go,manifest.go,bridge.go}`,
  `cli/cmd/plugin/plugin.go`; example `cli/examples/prod-provider-example/main.go`
- Analyzer: `cli/internal/analyzer/{analyzer.go,node.go,python*.go,go.go}`; shape
  `cli/internal/deployment/shape.go`
