# OSS roadmap: owning agentic deployment

**Status:** proposed · **Scope:** the open-source `prod` binary only · **Audience:** maintainers

This roadmap turns one strategic bet into a sequenced, buildable plan: **prod should be the
neutral, safe deploy primitive that AI coding agents call** — not a better flyctl, not an IaC
replacement. Platform vendors will each bolt an MCP tool onto their own product; none will build a
*cross-cloud* deploy interface with a *safety model* and a *verify/heal loop*. That intersection is
what prod can own.

It builds on — and does not re-litigate — the existing design docs:
[agent-native-plan.md](./agent-native-plan.md) (the deploy-shape model, now largely shipped),
[agentic-deploy-plan.md](./agentic-deploy-plan.md) (the MCP surface + launcher), and
[agent-deploy.md](./agent-deploy.md) (the user-facing MCP guide).

## The OSS / commercial boundary (read this first)

Every item below is scoped so the **OSS binary stays local and single-user**. The rule:

> **In OSS** if it makes *one developer's* agent deploy work and feel safe on *their own machine*.
> **Commercial** if it *coordinates that across a team or org* (centralized audit, org-wide policy
> enforcement, shared history, RBAC/SSO, approval routing).

The OSS governance primitives here (local `policy.yaml`, local `audit.log`) are deliberately the
*on-ramp* to the commercial control plane, not a giveaway of it. Where a feature has a commercial
sibling, it's called out under **Commercial boundary**.

## What's already true (grounded, so we don't re-scope done work)

The research pass that produced this doc found several things CLAUDE.md still describes as future:

- **The `out.(TUIWriter)` panic is fixed.** All call sites use guarded comma-ok with a plain-text
  fallback (`internal/agent/agent.go:355-494`, `slash_commands.go:49-93`), regression-tested in
  `internal/agent/console_fallback_test.go`. `ConsoleWriter` fully implements the events CLAUDE.md
  says it no-ops (`internal/output/writer.go:262-310`); the real no-ops now live in `TeaWriter`
  (`internal/tui/teawriter.go:92-115`). A cross-writer no-panic parity test already exists
  (`internal/output/writer_parity_test.go`). **→ CLAUDE.md §6/§10 are stale; fix in W0.**
- **Deploy shapes are implemented end-to-end.** `web|mcp-server|worker|cron` exist
  (`internal/deployment/shape.go:13-18`), flow Intent→plan→spec, and Fly + Render deploy
  worker/cron for real (`flyio/queued.go:388`, `render/queued.go:309-324`). Liveness is correctly
  shape-conditional (`internal/agent/monitoring.go:107-117`). **→ "finish shapes" (W5) is a small
  gap-closing job, not a build.**
- **Agent-framework detection exists.** `internal/analyzer/shape.go:13-17` detects langchain,
  langgraph, llama-index, crewai, autogen, agno, smolagents, Mastra, OpenAI Agents, and MCP SDKs —
  wired into Node/Python/Ruby/Rust/Java/C#/Elixir analyzers. **Go is the only gap.**
- **Cost is available at plan time** (`plan.Pricing.Total`, set in `internal/agent/planning.go:303`)
  — but only for 6 platforms; container/AWS/Modal can leave it `0`.
- **There is no environment/stage model and no config-file loader.** Both are greenfield (W1).

## Two architectural facts every workstream must respect

These were confirmed during review and are easy to design around wrongly:

1. **The MCP mutating tools run the real deploy in a separate OS process.** `deploy`/`rollback`/
   `destroy` shell out via `runProd` → `exec.CommandContext(ctx, exe, "run", "--", prompt)` with
   `PROD_JSON_MODE=true` in the child env (`internal/mcpserver/deploy.go:123`), and talk only over
   stdin/stdout JSON. So *anything cross-cutting that must reach an MCP-driven deploy — the
   initiator (W3), a stage/policy signal, a correlation id — has to cross that boundary explicitly*,
   as a new child-read env var (mirror `PROD_JSON_MODE`), not an in-process Go call. Every
   workstream that touches the MCP path inherits this.
2. **The real cross-version compatibility surface is `~/.prod/history.json`, not durable workflow
   state.** Workflow state is intentionally in-memory and wiped each run (`cmd/main.go` configures
   `workflowext.WorkflowsConfig{}` with no `SQLitePath`), so the append-only-enum discipline
   (`types.go:22-26`) is defense-in-depth, not guarding a live bug. The artifact a newer binary
   actually re-reads is `history.Record`, which is additive-safe because `Metadata` is
   `map[string]any`. Design new persisted fields against *that*.

## Workstream summary & sequencing

| # | Workstream | Why it's OSS | Depends on |
|---|------------|--------------|-----------|
| **W0** | Contract hardening (version + freeze) | Agents *are* the API consumer; the contract must be stable | — |
| **W1** | Local policy + safe-by-default environments | The trust unlock; on-ramp to commercial policy | W0 |
| **W2** | Verify → diagnose → heal loop | The differentiation no platform wrapper builds | W0 |
| **W3** | Local audit log | Cheap trust; on-ramp to commercial audit | W0, W1 |
| **W4** | Deploy memory for agents | Novel stickiness | W0, W2, W3 |
| **W5** | Finish non-HTTP shapes | "Deploy the agent itself" is half the category | — |
| **W6** | Agent-framework wiring & MCP ergonomics | Distribution *is* the agent ecosystem | W0 |
| **W7** | "Safe agent deploy" reference model (docs) | Category owners write the definition | W0–W4 land first |

**Order:** W0 → W1 → W2 → (W3, W4) → W5/W6 in parallel → W7 last (documents what shipped). W5 and
W6 have no *logical* dependency on W0–W4, but **W1 and W5 both edit the same ~50-line region of
`planning.go` (≈157–192)** — the stage parsing and the shape reconciliation — so land one before
starting the other or expect merge friction even though they're architecturally independent.

---

## W0 — Contract hardening: version and freeze the machine surface

**Goal.** An agent (or an MCP host) can depend on prod's JSON events and MCP tool schemas not
changing under it. Today the surface *works* but is unversioned and internally inconsistent.

**Current state (grounded).**
- JSON events are emitted as JSON Lines by `JSONWriter` (`internal/output/writer.go:411-572`).
  Three events use a typed `JSONEvent` struct (`writer.go:404-409`); the rest are hand-built
  `map[string]interface{}` literals (`writer.go:477,492,519,537,555`) with **inconsistent timestamp
  formats** (`time.Time` vs `time.Now().Format(RFC3339)` strings) and an **in-place mutation of the
  caller's `plan` map** (`writer.go:524`). No `event_version`/`schema` field anywhere.
- MCP tool schemas *are* typed Go structs with `jsonschema` tags across
  `internal/mcpserver/{server,tools,status}.go` — the strongest-typed surface — but have no version
  field and no schema snapshot; status strings (`preview|success|failed`) are comment-only enums.
- The MCP layer consumes the **untyped** JSON stream (`internal/mcpserver/deploy.go:70-100`), so the
  typed MCP contract sits on top of the unversioned JSON one.

**Scope.**
1. Introduce a single **canonical event struct** family (extend/replace `JSONEvent`) that every
   `StatusWriter` method constructs once; writers *render* it (JSON serializes, Console formats,
   Tea maps or explicitly opts out). Kills the map literals, the timestamp inconsistency, and the
   in-place mutation.
2. Add an `event_version` (integer) to the canonical event and a `contract_version` to the MCP
   server (`server.go:31` already has a `Version`). Document the status enums as typed constants.
3. **Golden snapshots:** a stored JSON-events golden and a serialized MCP JSON-Schema golden for all
   9 tools, diffed in CI so any field/enum change is a deliberate, reviewed break.
4. Close the `TeaWriter` parity gap: add a no-panic conformance case for `TeaWriter`
   (`writer_parity_test.go` currently excludes it to avoid an import cycle — put a shared harness in
   a leaf package or a `tui`-side test).
5. **A stable `deployment_id` correlation handle.** Add `deployment_id` to `deployOutput` and the
   `deployment_complete` event so an agent can thread preview → deploy → verify → recall → audit
   across calls. Today `status`/`deep_link`/`logs` key off *app name* (`status.go:12`), which is
   ambiguous when the same app exists on two platforms — the id is the fix, and W2/W4 tools accept
   it. Reuse the local record `ID` already generated at `planning.go:388`.
6. Make the `planDigest` mismatch error actionable: instead of the generic "preview first", say
   "prompt or path differs from the previewed plan — re-preview" so an agent knows it's expected
   drift, not a bug (`internal/mcpserver/server.go:78-88`).
7. Fix stale **CLAUDE.md §6/§10** and the stale note in this repo's `agent-native-plan.md` intro.

**Acceptance criteria.**
- [ ] Every JSON event carries `event_version` and an RFC3339Nano timestamp in one format; no
  writer builds a `map[string]interface{}` for an event.
- [ ] A single canonical event object is the sole input to all writer render paths; adding a new
  event to one writer without the others fails a test.
- [ ] `go test ./internal/output/... ./internal/mcpserver/...` includes a golden-schema test that
  fails on any unversioned change to JSON events or MCP tool I/O.
- [ ] `TeaWriter` is exercised by a no-panic parity check.
- [ ] Bumping a contract requires bumping `event_version`/`contract_version`; documented in a new
  `docs/protocol.md` (the wire contract, versioned).
- [ ] CLAUDE.md no longer describes the panic or the ConsoleWriter no-ops as live.

**Technical plan.**
- New `internal/output/event.go`: canonical `Event` structs + `event_version` const. Refactor
  `writer.go` methods to build an `Event` and hand it to a `render(Event)` per writer.
- `internal/mcpserver/schema_golden_test.go`: reflect each tool's input/output to JSON Schema,
  compare to `testdata/*.golden.json`.
- `internal/output/events_golden_test.go`: drive `exerciseAllEvents` (already exists,
  `writer_parity_test.go:17`) through `JSONWriter`, snapshot the JSONL.
- Leaf-package conformance harness so `TeaWriter` can be parity-tested without the import cycle.

**UX plan (agent-facing DX is the UX here).**
- `docs/protocol.md`: the stable event list, field tables, versioning policy ("we bump
  `event_version` on any breaking change; additive fields are non-breaking"). This is a
  category-credibility artifact — publish it prominently.
- Human-facing output is unchanged; this is purely contract discipline. No visible regressions is
  the UX bar.

**Risks.** Refactor touches every writer — the existing parity + JSON content tests
(`writer_parity_test.go:86-178`) are the safety net; extend before refactoring. Don't change the
*shape* of existing events in the same PR that introduces versioning (version first at v1 = today's
shape, then evolve) so downstream MCP parsing (`deploy.go:70-100`) keeps working.

**Commercial boundary.** None — this is pure OSS foundation.

---

## W1 — Local policy + safe-by-default environments

**Goal.** A developer can declaratively constrain what any agent-issued deploy may do — and prod
defaults to *staging/preview*, requiring an explicit escalation to touch production. This is the
single most important trust feature for agent deploys.

**Current state (grounded).** No environment/stage concept governs deploys (`config.GetEnvironment`
is prod's *own* build channel, `internal/config/config.go:17-24`, unrelated). No config-file loader
exists — `internal/config` resolves env→ldflags→default only; `policy.yaml` would be the **first
file tier**. Every action funnels through one choke point, `proceedWithPlan`
(`internal/agent/agent.go:687`), holding a fully-populated `DeployPlan` (platform, action, shape,
`Pricing.Total`). `Shape` is already threaded Intent→DeployPlan→DeploymentSpec — the proven
template for adding the `DeployStage` field.

**Scope.**
1. `policy.yaml` (project `.prod/policy.yaml`, then `~/.prod/policy.yaml`) with: `allowed_clouds`,
   `allowed_stages`, `forbidden_actions` (e.g. `destroy`), `max_monthly_cost` + `on_unknown_cost`,
   `resource_allow`/`deny`, `require_human_for` (actions that may never run under `--yes`/agent
   `confirm=true` without an interactive human), and `default_stage`.
2. A **policy gate** in `proceedWithPlan`, after `shouldProceed`/`refuseDeployPlatform` and before
   the mode branch — one check covering deploy, rollback, destroy across interactive, `--yes`, and
   MCP paths. It runs before the `--dry-run` branch, but **dry-run still renders the plan** and
   *annotates* what policy would block (dry-run mutates nothing and must stay inspectable).
3. A first-class stage field — **named `DeployStage`, NOT `Environment`** (there is already a
   `config.Environment`/`GetEnvironment` for prod's own build channel, same three-way vocabulary,
   same `"staging"` default — a guaranteed source of confusion). Values `staging`|`production`,
   threaded Intent→`DeployPlan`→`DeploymentSpec`.
4. **`staging` must be a genuinely distinct target, not a label** — a separate app/service name
   *and* separate backing resources — on platforms with no native stage concept (Fly/Render/Heroku),
   or the "conservative by default" claim is theater and, worse, silently changes where a deploy
   lands.

**The zero-config path is sacred (this is the fix for the biggest UX trap).** Stage-defaulting is
**gated on policy presence**. With no `policy.yaml`, `prod "deploy this to fly"` deploys *exactly as
asked* — no forced staging, no new vocabulary. Safe-by-default staging activates only when a policy
declares `default_stage: staging`. Governance is opt-in; the naive "just get me live" path never
regresses.

**Acceptance criteria.**
- [ ] With no `policy.yaml`, behavior is **byte-for-byte unchanged** — no staging default, no new
  prompts (fail-open on absence, fail-safe on presence). A test drives `prod "deploy…"` with no
  policy and asserts the resolved target is exactly what was requested.
- [ ] A `policy.yaml` that forbids `destroy` causes `prod "destroy…"` and the MCP `destroy` tool to
  refuse *before any mutation*, with a clear reason — verified on the `proceedWithPlan` gate for all
  three entrypoints (interactive, `--yes`, MCP-over-subprocess).
- [ ] `max_monthly_cost` blocks a plan whose `Pricing.Total` exceeds it. `on_unknown_cost` applies
  **only when a cap is set**; default is **`allow` + warn**, not `block` — because container/AWS/
  Modal report `Pricing.Total==0`, so a `block` default would silently wall off those clouds the
  instant anyone sets a cap. A block/allow denial names the platform and the exact escape hatch.
- [ ] When `default_stage: staging` is set, `production` requires an explicit `--production` flag or
  `"...to production"` phrase; the **resolved target name** (e.g. `myapp-staging`) and stage appear
  in the approval summary — the human never gets a surprise target.
- [ ] `staging` produces a genuinely separate app/service and separate backing resources (tested on
  Fly + Render), not just a name suffix on the same target.
- [ ] `policy.yaml` loads at `0600`/dir `0700`; a malformed policy fails closed **with the file
  path, line, and offending key**, and suggests `prod policy test`.

**Technical plan.**
- `internal/config/policy.go`: `LoadPolicy()` (first file-tier in the config package). Note
  `gopkg.in/yaml.v3` is **already an indirect dependency** (via go-workflows, ecdysis, sentry, the
  Azure SDK) — promoting it to direct is a `go.mod` bookkeeping change, not new supply-chain
  surface. Load order mirrors `auth/storage.go:23` (`homeDir/.prod`), project-dir override on top.
- `internal/agent/policy.go`: `func (a *Agent) enforcePolicy(plan *DeployPlan) error`, called at
  `agent.go:~713`. Denial returns via the existing refuse pattern (`agent.go:710-712 → a.done()`).
- Add `DeployStage` to `DeployPlan` (`agent.go:101`) and `DeploymentSpec`
  (`internal/deployment/deployment.go:77`) — with a disambiguating comment at both sites pointing at
  `config.GetEnvironment` so no one conflates them. Populate in `planning.go`; keep it a
  string/append-only enum. The real back-compat surface is `history.json` (see the architectural
  facts above), which is additive-safe. A new `intent.baml` field + `make generate` if parsed from NL.
- Inject the loaded policy into `NewAgent` (`agent.go:89`) from `cmd/main.go`.

**UX plan.**
- `prod policy init` scaffolds a commented `policy.yaml` with safe defaults. `prod policy test`
  (NOT `check` — `check` reads like "lint the file"; `test` = "evaluate a would-be plan against the
  policy") dry-evaluates the current project's likely plan without deploying.
- `prod doctor` reports guardrail state so the features are discoverable without reading docs:
  `Policy: none (deploys run unrestricted)` / `Default stage: staging` / `Audit: ~/.prod/audit.log`.
- The approval summary (`confirmMessage`, `agent.go:744`) is enriched — especially for a
  production escalation or an agent-initiated deploy — to carry: **resolved target name**, stage,
  resources to be created, env-var *key* count, rollback availability, and (once W4 lands) the
  **diff from the last deploy**. A bare cost line is not enough context for a human to catch a bad
  agent plan. When a policy is active it adds "Policy: ✓ passed (N rules)".
- Denials read as outcomes, and match the tone of the BAML plain-language summarizer (route the
  human-facing denial string through the same presentation layer so users don't feel two tools):
  *"Blocked by policy: destroy is not allowed in this project (.prod/policy.yaml). Remove it from
  `forbidden_actions` to permit."*

**Risks.** The `--yes` (`agent.go:835`) and MCP paths must route through `proceedWithPlan` — they do
(the MCP subprocess drives the same FSM over JSON), but any stage/policy signal the MCP path needs
must cross the process boundary as a child-read env var, not a Go call (see architectural facts).
Verify the funnel with a test so a future refactor can't create a bypass. Keep the policy schema
small in v1; resist a mini-language.

**Commercial boundary.** OSS = a *local* file enforced on *this* machine. Commercial = org-wide
policy distribution, signed/central policy that a developer can't edit away, and per-team/per-agent
policy binding.

---

## W2 — Verify → diagnose → heal loop (the differentiation)

**Goal.** A deploy returns *agent-legible proof it worked* — or a *typed diagnosis and a fix to
try* — not a flat `status: "failed"` string. This is the loop a single-platform MCP wrapper won't
build.

**Current state (grounded).** `verifyLiveness` (`internal/agent/monitoring.go:107`) is the single
shape-conditional chokepoint but returns only `error` (nil/err). The MCP `status` tool duplicates
the liveness rule in `probeLive` (`internal/mcpserver/status.go:36-51`). `SummarizeDeployError`
(BAML, `baml_src/intent.baml:287`) returns a lightly-structured `Error{summary, remediations[]}` —
but **no category/cause/retryable**, and the terminal completion event **drops remediations
entirely**, passing only a flat `errorMsg` string (`writer.go:145`, emit at `writer.go:488-493`).
Auto-rollback is duplicated across 6+ `workflow_*.go` files.

**Scope.**
1. Make `verifyLiveness` return a typed **`VerifyResult`** `{shape, url, probe:
   http|mcp-initialize|skipped, status_code, live, reason}`; back both the deploy path and the MCP
   liveness surface with it so they can't drift. Note: inside a deploy workflow the worker/cron
   `probe: skipped` branch is currently unreachable (all 7 workflows short-circuit before
   `AgentVerifyLiveness` for non-HTTP shapes, e.g. `workflow_container.go:117`) — that branch only
   becomes live once the new re-probe path (below) calls `verifyLiveness` outside a workflow.
2. Enrich the BAML `Error` class with `category`, `cause`, `retryable bool`, `blame`. **`category`
   and `blame` must be BAML `enum` types, not raw `string`** — there are no BAML enums in the repo
   today, so this is a net-new pattern, but it's what makes "an agent can branch on `category`"
   schema-enforced rather than prompt-hope. Define precedence in `protocol.md`: **`retryable` is the
   branch signal (act on it); `blame` is advisory (explains).** Carry the fields through
   `deployError` and **surface remediations in the completion event** (they currently never reach an
   agent — dropped at `writer.go:145`).
3. **Do not add a second liveness tool.** The reviewer's-right call: `verify` vs `status` is a
   coin-flip for an agent. Instead, make the existing `status` tool return the `VerifyResult` shape
   with a `reprobe bool` input (default false = fast lookup; true = active re-probe). One tool, one
   mental model. (If a standalone `verify` is kept for discoverability, its description must say, in
   these words, "call after a deploy for machine-checkable proof; use `status` to look up a past
   deploy.")
4. `deployment_id` (from W0) is accepted by `status`/`recall` so an agent ties the loop together
   without re-guessing the record by app name.
5. Plumbing only for a future `heal`/`retry` primitive that composes the existing
   `getPreviousDeployment`/`rollbackDeployment` verbs. **Do not register a `heal` MCP tool until it
   does real work** — a registered no-op invites agents to call it. Until then, document the
   "retry = re-deploy with a fix" pattern in `protocol.md`.

**Acceptance criteria.**
- [ ] `verifyLiveness` returns `VerifyResult`; worker/cron produce `probe: skipped, live:
  assumed` (distinct from probed-live), never a false "live".
- [ ] The `deployment_complete` event and the MCP `deploy` result include the structured diagnosis
  (`category`/`blame` as enums, `cause`, `retryable`) and the remediation list when a deploy fails.
- [ ] `status` returns the `VerifyResult` shape and accepts `reprobe` + `deployment_id`; its
  liveness rule is the *same code* as the deploy path (the duplicated ≥500 rule at `status.go:36`
  and `monitoring.go:139` is unified onto `VerifyResult` — no second copy).
- [ ] Given a known failure fixture (e.g. missing `PORT`), the diagnosis `category` is a stable enum
  value an agent can branch on — one fixture test per enum value, using the `llm.Client` mock.
- [ ] No behavioral regression to auto-rollback for web/mcp-server shapes, and no `heal` tool is
  registered until it performs a real repair.

**Technical plan.**
- Define `VerifyResult` in `internal/agent/monitoring.go`; unify `probeLive`
  (`mcpserver/status.go:36`) onto it (they're already commented as intentionally identical).
- BAML: extend `Error` in `baml_src/intent.baml:20-23`; `make generate`; map new fields in
  `internal/agent/errors.go:48-58` into `deployError` (`types.go:53-58`).
- Widen `SendDeploymentComplete` (`writer.go:145`) to carry a structured diagnosis + remediations;
  update all writers (this rides on W0's canonical event object — do W0 first).
- Add `reprobe` + `deployment_id` inputs to `status` (`mcpserver/status.go`) and return the
  `VerifyResult` shape — do **not** register a separate `verify` tool (agents can't reliably choose
  between two liveness tools). A `heal` tool and a `repairDeployment` activity
  (`internal/agent/activities.go:86-109`) are **deferred, not stubbed** — don't register `heal`
  until it performs a real repair. The refactor of 6+ duplicated auto-rollback blocks into one
  primitive is the risky part and shouldn't block the verify/diagnose value.

**UX plan.**
- Human output: on failure, print the diagnosis one-liner + the top remediation with its
  `cliCommand` (remediations already exist for humans via `deployError`; W2 just stops dropping
  them for machines). On success for a web/mcp shape, print a compact "✓ verified live (200, 0.4s)".
- Agent DX: `status` (with `reprobe: true`) returns a small, branchable object. Document the
  `category`/`blame` enum values and the `retryable`-is-the-branch-signal rule in `docs/protocol.md`
  so agents switch on the right field.

**Risks.** `rollback` identifier is overloaded (image URL for Fly/AWS vs deployment ID for native
platforms, `flyio/queued.go:571-588`) — a heal primitive must not assume a uniform ID; that's why
`heal` is sequenced after the verify/diagnose value. Don't regress the apologetic, OS-tailored human
prose the BAML prompt produces (`intent.baml:289-359`) while adding machine fields — they coexist.

**Commercial boundary.** OSS = per-deploy verify/diagnose on your machine. Commercial = fleet-level
health rollups, cross-deploy failure analytics, and org policy that auto-quarantines an agent after
N failed heals.

---

## W3 — Local audit log

**Goal.** An append-only local record of every agent-issued action: who/what/when, the approved
plan, and that a human confirmed. Cheap, trust-building, and the on-ramp to commercial audit.

**Current state (grounded).** `history.Store` records deploy/rollback/destroy start + final status
into `~/.prod/history.json` via one choke point (`internal/agent/planning.go:375,460`), but: **no
actor** (an MCP/agent deploy is indistinguishable from a human CLI deploy — the `source` arg is the
project path, not the initiator); **the approved plan is never persisted** (assembled at
`agent.go:381-399`, emitted, discarded); **no approval timestamp/approver**. History is a
full-file overwrite (`store.go:198`) — freely rewritable, not tamper-evident.

**Scope.**
1. An append-only `~/.prod/audit.log` (`O_APPEND`, `0600` under the `0700` dir), one JSON line per
   event, optionally hash-chained (prev-hash field) for tamper-evidence. `history.json` stays the
   mutable index; the audit log is the immutable ledger.
2. Capture, at the existing choke points: `initiator` (cli|mcp|agent-id, threaded from
   `mcpserver/server.go:90`), the **approved plan snapshot** (the `planData` map at
   `agent.go:381-399`), and `approved_at` + approval mode (interactive|--yes|mcp-confirm).

**Acceptance criteria.**
- [ ] Every deploy/rollback/destroy appends exactly one immutable audit line with `initiator`,
  `action`, `platform`, `environment`, `approved_at`, `approval_mode`, and the plan snapshot.
- [ ] An MCP-initiated deploy is distinguishable from a human CLI deploy in the audit log.
- [ ] The audit log is append-only (`O_APPEND`); a test asserts prior lines are never rewritten.
- [ ] `prod audit` (and an MCP read tool) can list recent audited actions; secrets are never written
  (env *values* excluded; keys/counts only).
- [ ] **An audit write failure warns and continues — it never blocks the deploy.** The deploy is the
  product; the ledger is the receipt. (Read-only home / full disk must not brick shipping.)
- [ ] Optional `--verify` recomputes the hash chain and reports tampering.

**Technical plan.**
- `internal/history/audit.go`: `AppendAudit(entry)` with `O_APPEND` (safer than the read-modify-write
  `save()` — also fixes the cross-process gap the current `sync.Mutex` at `store.go:65` doesn't
  cover). Write beside `DefaultPath` (`store.go:69`).
- Thread `initiator` **across the subprocess boundary** — `runProd` (`mcpserver/deploy.go`) spawns
  a child process, so set `PROD_INITIATOR=mcp` on `cmd.Env` (mirroring the existing
  `PROD_JSON_MODE=true`) and read it via `os.Getenv` on the child side before `logDeploymentStart`
  (`planning.go:375`). CLI entrypoints set `cli`. This is **not** an in-process call chain.
- Persist the approval snapshot (the `planData` map at `agent.go:381-399`, today assembled and
  discarded) at the confirm gate.
- Redaction: reuse the env-var sensitivity classification from the categorize step so secret
  *values* never land in the ledger — keys/counts only.

**UX plan.**
- `prod audit` prints a readable, most-recent-first table (time · initiator · action · platform ·
  env · approver/mode). `prod audit --json` for machines; `prod audit --verify` for the chain.
- Emphasize in docs: "your machine keeps the receipts" — this is the sentence a security-minded
  team wants to hear, and it seeds the commercial centralized-audit upsell.

**Risks.** Don't let the audit log and `history.json` drift into two sources of truth — audit is
append-only ledger, history is the queryable index; both written from the same choke point in one
activity. Hash-chaining is optional in v1; a simple monotonic sequence + append-only is enough to
start.

**Commercial boundary.** OSS = local ledger on your machine. Commercial = centralized, tamper-proof,
queryable org audit with retention, export, and compliance reporting. This is the clearest
open-core line in the roadmap.

---

## W4 — Deploy memory for agents

**Goal.** prod becomes the context an agent reads *before* it deploys and writes *after* — last-good
config, the diff from the last deploy, and *why the last one failed*. No CLI is a memory layer.

**Current state (grounded).** Failure metadata (`error`, `stage`, `rollback_error`) *is* written to
`Record.Metadata` (`workflow_flyio.go:229`, `workflow_container.go:178`) but **no MCP tool exposes
it** — `list_deploys` and `status` strip metadata to a fixed field set. Build/start commands,
env-var decisions, service requirements, and pricing are **never persisted** locally (managed mode
captures more, `planning.go:424-433`, but that's backend-only). No config diffing between runs
exists anywhere.

**Scope.**
1. Persist the "last-good config" locally: on a successful deploy, snapshot the resolved
   build/start commands, shape, env-var *keys* (not values), service requirements, and pricing into
   the record (fields already collected in managed mode — just persist them in local mode too).
2. A **`recall` MCP tool**: returns the last successful config for an app, the latest *failed*
   record's diagnosis, and (given two records) a **diff**.

**Acceptance criteria.**
- [ ] After a successful deploy, `recall <app>` returns the last-good build/start/shape/env-keys and
  the recorded pricing.
- [ ] `recall <app>` surfaces the most recent failure's structured diagnosis (from W2) — the data
  already exists in metadata; W4 exposes it.
- [ ] A `diff` between two deploy records reports changed commands/shape/env-keys — net-new, since no
  cross-record comparison exists today.
- [ ] Env-var *values* are never returned; keys/counts only.

**Technical plan.**
- Extend the persisted record (reuse `Record.Metadata`, `store.go:48`) at
  `updateDeploymentStatus` (`planning.go:460`) to include the last-good config on success.
- `internal/mcpserver/recall.go`: read via `history.LatestForApp` (`store.go:29`) for last-good and a
  new "latest failed" variant; register in `server.go:34-42`. Diff logic lives beside
  `deploytarget.Resolve` (`deploytarget.go:47`), the canonical record→view layer.
- Read `Record.Metadata` directly (not through the field-stripping DTOs).

**UX plan.**
- **Disambiguate the read surfaces up front** — `ls`/`status`/`recall` overlap and will paralyze
  humans and mis-route agents unless each states when to use it. Ship this exact split in help text
  and in every MCP tool *description* (agents pick tools off the description string alone):
  - `ls` = **browse** history (what deploys exist).
  - `status` = **is it up right now** (+ optional re-probe).
  - `recall` = **what shipped last and what broke last** (config, cost, last failure, diff).
- `prod recall <app>` for humans (what shipped last, what it cost, what broke last time).
- Agent DX: `recall` is the "before you act, here's context" call — document the recommended agent
  pattern (**recall → plan → verify**) in `docs/protocol.md` and the reference model (W7). The diff
  `recall` computes is what feeds the enriched **production-escalation approval summary in W1** —
  that's where the memory earns its keep, so wire them together.

**Risks.** Snapshotting config must exclude secret values (same redaction as W3). Keep the record
schema additive (durable-state + hand-editable history compatibility, `store.go:184`).

**Commercial boundary.** OSS = per-machine memory. Commercial = shared team memory (an agent on CI
recalls what a teammate deployed), cross-project patterns, and org-wide "last-good" registries.

---

## W5 — Finish the non-HTTP shapes (gap-closing, not a build)

**Goal.** Worker/cron shapes are honored consistently across the built-in clouds that can serve
them, and mis-targeting fails early with a helpful message instead of late with "returned no URL".

**Current state (grounded).** Shapes work end-to-end on Fly + Render, but `PlatformSpec.Shapes`
(`internal/agent/platforms.go:60`) is **empty for every built-in** — so `SupportsShape` reports
web-only, and a bare `worker` can be planned onto AWS/GCP/Azure and fail late
(`workflow_container.go:141`). Fly's worker success is *ungated* (`workflow_flyio.go:178`), an
asymmetry with the container path. Go is the only analyzer missing agent-shape detection
(`analyzer/go.go:87`). The scaffolded `prod.template.yaml` `shape:` is **never read at deploy**.

**Scope.**
1. Declare `Shapes: [worker, cron]` on Render and Fly in `platforms.go:159-281` (they already build
   `background_worker`/`cron_job` and portless Fly processes).
2. A **plan-time guard**: extend the cron reconciliation block (`planning.go:174-192`) to also
   handle a bare `worker` on a web-only platform — redirect to a supporting cloud or refuse with a
   clear message, *before* execution.
3. Add `DetectAgentShape` to `analyzer/go.go` (mirror `node.go:194`/`python.go:227`); extend
   `agentFrameworkDeps` (`shape.go:13`) with Go frameworks (e.g. langchaingo, genkit).
4. **Consume the manifest:** a tiny `prod.yaml` reader feeding `spec.DetectedShape` as a
   high-priority signal (`planning.go:157`) so the templates' declared shape is authoritative and
   reduces LLM dependence.

**Acceptance criteria.**
- [ ] `SupportsShape` returns true for worker/cron on Fly and Render; Fly's worker path is gated on
  `SupportsShape` **after** its Shapes are declared (no regression to working Fly worker deploys).
- [ ] A `worker` targeted at AWS/GCP/Azure is caught at plan time with a message naming a supported
  platform — not a late "returned no URL".
- [ ] A Go project importing an agent framework is shape-detected as `worker` (no web server) or
  `mcp-server`.
- [ ] A `prod.yaml` with `shape: worker` makes that shape authoritative over the LLM guess; covered
  by a test.

**Technical plan / risks.** Order matters: declare Fly's `Shapes` **before** gating its worker path,
or you regress working deploys. The manifest reader is net-new but tiny and pure-local. Keep the
container clouds honest — either implement worker support there or guard against it; don't leave the
silent late failure.

**Commercial boundary.** None — pure OSS capability parity.

---

## W6 — Agent-framework wiring & MCP ergonomics (distribution)

**Goal.** Getting prod into an agent is one command, and prod is present where the agent ecosystems
already look. Distribution *is* the agent ecosystem.

**Current state (grounded).** `prod mcp` serves 9 tools over stdio, but wiring is a **manual JSON
paste** (`cmd/mcp/mcp.go:44-47`) — no installer. Templates exist for `agent-worker` and `mcp-server`
(`cmd/new/templates/`). Detectors exist for the major Python/JS agent frameworks.

**Scope.**
1. **`prod mcp install [--client claude-code|cursor|cline]`** — writes the `mcpServers` block into
   the target client's config file (e.g. `~/.claude.json`, Cursor, Cline settings). Pure local
   file-writing, no new deps; idempotent; prints the manual JSON as a fallback.
2. MCP-registry presence + a one-line install in the README and the site (ties to the site work
   already done).
3. Framework templates/examples: ensure `agent-worker` (LangGraph) and `mcp-server` templates are
   current, and add short "deploy this with prod" snippets to the docs for LangGraph / CrewAI /
   Agents SDK / Mastra (detectors already recognize them).

**Acceptance criteria.**
- [ ] `prod mcp install --client claude-code` adds a working `prod` MCP server to the client config
  without clobbering existing servers; re-running is idempotent; `--print` shows the JSON without
  writing.
- [ ] `prod mcp install` with an unknown/あいまい client prints the manual block and exits cleanly.
- [ ] README + docs show the one-liner; `docs/agent-deploy.md` links it.

**UX plan.** The installer must be *safe by default*: back up the target config, never overwrite
unrelated servers, and confirm before writing. Print exactly what it changed. On failure, degrade to
the manual JSON (never leave the user stuck).

**Risks.** Client config formats/locations vary and move — keep a small, well-tested adapter per
client and always fall back to the documented manual block. This is the most "integration rot"-prone
item; keep it thin.

**Commercial boundary.** OSS = wire prod into *your* editor. Commercial = org-managed agent
provisioning, shared server configs, and per-seat distribution.

---

## W7 — Publish the "safe agent deploy" reference model

**Goal.** Own the category definition. A short, citable spec for *how an agent should deploy safely*:
**propose → human-approve → deploy → verify → diagnose → heal**, with the plan/verify/audit schemas
that W0–W4 make real.

**Scope.** `docs/safe-agent-deploy.md` — the model, the state diagram, the JSON schemas (linking
`docs/protocol.md` from W0), the approval-gate semantics (`confirm=false` = plan-only), and the
policy/audit primitives. Frame it as a pattern others can implement, not a prod-only feature — that's
what makes it a *definition* rather than marketing.

**Acceptance criteria.**
- [ ] The doc describes each phase with prod's concrete surface (MCP tool, event, policy hook) and
  links the versioned protocol.
- [ ] It reads as a neutral reference (an agent framework author could adopt the model), and is
  linked from the README and the site's MCP page.

**Depends on** W0–W4 landing so the plan/verify/policy/audit schemas it cites are real, not
aspirational.

**Commercial boundary.** None — it's an OSS artifact whose *value* is category leadership.

---

## Cross-cutting: what stays out of OSS (the moat)

To keep the boundary crisp, these are **explicitly not** in this roadmap and belong to the
commercial control plane: multi-tenant/centralized audit and policy enforcement, teams/RBAC/SSO,
approval routing and org-wide human-in-the-loop, shared deploy history/memory across a team, and
fleet health/analytics. The OSS primitives above (local policy, local audit, local memory) are the
adoption wedge that makes those worth buying.

## Docs hygiene (fold into the first PR that touches each area)

- CLAUDE.md §6/§10: remove the "panic in console mode" and ConsoleWriter-no-op descriptions (fixed);
  update §6 to say the `deployShape` model shipped.
- `agent-native-plan.md` intro: mark the shape model as implemented.
- Add `docs/protocol.md` (W0) and cross-link it from `agent-deploy.md`.

## Suggested milestones

- **M1 (foundation):** W0 + W1. The contract is versioned and agents can't break it; policy +
  staging-default ships as the flagship trust story.
- **M2 (differentiation):** W2 + W5. The verify/diagnose loop and complete shape coverage — the demo.
- **M3 (stickiness + reach):** W3 + W4 + W6, then W7 to document the whole model.
