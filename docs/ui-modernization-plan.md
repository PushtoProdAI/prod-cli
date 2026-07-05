# prod — UI/UX Modernization Plan

**Status:** plan only — no code changes implied by this document.
**Scope:** the user-facing surface is a Bubble Tea TUI + console + JSON writers (there is **no** web frontend). This plan re-fits that surface from its SaaS-backend origins to the product prod is now: local-first, no-account, BYO-credentials, agent-native.
**Method:** synthesized from three independent reviews (architect, UI/UX, product) plus **empirical verification** of the headline flow — which corrected a control-flow claim both agents got wrong.

> The single most important finding, verified by running the binary: **no plain-terminal one-shot deploy completes today.** Fix that first; everything else is polish on a flow that must first work.

---

## 0. Verified ground truth (what actually happens)

Confirmed by building and running `prod`:

| Invocation | Result today |
|---|---|
| `prod "deploy this to fly"` (README headline) | **`Error: unknown command "deploy this to fly"`** — cobra matches the quoted prompt against subcommands before `RootCommand.Args` runs. The documented flagship command does not run at all. |
| `prod run "deploy this to fly"` (plain terminal) | Prints the plan + `Do you want to proceed? (y/n)`, then **exits without deploying** — `run.go` only reads stdin when `PROD_JSON_MODE=true`; nothing answers the prompt. |
| bare `prod` → type a prompt (TUI) | Works. The tea loop drives the multi-state deploy. |
| `prod run "…"` with `PROD_JSON_MODE=true` + a driving client | Works (the client feeds `approved` on stdin). This is the MCP path. |
| MCP `deploy` tool | Works (drives the JSON path with an approval reply). |

Root causes: `interactive` defaults `true` (`agent.go:75`); `proceedWithPlan` takes the interactive branch → `confirmWithPrompt` → `waitForConfirmation` (`agent.go:461,475`); `root.go:93` returns after a single `Process` call with no stdin reader; and cobra intercepts a bare positional as a subcommand (`root.go:62 Args` never gets the chance).

**Already remediated — do NOT re-chase (CLAUDE.md §6 is stale):** the console-mode confirm panic (all `out.(TUIWriter)` assertions are comma-ok, `agent.go:145`), `/login`/`/logout` hidden in local mode (`slash_commands.go:31`), and `ConsoleWriter.SendPlanApprovalRequest/SendEnvVarPrompt/SendDeploymentComplete` implemented (`writer.go:253–293`). Update CLAUDE.md §6 to describe the *current* risk (the writer-interface split, below) instead.

---

## 1. The two structural defects everything else rides on

**A. The one-shot deploy path is incomplete (P0 correctness).** The product's core promise — "describe intent, get a URL" — cannot complete outside the full TUI or a JSON-driving client.

**B. The output layer is split across two writer interfaces (P0 architecture).** `output.StatusWriter` carries structured events; a separate `agent.TUIWriter` carries the *rich* surface (plan, confirm, select, success, error) and is implemented **only** by `TeaWriter`. For console/JSON, `agent.go` hand-writes `fmt.Fprintf` fallbacks and (in JSON mode) re-wraps them as `{"type":"log"}`. Consequences that surface as UX bugs: `--dry-run` emits **no structured plan/cost** in JSON (`sendPlan` is TUI-only); `prod doctor` bypasses the writer entirely (`os.Stdout` + `os.Exit`); the console one-shot can ask you to **approve a spend it never showed** (the plain-text plan fallback drops cost); `deployShape` is plumbed into the plan but rendered nowhere. There is no canonical event object — "the plan" is modeled 4 ways (`DeployPlan`, `PlanDisplayMessage`, an inline `map`, `mcpserver.planSummary`).

Fixing B is **cheaper than it looks** (see Phase 1). The map-based plan event *already* reaches all three writers — `SendPlanApprovalRequest(map)` is implemented on `ConsoleWriter` (`writer.go:273`), `JSONWriter` (`writer.go:478`), and `ProxyWriter` (`writer.go:576`). `--dry-run`/console drops cost only because `ConsoleWriter.SendPlanApprovalRequest` reads just `action/platform/summary` and the agent's map omits `shape` — a ~30-line enrichment, **not** a typed-event-bus rewrite (which is blocked by an import cycle; see Phase 1).

---

## 2. Phased plan (revised after an adversarial code review)

### Phase 0 — Make every deploy path complete (P0, do first)
**Outcome:** a newcomer runs one command in a normal terminal and reaches a live URL; an agent/script can deploy non-interactively.

1. **`prod "<prompt>"` runs the prompt, not "unknown command."** Verified one-liner: `cmd.Args = cobra.ArbitraryArgs` at `cmd/main.go:116` (before `cmd.Execute()`). ecdysis wires `RootCommand.Args` into `PreRunE`, never into cobra's `cmd.Args`, so it defaults to `legacyArgs` → "unknown command" on a non-subcommand positional. `ArbitraryArgs` lets the positional through to `PreRunE`→`RunE`; real subcommands still route by name. Add a nonempty-prompt guard, and note the tradeoff: a mistyped subcommand (`prod deploi …`) now becomes a prompt instead of a typo suggestion — acceptable. *(Effort: **S**.)*
2. **`--yes` / `--confirm` flag** driving `SetInteractive(false)` so `prod --yes "deploy…"` / `prod run --yes "…"` hit the auto-approve path (`a.confirm`, `agent.go:528`) — unreachable today (nothing sets `interactive=false` outside tests). *(Effort: S.)*
3. **Both `prod "…"` AND `prod run "…"` read y/n from the TTY** when interactive + attached — one shared confirm-reading loop (loop `Process` until `IsComplete`). Note the two distinct dead-ends: `root.go:141` calls `Process` once with no reader; `run.go:56` only reads stdin under `PROD_JSON_MODE`. Non-TTY + no `--yes` → an actionable "re-run with --yes or --dry-run" error, never a silent dead-end. *(Effort: M.)*
4. **Co-land help + README** with #1 (today `Docs().Long` = "Prod starts an interactive session by default." and the README headline is the currently-broken form) + a **regression test** that `prod "<prompt>"`, `prod run --yes "<prompt>"`, and the TUI each reach a deploy (mock the workflow). *(Effort: S.)*

### Phase 0.5 — Standalone quick wins (no Phase-1 dependency; two are security/trust)
These were mis-filed as later "chrome"; they're independent and high-value. Do them in parallel with Phase 0.

1. **[SECURITY] TUI history → `~/.prod` at `0600`.** Today it's `/tmp/.prodcli_app_history` (`model.go:157`) — a world-readable, predictable path in shared `/tmp` that leaks the user's prompt history to other local users and invites symlink-clobber; directly violates CLAUDE.md §7. *(Effort: S — elevated to P1.)*
2. **Adaptive theme** — stop forcing `#111827` backgrounds (`styles.go:5–32`) that render a dark rectangle inside a light terminal; use lipgloss `AdaptiveColor` / drop forced backgrounds. The single biggest "feels dated/untrustworthy" lever, and it has zero dependency on the writer work. Add a `NO_COLOR`/monochrome path in the same pass (accessibility). *(Effort: S — bumped to P2.)*
3. **Semantic color from severity, not substring** — `styleLogMessage` (`utils.go:153`) reds any line containing "error"/"failed" (so "0 errors" renders red). Local fix, no event-model dependency. *(Effort: S.)*
4. **De-dup / delete dead `greetUser`** — two copies (`tui/utils.go:42`, `root/root.go:168`); the `root.go` copy is dead (never called). *(Effort: S.)*

### Phase 1 — Enrich the plan event through the existing writers (down-scoped to the 80/20)
**Outcome:** cost-in-console, shape-everywhere, doctor-in-JSON — the entire user-visible payoff — via the writers that already exist.

1. **Enrich the plan map** the agent builds (`agent.go:154–178`) with `shape` and per-service `pricing`; render `pricing`/`shape` in `ConsoleWriter.SendPlanApprovalRequest` (`writer.go:273`); unify the dry-run cost print (`agent.go:444–454`, currently a raw `Fprintf`) to go through the writer. *(Effort: S.)*
2. **Route `prod doctor` through a writer** — wire `rootCmd.Doctor` a `StatusWriter` in `main.go` (only `.Run` gets one today), emit structured per-check results, and return an error instead of `os.Exit(1)`. *(Effort: S.)*
3. **Extend the golden parity test** (`internal/output/writer_parity_test.go`, it exists) to cover cost + shape on the plan event across all writers. *(Effort: S.)*

> **Deferred / optional track (NOT Phase 1):** the "one canonical typed event bus + fold `TUIWriter` into `StatusWriter`" is **blocked by an import cycle** — `internal/deployment` imports `internal/output` (`docker.go:27`, `step_executor.go`, every adapter takes an `output.StatusWriter`), so a `PlanEvent{Cost deployment.CostEstimate, Shape deployment.DeployShape}` cannot live in `output`. It requires a **prerequisite decoupling PR** that moves the progress-sink contract to a leaf package (`internal/status`) so `deployment` no longer imports `output` (~9 files), and a typed *response* channel to replace the stringly-typed approval replies. Treat this as its own **L** item, ride it on the cross-cutting TUI-decouple work, and only pursue it if drift becomes a real cost — for a single binary with three writers, enriching the map (above) is the right-sized fix, not a bus.

### Phase 2 — Trust surface + de-SaaS the framing (P1)
**Outcome:** the no-account promise is felt; money/undo decisions are legible and safe.

1. **One decision block, not two.** A single framed panel: *what will be created · on which platform · shape + its liveness strategy · **Est. ~$X/mo** (per-service)* → immediately followed by `Proceed? [y/N]`. `--dry-run` renders the identical block and stops; `--yes` skips the prompt. Cost and approval never separate. *(Effort: M — builds on the enriched plan event.)*
2. **Cost with a confidence flag** (`estimated | stale | fallback`) — pricing is a live scrape + LLM call (ROADMAP watch-item); never show a bare number as authoritative, in the UI or in MCP `summarizePlan`. *(Effort: M.)*
3. **De-SaaS the language** *(cycle-free, pull forward)* — replace "🔐 Authentication required / Interactive login (recommended)" with platform-scoped copy ("Connecting to your Fly.io account…"); drop the blanket "Checking authentication…" on every input; retire the "cloud assistant / a friend in the cloud" greeting for one stable tool-voice line naming the verbs that matter (deploy · rollback · doctor). *(Effort: S.)*
4. **Rollback discoverable in every success surface** *(cycle-free, pull forward)* — add the "Need to undo this? `prod \"rollback\"`" hint to the TUI `SendSuccess` box, not just console (`agent.go:1102` vs `1115`). *(Effort: S.)*
5. **Mask sensitive env-var input** (TUI already has `EchoMode`, `modes.go:88`); drop the "we'll show it in plaintext" message. *(Effort: S.)*

### Phase 3 — Progress legibility (P1/P2)
**Outcome:** a slow multi-minute cloud deploy never looks hung.

1. **Stepped progress** — an ordered, named step list (detect → prepare → build → push → provision → wait-for-live) with per-step status + elapsed time, driven by typed status events, not spinner-by-substring-match (`teawriter.go:113`). Identical in TUI and console. *(Effort: M.)*
2. **Narrate health-check auto-rollback** — `isURLLive` failure silently rolls back today; say what happened and why. **Depends on the external deployShape-liveness PR** (docs/agent-native-plan.md §1) to also *skip* the HTTP check for non-HTTP shapes. *(Effort: S.)*
3. **Chrome + hygiene:** keybinding cheat-sheet behind a `?` toggle (show only Line x/y · %); OSC-8 clickable success URL; rune-aware table truncation (not byte-length); a small consistent status-icon vocabulary; drop the unused `banner` const. *(Effort: S each.)*

*(The adaptive theme, `/tmp` history, "0 errors" color, and greetUser de-dup moved to Phase 0.5.)*

### Phase 4 — Agent surface (P2)
**Outcome:** an agent can preview, deploy, observe, and recover — safe autonomy, not just deploy.

1. **Grow the MCP toolset** to `plan`, `status`, `rollback` (net-new) — rollback parity with deploy is the safety requirement. *(Effort: M.)*
2. **`shape` param on MCP `deploy`** + surface shape in the plan/preview; success includes a copy-pasteable `mcpServers` block. **Depends on the external deployShape-liveness PR.** *(Effort: S–M.)*
3. **Cost confidence in `summarizePlan`** (same flag as Phase 2.2). *(Effort: S.)*

### Cross-cutting — decouple the TUI state machine (L, incremental)
`model.go` is a ~700-line god-object (~40 fields, six prompt pointers) with a 250-line `View()` (two near-identical branches, magic cursor math); adding a mode touches five sites. Introduce a `Mode`/`Prompt` interface (`View`, `HandleEnter`, `HandleKey`), one impl per mode, and typed response messages replacing the stringly-typed replies. Do it incrementally as Phases 1–3 touch each mode. The optional typed-event-bus track (Phase 1 note) rides here.

---

## 3. Sequencing rationale (revised)

- **Phase 0 is non-negotiable and first** — a UX overhaul on a flow that can't complete is polishing a dead-end. It's small (the headline fix is one line).
- **Phase 0.5 runs in parallel** — independent quick wins, two of them security/trust (`/tmp` history, theme), none blocked by anything.
- **Phase 1 is right-sized, not a rewrite** — enrich the map that already reaches all three writers. The full typed-event bus is **gold-plating** for a three-writer single binary *and* is blocked by the `deployment→output` cycle; it's an explicitly optional L track, not the multiplier the first draft claimed.
- **Phase 2's language + rollback-in-TUI items are cycle-free — pull them forward** alongside Phase 0.5; only the decision-block/cost items lean on the enriched event.
- **Phases 3–4 carry an external dependency** on the deployShape-liveness PR (3.2, 4.2) — named explicitly so it isn't a surprise.
- **Deliberate omission:** no usage analytics/telemetry of these flows — that's correct for a local-first, no-account tool (only opt-in local error reporting via `PROD_SENTRY_DSN` exists). Stated so it isn't re-litigated.

**The 20% that delivers 80%:** Phase 0 (deploy completes — one line + `--yes` + a TTY reader) + Phase 0.5 (theme + the `/tmp` security fix) + Phase 1's map enrichment (cost/shape/doctor through the existing writers) + Phase 2's decision-block/language/rollback items. That turns the headline command from broken into trustworthy and makes console/JSON/agent first-class — without the typed-event-bus churn the first draft over-scoped.
