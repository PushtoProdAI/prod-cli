# prod — post-launch fast-follows (validated plans)

Each item below is **validated against the current code** (what's actually true today),
then given a plan, acceptance criteria, and how to prove it. None blocks the OSS launch;
these are the ranked "make prod win" work after it's public. See
[launch-roadmap.md](./launch-roadmap.md) for the launch blockers themselves.

---

## 1. Console/TUI parity — ✅ RESOLVED (validation, not a plan)

**Claim to validate:** `CLAUDE.md` warned of a console-mode panic — an unchecked
`out.(TUIWriter)` assertion in the confirm path that would crash in console mode (the
default).

**Finding: already fixed and guarded.**
- Every TUI-only render goes through a helper that does the **checked** assertion with a
  console fallback, e.g. `sendPlan` (`agent.go:264-279`): `if tuiWriter, ok :=
  out.(TUIWriter); ok { … return }` then a plain-text branch that prints the plan + shape +
  cost. All 13 `out.(TUIWriter)` assertions in `agent.go` (and the 4 in `slash_commands.go`)
  use the guarded `, ok` form — no unchecked assertion remains on the confirm path.
- The intended anti-drift guard exists: `TestWriterParityNoPanic`
  (`internal/output/writer_parity_test.go`) drives **every** writer (console, JSON, noop,
  proxy) through the full event surface (`SendPlanApprovalRequest`, `SendEnvVarPrompt`,
  `SendDeploymentComplete`, …) and asserts none panics. `sendPlan`'s console fallback is
  additionally covered by `plan_display_test.go` (shape + cost render; web shape suppressed).

**Remaining (small, optional):** the *parity* test covers the `StatusWriter` surface but
doesn't iterate the `TUIWriter`-only fallbacks (`sendSecretPrompt`, etc.) across every
writer. Extend it to call each fallback with a `ConsoleWriter`/`JSONWriter` — closing the
last drift gap. *(S. Nice-to-have; the panic is resolved.)*

---

## 2. Windows — WSL2 now, native deferred

**Validation:** prod is a CGO binary (BAML native shim). The release pipeline
(`.github/workflows/release.yml`) ships **linux/amd64 + arm64**, which run under **WSL2**
unchanged. Native Windows needs a CGO/mingw toolchain for BAML — real pain, no demand
signal yet. `scripts/install.sh` already detects `windows/*` and prints a "build from
source" message rather than silently failing.

**Done here:** [`docs/windows.md`](./windows.md) documents the WSL2 path, and
`install.sh`'s Windows branch now points users at WSL2 (was: a source build needing the
mingw toolchain). Native Windows is deferred.

**Acceptance criteria (to validate):** a Windows user following `docs/windows.md` gets
`curl|sh` → `prod doctor` green → a deploy, entirely inside WSL2 — run on a real Windows
box (the one cross-platform path prod hasn't been exercised on).

**Validation:** run the install one-liner + `prod doctor` + one deploy inside a WSL2
Ubuntu on a real Windows machine (the one cross-platform path prod hasn't been run on).

**Effort:** S (docs). Native Windows: L, deferred until demand.

---

## 3. Cloud Run Secret Manager — ✅ IMPLEMENTED (pending real-GCP validation)

**Shipped:** `gcprun/secrets.go` + the wiring in `gcprun_deploy.go`. Sensitive env now goes
to Secret Manager (create secret + version), the runtime service account is granted
`secretAccessor` via a **read-modify-write** IAM policy update (not a blind replace), and the
container references it via `SecretKeyRef` — bringing Cloud Run to secret parity with App
Runner and Azure. Edge cases handled: Secret-Manager-not-enabled (clear error), 409-on-create
(treated as exists → new version), missing default compute SA (grant fails with a pointed
error), IAM propagation (grant precedes the reference; readiness polling absorbs lag). Pure
logic is unit-tested (secret id, the IAM binding merge, env partition, the SecretKeyRef,
409 detection). **Still MANUAL-VERIFY against a real GCP project** — the live IAM behavior is
the failure mode and can't be exercised without one (same posture as the other cloud adapters).

**Original gap (now closed):** App Runner split secrets and Azure used built-in secrets, but
Cloud Run set sensitive env as plain values. The APIs to fix it exist:
`run/v2` `EnvVar.ValueSource.SecretKeyRef{Secret, Version}` and the Secret Manager REST
client (`google.golang.org/api/secretmanager/v1`).

**Plan:** for each sensitive env var, create/version a Secret Manager secret, grant the
Cloud Run **runtime service account** access, and reference it via `SecretKeyRef` instead
of a plain value.
1. Secret Manager: `Projects.Secrets.Create` (replication: automatic) if absent, then
   `AddSecretVersion` with the value. Secret id = sanitized `<app>-<var>`.
2. **IAM (the crux):** the Cloud Run service runs as a service account (the default is the
   project's compute SA, `<PROJECT_NUMBER>-compute@developer.gserviceaccount.com`). That SA
   needs `roles/secretmanager.secretAccessor` on each secret. Resolve the project number
   (Resource Manager `projects.get`), then set the secret's IAM policy (Secret Manager
   `SetIamPolicy`, read-modify-write, add the binding).
3. Container env: sensitive → `EnvVar{Name, ValueSource:{SecretKeyRef:{Secret: full path,
   Version: "latest"}}}`; non-sensitive stays inline.

**Edge cases:**
- **The IAM grant is the failure mode.** A wrong/missing binding deploys a service whose
  container **crashes at startup** ("cannot access secret"). Grant + verify *before* the
  service references the secret, and surface the error clearly.
- **The default compute SA may not exist.** On a fresh project that enabled only Cloud Run
  (never Compute Engine), `<PROJECT_NUMBER>-compute@developer` isn't created — the grant
  target is absent. Detect this and either create it, use the service's actual SA, or error
  with "no runtime service account — enable Compute Engine or set one."
- **IAM propagation is eventually consistent.** Even a correct grant can lag a few seconds,
  so referencing the secret immediately can transiently fail at container start. Grant →
  verify with a short retry/backoff, not just ordering.
- **Use read-modify-write for the secret's IAM policy** — NOT the blind-replace pattern
  `allowUnauthenticated` uses on the *service* (`gcprun.go:104-117`); a blind replace on a
  secret would wipe other accessors. GetIamPolicy → add binding → SetIamPolicy.
- Secret Manager API not enabled → clear "enable secretmanager.googleapis.com" error.
- A user-set custom runtime SA (not the compute SA) — resolve the service's actual SA from
  the created service, not an assumption.
- Rotating a value on redeploy → `AddSecretVersion` + `Version: "latest"` (or pin).

**Acceptance criteria:** a Cloud Run deploy with a sensitive env var stores it in Secret
Manager (not the service config), the container starts and reads it, and the value never
appears in the Cloud Run service YAML.

**Validation: REQUIRES REAL GCP.** This is high blast radius (a wrong IAM grant = broken
app) and untestable without a project — do not ship on SDK-verification alone. Deploy a
real app with a `DATABASE_URL` secret against a live GCP project; confirm it runs and the
value isn't in the service config.

**Effort:** M–L (the SA/IAM resolution is the weight). Priority: high — it's the last
container cloud not at secret parity.

---

## 4. More languages — Go, Ruby/Rails, Bun

**Validation:** analyzers today are **Node + Python only** (`internal/analyzer/node.go`,
`python*.go`); the extension point is the `analyzer.Analyzer` interface
(`CanHandle`/`Analyze`) plus framework heuristics in `internal/agent`. No Go/Ruby/Bun
detection exists.

**Adding a language touches three places:** an `analyzer/<lang>.go` (implement
`CanHandle`/`Analyze`) + a line in the `analyzers` slice (`analyzer.go:61`, `// TODO add
more analyzers`); a `templates/<lang>.dockerfile` + its `docker.go` map entry; and any
framework heuristics in `internal/agent` (`framework_registry.go`, `frameworks.go`).

**Plan (ranked by reach ÷ effort):**
- **Go — XS, do first.** prod is Go (dogfoodable), and **`templates/go.dockerfile` already
  exists and is registered** (`docker.go:125,150`) — so Go needs *only* `analyzer/go.go`
  (detect `go.mod`, main package, module path) + one slice line. Cheapest possible win.
- **Ruby/Rails — M.** Vibe-coder favorite, but genuinely more work: `analyzer/ruby.go`
  (Gemfile, Rails, bundler, `rails server`/Puma, `RAILS_MASTER_KEY`, asset precompile, DB
  migrations) **plus a new `templates/ruby.dockerfile`** (none today) + framework heuristics.
- **Bun/Deno — S–M.** Agent/edge runtime; detect `bun.lockb`/`deno.json` + the run command
  (+ a template if not covered by the node one).
- Agent *frameworks* (LangGraph/CrewAI/Mastra detectors) matter more than raw languages for
  the agent story — track under the agent-native work (§5), not here.

**Acceptance criteria (per language):** a hello-world app in that language, with no
Dockerfile, deploys to at least one built-in cloud from `prod "deploy this to <cloud>"`.

**Validation:** a real deploy of a minimal app per language to Fly (fast, cheap) in CI's
follow-up or manually.

**Effort:** Go S, Ruby M, Bun S–M. Do Go first (cheapest, dogfoodable).

---

## 5. Agent-native headline — Modal + finish the shape model  `[the differentiator]`

**Validation:** [`agent-native-plan.md`](./agent-native-plan.md) exists (443 lines) but its
"where the code is today" is **stale** — it treats the `deployShape` model as unbuilt. In
fact the foundation shipped: `deployment.DeployShape` + `ParseShape` (`shape.go`),
`DeployPlan.Shape`, and shape-aware liveness (`verifyLiveness` skips the HTTP probe for
worker/cron). The MCP `deploy` preview already surfaces the shape.

**What's actually left (update the plan to say so):**
- **Per-shape liveness ownership** — a `LivenessChecker` interface so worker/cron adapters
  assert liveness their own way (today non-HTTP shapes just skip the probe).
- **`SupportedShapes()` per platform** + planning-time validation (reject "deploy a worker
  to Netlify").
- **The MCP-server shape handshake** — on a successful `mcp-server` deploy, emit a
  copy-pasteable `mcpServers` block so an agent host can wire it in one step.
- **Modal adapter** — serverless **GPU**, Python-native. *"Deploy this agent to Modal with
  an A10G"* in English is a headline no competitor matches, and it's the first genuinely
  non-container, non-HTTP target — the reason the shape work exists. Modal deploys via its
  own CLI/API (not a container registry), so it does **not** fit the L2 managed-container
  base — it's a net-new adapter shape.

**Acceptance criteria:** `prod "deploy this agent to Modal with a GPU"` provisions a Modal
app and returns its URL; a worker/cron deploy to a supporting cloud is reported live
without an HTTP probe; an mcp-server deploy prints a ready-to-paste `mcpServers` entry.

**Validation:** real deploys — a Modal GPU app (needs a Modal account), a worker to Fly,
an MCP server. Modal especially is untestable without an account.

**Effort:** L (Modal is the strategic payload). Land soon *after* launch, not before —
it's the reason to *pick* prod, but it's net-new surface that shouldn't gate going public.

**First: update `agent-native-plan.md`'s §0 to mark the shape foundation done** so the plan
reflects reality. (Minor cleanup while there: `verifyLiveness` hardcodes `shape == Worker ||
Cron` — use the existing `HTTPShaped()` helper when the `LivenessChecker` lands.)

---

## 6. Missing operator commands — `destroy` and logs

**Validation:** `cmd/` has only `doctor`, `mcp`, `plugin`, `root`, `run`. prod can deploy
and roll back, but there is **no teardown and no log access** — the `Delete`/log methods
that exist are internal adapter methods, not user commands. For a deploy tool these are
table-stakes operator gaps once someone's app is live.

- **`prod destroy`** (or `teardown`) — delete a deployment + its provisioned resources, with
  the same plan→approve gate as deploy. The per-adapter delete logic largely exists
  internally; the gap is a workflow + command + an MCP tool. *(M. High value — right now the
  only way to clean up is the cloud console.)*
- **`prod logs`** — stream/tail a deployment's logs. Each platform has a logs API; a
  shape-aware `logs` command (and MCP tool) closes the deploy→debug loop. *(M.)*
- **Cost-confidence flag** (`estimated | stale | fallback`) — the roadmap's Part 3D item;
  the preview already shows "Estimated cost", but there's no confidence signal. Pairs with a
  pricing cache. *(M.)*

Acceptance: `prod "destroy this on fly"` removes the app + DB after approval; `prod logs`
streams a running deploy's output; the cost preview labels its confidence.

---

## Ranking (reasons-to-choose × reach ÷ effort)

1. **Go language** (§4) — nearly free (Dockerfile template already exists), dogfoodable.
2. **`prod destroy`** (§6) — table-stakes teardown; the delete logic mostly exists internally.
3. **Cloud Run Secret Manager** (§3) — closes secret parity; real-GCP validation gates it.
4. **Agent-native completion → Modal** (§5) — the differentiator; larger, land after launch.
5. **`prod logs`** (§6) + **Ruby/Rails, Bun** (§4) — steady operator + funnel widening.
6. **Cost-confidence flag** (§6), **WSL2 docs** (§2), **console/TUI fallback test** (§1) — hygiene.
