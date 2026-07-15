# CLAUDE.md

Guidance for Claude Code (and human contributors) working in this repository.

> **prod** lets developers — and AI agents — deploy applications and agents to the cloud
> using natural language. You describe intent in English; prod parses it, plans the
> deployment, and executes a durable, multi-step deploy against the target platform.

**The OSS product is a single self-contained Go binary. No backend, no database, no account.**
It runs entirely on your machine, keeps state locally, and talks directly to each platform —
like `terraform`, `pulumi`, or `flyctl`. See [ROADMAP.md](./ROADMAP.md) for the plan and the
rationale behind collapsing the old SaaS backend out of the core.

> **Current state vs. target.** The code today still routes through a Supabase backend for auth,
> LLM, and deploy logging. ROADMAP Phase 0 severs that: local state, direct LLM, no auth
> gate. Where this file describes the single-binary target and the code differs, the code is the
> thing being changed — follow the target and check the ROADMAP.

---

## 1. What prod is

A **single Go binary** (`prod`, built from `cli/`). It:

1. Parses natural language into a typed intent (BAML).
2. Analyzes the project on disk (language, routes, env, migrations).
3. Plans the deploy, shows it for approval.
4. Executes a **durable workflow** (resumable across retries) against the platform, using the
   **user's own credentials**, and records history to a **local store**.

Deploy targets (all in-binary, user's own creds): **Fly.io, Render, Vercel, Netlify, Heroku,
Cloudflare Pages** (direct-API; Cloudflare Pages is static-only, via the direct-upload REST API)
and **AWS App Runner, Google Cloud Run, Azure Container Apps** (managed container — build locally →
push to a registry in your account → create the service). Plus **Modal** (experimental, via the
`modal` CLI) and **out-of-tree provider plugins** (`prod plugin install`). Deploy, rollback, and
**destroy** are all natural-language actions (destroy can cascade backing DBs with `--delete-data`).

**No server is required to deploy.** LLM calls go direct to OpenAI/Anthropic/Ollama with the
user's key; history lives in a local JSON file; platform tokens live in `~/.prod`. A hosted backend
exists only as the optional **commercial** tier (teams, shared history, metering) and lives in a
**separate repo** — it is not part of `prod-cli`.

### The word "agent" — two meanings, don't conflate them
- **prod's orchestrator** (`cli/internal/agent/`) — the internal state machine that drives a
  deploy. Most "agent" references in the code mean this.
- **AI agents as a deploy target** — deploying autonomous LLM apps (web-shaped agents, MCP
  servers, later workers/cron). A first-class product goal (ROADMAP Phase 1–2), partly net-new.

Always be explicit about which one you mean.

---

## 2. Architecture at a glance

```
You: "deploy this to fly with a postgres"
      │
      ▼
┌──────────────────────── the prod binary (Go) ─────────────────────────┐
│  cmd/ (cobra via ecdysis)                                             │
│    → internal/agent    FSM orchestrator + durable workflows          │
│        checkPrerequisites → plan → confirm → detectExisting          │
│        → categorizeEnvVars → prepareProject → deploy                  │
│    → internal/llm      BAML: English → typed Intent (direct provider) │
│    → internal/analyzer detect language, routes, env, migrations       │
│    → internal/deployment/<platform>   adapter pattern, user's creds   │
│    → internal/output   Console | JSON | TUI writers                   │
│    → local state       ~/.prod/history.json (deploy history)          │
└───────────────────────────────────────────────────────────────────────┘
      │ direct API calls with the user's own tokens/creds
      ▼
   Fly · Render · Vercel · Netlify · Heroku · AWS · Cloud Run · Azure · Modal (your account)
```

Deploys run as **durable workflows** (`go-workflows`) so slow, failure-prone cloud provisioning
can retry and resume. Deploy **history** is a local JSON file (`~/.prod/history.json`); the
go-workflows durable backend is dormant today (in-memory), so workflow state is in-process. No
server either way.

> `go-workflows` is now a plain upstream dependency (`github.com/cschleiden/go-workflows`, tagged
> release, no `replace`) — the old personal-fork/supply-chain risk is gone. It's still the biggest
> single piece of complexity; keep the dependency shallow.

---

## 3. Repository layout (`prod-cli`)

The public repo is **the binary only**. Backend/`infra`/`lambda` live in the separate commercial repo.

```
cli/
  cmd/                   Entry points; cobra commands (via ecdysis)
    main.go              Dependency wiring / bootstrap
    root/                `prod [prompt]` — TUI or one-shot
    run/                 `prod run <prompt>` — automation / JSON mode / MCP substrate
    deploys/             `prod ls` / `open` / `logs` — the deploy launcher over local history
    doctor/ plugin/ mcp/ `prod doctor` / `prod plugin ...` / `prod mcp`
  internal/
    agent/               Orchestrator FSM, per-platform workflows, detectors
    analyzer/            Static project analysis (node.go, python.go, go.go — Node/Python/Go)
    deployment/          Platform adapters: flyio/ render/ vercel/ netlify/ heroku/ cloudflare/ aws(apprunner)/
                         gcprun/ aca(azure)/ modal/ + managedcontainer/ (shared container base)
    deploytarget/        Resolve a history record → {LiveURL, ConsoleURL, LogsCmd, CanRollback}
                         (per-cloud knowledge; used by the MCP tools + prod ls/open/logs)
    mcpserver/           The `prod mcp` server + its 9 tools (deploy/rollback/destroy/status/…)
    pluginhost/          Out-of-tree provider-plugin host (go-plugin over net/rpc)
    llm/                 BAML client wrapper (Client interface, mock, ClientRegistry selection)
    output/              StatusWriter: Console | JSON | Tea | Proxy writers
    history/             Local deploy history (~/.prod/history.json) + the deploytarget records
    registry/            Container-registry adapters (ECR / GAR / ACR / generic)
    backend/             HTTP client — used only in managed mode (optional)
    config/              Config resolution (flags → env → file → default)
    tui/                 Bubble Tea v2 UI
    workflowext/         go-workflows wiring
    tokens/ cache/ error/ log/ scratchpad/
  baml_src/              BAML sources (intent.baml, clients.baml, pricing.baml)
  baml_client/           GENERATED Go from BAML — never edit by hand
  Makefile               Build/dev/generate targets
docs/design.md           LEGACY deep reference (describes the SaaS backend being retired)
```

`docs/design.md` still documents the old backend-centric AWS flow and DB schema — useful for the
AWS port, but read it knowing the backend is leaving the OSS core.

---

## 4. Build, run, develop

From `cli/`:

```bash
make build            # build ./prod for host arch
make dev              # go run
make generate         # regenerate baml_client/ from baml_src/ (after editing *.baml)
make build-all        # cross-compile all targets
go test ./...         # unit tests
```

**CGO is enabled** (`CGO_ENABLED=1`) because BAML has a native dependency. This is why the current
`.goreleaser.yml` is darwin-only — Linux/Windows cross-compile is broken and is fixed in Phase 0
with native per-OS CI runners. A contributor on Linux hits this on `make build`; document it in
`CONTRIBUTING.md`.

---

## 5. Run modes & configuration

| Mode | Account? | State | LLM | Notes |
|------|----------|-------|-----|-------|
| **local** (default) | none | local JSON in `~/.prod` | direct: OpenAI/Anthropic/**Ollama** | the product; no backend |
| **managed** (opt-in) | yes | synced to hosted backend | proxied + metered | commercial tier; teams/SSO/history |

Configuration precedence: **flags → env → config file → built-in default**. Nothing
environment-specific is hard-coded — no Supabase/AWS/S3 identifiers in source.

**LLM routing:** BAML functions pin `client "ProxyClient"` in source, but selection is
**overridable at call time** via `WithClientRegistry` (`baml_client/runtime.go`) — so local mode
uses the direct clients in `baml_src/clients.baml` (`CustomGPT4o`, `CustomSonnet`, `OllamaClient`)
**without editing `.baml` or regenerating**. Building that `ClientRegistry` in `llm.getCallOptions`
(and plumbing `OPENAI_API_KEY`/`ANTHROPIC_API_KEY`) is the Phase-0 direct-LLM task; today
`getCallOptions` only injects proxy env. Keep both paths working; select at runtime, never by
editing generated code.

**Credentials are the platform's, held locally** — a Fly token, AWS creds from the standard chain
(`~/.aws`, env, SSO), etc. There is no prod-account login in local mode.

---

## 6. Extension points (what you'll most often touch)

### Add a deployment platform
Implement the adapter interfaces from `internal/deployment/deployment.go`:

```go
type DeploymentAdapter interface {
    SupportedStrategies() []DeploymentStrategy
    GenerateArtifacts(spec *DeploymentSpec, strategy DeploymentStrategy) (Deployable, error)
    EstimateCost(spec *DeploymentSpec, strategy DeploymentStrategy) (CostEstimate, error)
}
type Deployable interface {
    Deploy(ctx context.Context) ([]CreatedResource, error)
    GetPreviousDeployment(ctx context.Context) (*DeploymentInfo, error)
    Rollback(ctx context.Context, targetDeploymentID string) error
}
```

Copy an existing package (`internal/deployment/flyio/` is the cleanest reference), add the
platform to the enum in `internal/agent/types.go`, wire dispatch in `internal/agent/workflow.go`,
add `workflow_<platform>.go`. Deploys use the **user's own credentials** — no central account.
Every platform must support rollback (native API where available; image-swap — redeploy the
previous recorded image — for Fly.io and AWS App Runner, which have no native rollback API).
Modal is the exception: it deploys from source, so rollback means redeploying a prior version.

### Add / change an LLM behavior
Edit `baml_src/*.baml`, then `make generate`. Never hand-edit `baml_client/`. The Go wrapper is
`llm.Client` (`internal/llm/client.go`); add a mock in `mock.go`. Switching *client* is call-time
(`WithClientRegistry`) and needs no regen.

### Add a language/framework detector
Implement `analyzer.Analyzer` (`CanHandle`/`Analyze`) in `internal/analyzer/`, plus framework
heuristics in `internal/agent/`. Node, Python, and Go today (`go.go`); more languages and
agent-framework detectors (LangGraph, CrewAI, Agents SDK, Mastra) are ROADMAP goals and go here.

### Output modes
Everything user-visible goes through a `StatusWriter` (`internal/output/`). Do **not** call
`fmt.Println` in agent/deployment code — emit via the writer so it renders in TUI, console, and
JSON (`PROD_JSON_MODE`) modes. The JSON event protocol is the MCP substrate; keep events
structured and stable.

> The JSON event contract is **versioned and frozen** (W0). JSON events are built from one typed
> event family carrying `{type, event_version, timestamp}` (`output/writer.go`); the wire contract is
> documented in [docs/protocol.md](./docs/protocol.md) and pinned by goldens
> (`output/testdata/events.golden.jsonl`, `mcpserver/testdata/tool_schemas.golden.json`) — change a
> field and CI fails until you `-update` the golden (bumping `EventVersion`/`ContractVersion` only if
> the change is breaking). Cross-writer no-panic parity is guarded for every writer including
> `TeaWriter` (`output/writer_parity_test.go`, `tui/teawriter_test.go`); the old console-mode panic is
> fixed and regression-tested (`agent/console_fallback_test.go`). When you add an event, add it to
> **all** writers from one event object and update the golden.

### Deploy shapes (shipped, with gaps)
The `deployShape` field (`web` | `mcp-server` | `worker` | `cron`) **exists** on
`Intent`/`DeploymentSpec` (`deployment/shape.go`) and selects the liveness strategy: `verifyLiveness`
(`agent/monitoring.go`) is shape-conditional — worker/cron skip the HTTP probe (no false rollback),
mcp-server requires a JSON-RPC `initialize` handshake, web GETs the URL (live = any response <500,
so auth walls count as live). Fly and Render deploy worker/cron end-to-end; agent-framework
detection (langgraph/crewai/…) lives in `analyzer/shape.go`. **Remaining gaps** (W5 in
[docs/agentic-ownership-roadmap.md](./docs/agentic-ownership-roadmap.md)): `PlatformSpec.Shapes` is
empty for built-ins so a worker can be mis-targeted at AWS/GCP/Azure and fail late; Go lacks
agent-shape detection; the scaffolded `prod.yaml` shape is never read at deploy. Keep health-check
auto-rollback **conditional on shape**.

---

## 7. Security

A local tool that wields the user's cloud credentials and builds cloud infrastructure. Treat these
as security-sensitive.

- **Credentials stay local and are the user's own.** Read from standard sources (`~/.aws`, env,
  platform config); store prod's own files in `~/.prod` at `0600`/dir `0700`. Never transmit user
  cloud creds anywhere. (This is the local-tool model — like Terraform — and it's *why* the SaaS
  central-account/STS indirection is unnecessary in the OSS core.)
- **CloudFormation input validation** (AWS port): user data flows into templates — validate service
  names, image URLs, env var names; block shell metacharacters in migration commands (see the
  existing `deploy-aws-stack/template-generator.ts` logic being ported). Embed templates via
  `go:embed` so user data flows only through parameters, never code.
- **Secrets:** never commit. History was swept before publishing — see
  [docs/security-sweep.md](./docs/security-sweep.md). Live secrets found in history: a **Render API
  key** and **two Sentry DSNs** (rotate before public). The real Supabase anon/service-role keys are
  **not** in git (they were `.env`-only, build-injected). A `gitleaks` pre-commit hook blocks new
  ones (`make install-hooks`).
- **MCP / agent surface:** `deploy`/`rollback` are destructive and cost money. Require explicit
  human approval by default; an agent must pass an explicit `confirm`/`--yes` to skip it.

**Commercial backend only** (separate repo, not `prod-cli`): the multi-tenant Postgres uses RLS +
`SECURITY DEFINER` RPCs. If you work there, remember **RLS does not protect `SECURITY DEFINER`
RPCs** — they run as owner and are directly callable via PostgREST, so scope *inside* the function
(`auth.uid()`, gate cross-user/`NULL` behind `is_admin_user()`); see the IDOR-fix migration
`20260704000000_scope_deployment_query_functions.sql` as the canonical pattern.

---

## 8. Testing & quality

```bash
cd cli && go test ./...
```

- Use the `llm.Client` mock (`internal/llm/mock.go`) — never hit a real LLM in tests.
- Adapter integration tests: `testcontainers-go` where a real dependency is needed; keep them
  hermetic and skippable without cloud credentials.
- Run `golangci-lint run` and `govulncheck ./...` before pushing.
- Table-driven tests; wrap errors with `github.com/go-errors/errors` for stack traces.

---

## 9. Conventions & house style

- **Dependency injection** via constructors (`New...`), not globals. `cmd/main.go` is the
  composition root.
- **Small, focused interfaces** (`Deployable`, `AuthProvider`, `StatusWriter`, `analyzer.Analyzer`).
- **Errors:** wrap with stack traces; surface user-facing errors via `SummarizeDeployError` (BAML)
  so they're plain-language and OS-aware. No naked `panic`.
- **Comments explain *why*, not *what*.** Match the surrounding file's density.
- **Naming for humans:** user-facing strings describe outcomes ("Deploying to Fly.io…",
  "Deployed — https://…"), not internals.
- **Simplicity is the design value.** Fewer moving pieces beats clever. Justify any new dependency,
  service, or language; default to collapsing components, not adding them.
- **Generated code** (`baml_client/`) is never edited or reviewed line-by-line.

---

## 10. Common gotchas

- Editing `baml_src` without `make generate` → stale behavior. (Switching *client* is call-time via
  `WithClientRegistry` — that does **not** need regen.)
- Calling `fmt.Print*` in orchestration code → breaks JSON/TUI modes. Use the writer.
- Adding an event to only one writer → cross-writer drift. Implement it in all writers from one
  event object (the old console-mode panic is fixed and guarded by tests; don't reintroduce it).
- Assuming a backend/account exists → wrong for local mode. The deploy path must work with only
  local state + the user's platform creds.
- Assuming HTTP liveness for every deploy → breaks worker/non-HTTP shapes. Check `deployShape`.
- Reaching for a server to solve a single-user problem → almost always unnecessary; use local state.

---

_Keep this file current. If you change an extension point, the run-mode model, or a security
pattern, update the relevant section in the same PR._
