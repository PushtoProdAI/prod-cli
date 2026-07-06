# prod — Agent-Native Direction (Design)

**Status:** design only — no code changes implied by this document.
**Audience:** an engineer picking up any one of the three tracks below without a blank page.
**Grounding:** every symbol/path cited was verified against the tree on the `main` branch.

> Thesis (from [ROADMAP.md](../ROADMAP.md) §Thesis): prod is the natural-language deploy layer for
> the AI-native stack — agents, MCP servers, GPU compute, sandboxes. Today the deploy path assumes
> one shape (a `web` service that answers HTTP 200) and two cloud archetypes (direct-API PaaS and
> the new managed-container→HTTPS pattern on App Runner). This plan removes the shape assumption,
> then adds two clouds that make prod compelling to agent builders: **Modal** (serverless + GPU,
> Python-native) and **Google Cloud Run** (the cheap breadth win that reuses App Runner wholesale).

---

> **Status update (this plan is partly shipped; §0 below is now historical).** The
> `deployShape` **foundation landed**: `deployment.DeployShape` + `ParseShape`
> (`internal/deployment/shape.go`), `DeployPlan.Shape`, and shape-aware liveness
> (`Activities.verifyLiveness` in `internal/agent/monitoring.go` skips the HTTP probe for
> worker/cron). The dispatch in §0 was also rebuilt: the per-`workflow_<platform>.go` clones
> and the `Platform` string-switch are **gone** — platforms register in the L1
> `PlatformCatalog` (`internal/agent/platforms.go`) and the managed-container clouds share
> one generic workflow (L2, `internal/deployment/managedcontainer`). **What remains of this
> plan:** the per-shape `LivenessChecker` interface, `SupportedShapes()` validation, the
> MCP-server handshake, and the **Modal** adapter — tracked in
> [fast-follows.md §5](./fast-follows.md).

## 0. Where the code is today (the three seams this plan threads through)

1. **Intent → plan.** `llm.ExtractIntent` returns `types.Intent` (`baml_src/intent.baml`, class
   `Intent { action, platform, source }` → generated `baml_client/types/classes.go`).
   `Workflows.planDeploy` (`internal/agent/planning.go`) string-switches `intent.Action`/
   `intent.Platform` into the `Action`/`Platform` enums (`internal/agent/types.go`) and produces a
   `DeployPlan` (`internal/agent/agent.go:84`).

2. **Plan → adapter.** `deployment.NewDeploymentBuilder(&spec, envVars).Build()`
   (`internal/deployment/deployment.go`) maps `analyzer.ProjectSpec` → `deployment.DeploymentSpec`.
   `Workflows.Deploy` (`internal/agent/workflow.go:162`) dispatches `Platform` → a
   `workflow_<platform>.go`, which calls the `AgentDeploySteps` activity →
   `Activities.createDeployable` (`internal/agent/deployment.go:22`) → a `deployment.Deployable`.

3. **Adapter → liveness.** Every `workflow_<platform>.go` finishes by calling the
   `AgentIsURLLive` activity → `Activities.isURLLive` (`internal/agent/monitoring.go:77`): a single
   `http.Get`, fail if status `> 300`, with `RetryOptions.MaxAttempts = 15` (see
   `workflow_aws.go:103`). **This is the only liveness primitive in the codebase**, and it is
   HTTP-only. Adapters hard-code a `web` service with a `Port` (`apprunner.ServiceConfig.Port`,
   `defaultPort = "8080"` in `aws/apprunner_deploy.go`).

The App Runner reference stack — the newest and cleanest managed-container pattern — is:
`aws/apprunner_deploy.go` (the `Deployable`) → `apprunner/apprunner.go` (the API client `Deployer`)
→ `registry/ecr.go` (the `ecr` `Registry` kind, built from `aws.Config`).

---

## 1. The `deployShape` model  `[foundation — do first]`

### Goal
Add a first-class `deployShape` (`web | mcp-server | worker | cron`) that flows Intent →
DeploymentSpec → adapter, and that **selects the liveness strategy** instead of assuming HTTP.
This is the unlock for deploying agents, which are frequently non-HTTP (loop workers, queue
consumers, scheduled jobs). Without it, a worker agent deploys and then gets **auto-failed by
`isURLLive`** because it has no URL to GET.

### Interfaces / enums / files to add or change

**a) BAML — teach the classifier the shape.** `baml_src/intent.baml`:
```baml
class Intent {
    action string
    platform string
    source string
    deployShape string   // web | mcp-server | worker | cron | unknown
}
```
Add a `## DEPLOY SHAPE` block to the `ExtractIntent` prompt: default `web`; `mcp-server` when the
prompt says "MCP server"; `worker` for "worker/consumer/loop/bot that runs continuously";
`cron` for "every night / on a schedule / cron". Then `make generate` (regenerates
`baml_client/types/classes.go` — never hand-edit). Note: switching *client* is call-time, but a
schema field change **does** require regeneration.

**b) A typed enum, parsed like `Platform`/`Action`.** New file `internal/deployment/shape.go`
(put it in `deployment` so adapters can branch without importing `agent`):
```go
type DeployShape string
const (
    ShapeWeb       DeployShape = "web"
    ShapeMCPServer DeployShape = "mcp-server"
    ShapeWorker    DeployShape = "worker"
    ShapeCron      DeployShape = "cron"
)
func ParseShape(s string) DeployShape { /* lower-case; default ShapeWeb on ""/"unknown" */ }
// HTTPShaped reports shapes whose liveness is a URL probe.
func (s DeployShape) HTTPShaped() bool { return s == ShapeWeb || s == ShapeMCPServer }
```

**c) Carry it end-to-end.**
- `DeploymentSpec` (`internal/deployment/deployment.go`): add `Shape DeployShape`.
- `DeployPlan` (`internal/agent/agent.go:84`): add `Shape deployment.DeployShape`.
- `DeploymentBuilder.Build()` (`deployment.go:173`): thread the shape through (add it to
  `NewDeploymentBuilder`, or set `spec.Shape` at each `db.Build()` call site — there are 7:
  `workflow_{render,flyio,netlify,vercel,heroku,aws}.go` + `workflow_rollback.go`). Simplest:
  give `DeploymentBuilder` a `shape` field and default it to `ShapeWeb`.
- `planDeploy` (`planning.go`): after the `intent.Platform` switch, add
  `plan.Shape = deployment.ParseShape(intent.DeployShape)`.

**d) Inference (two sources, LLM primary + analyzer confirm).**
- **Primary:** the `ExtractIntent` field above (user said it).
- **Secondary/confirm:** extend the analyzer to *detect* a shape signal, so "deploy this" with no
  words still works. Add `Shape DeployShape` (or a `ShapeHint`) to `analyzer.ProjectSpec`
  (`internal/analyzer/analyzer.go:14`); have `node.go`/`python.go` set it from cheap heuristics:
  a `mcp`/`@modelcontextprotocol/sdk` dependency or `FastMCP` import ⇒ `mcp-server`; a
  `celery`/`bullmq`/`while True:` worker entrypoint with **no detected `Routes`** ⇒ `worker`;
  an `apscheduler`/`node-cron`/crontab file ⇒ `cron`. Reconcile in `planDeploy`: explicit user
  shape wins; else analyzer hint; else `web`.

**e) Shape-aware liveness — the core behavioral change.** Replace the single
`AgentIsURLLive` call site with a shape dispatcher. Add `internal/agent/monitoring.go`:
```go
func (a *Activities) verifyLiveness(ctx context.Context, shape deployment.DeployShape,
    url string, res deployment.CreatedResource) error {
    switch shape {
    case deployment.ShapeWeb:
        return a.isURLLive(ctx, url)                 // existing check, unchanged
    case deployment.ShapeMCPServer:
        if err := a.isURLLive(ctx, url); err != nil { return err }
        return a.mcpHandshake(ctx, url)              // new: JSON-RPC `initialize`, expect serverInfo
    case deployment.ShapeWorker, deployment.ShapeCron:
        return a.checkAdapterLiveness(ctx, shape, res) // new: delegate to the adapter (below)
    }
}
```
- `mcpHandshake`: POST a JSON-RPC `initialize` to the MCP endpoint over streamable HTTP; success =
  a well-formed `result` with `serverInfo`/`capabilities`. This is the launch "hosted MCP server"
  shape from ROADMAP Phase 1.
- Non-HTTP shapes can't be probed from the client, so liveness is **platform-owned**. Add an
  optional interface in `deployment` and let adapters implement it:
```go
// LivenessChecker lets a non-HTTP adapter own its own liveness (process RUNNING /
// log-heartbeat for worker, schedule-registered for cron). HTTP shapes never need it.
type LivenessChecker interface {
    CheckLiveness(ctx context.Context, shape DeployShape) error
}
```
  `checkAdapterLiveness` type-asserts the `Deployable` to `LivenessChecker` (mirrors the existing
  `out.(output.InfoBoxWriter)` optional-interface pattern in `deployment.go:109`); if the adapter
  doesn't implement it, worker/cron on that platform is unsupported (see edge cases).

**f) Adapters branch on shape for artifact generation.** Where an adapter hard-codes a web service
+ port, gate it: `if spec.Shape.HTTPShaped() { set Port } else { portless start command only }`.
For App Runner this means **rejecting** worker/cron (App Runner is HTTP-only) — see (g). For a
future portless adapter (Modal, §2) the worker branch generates a long-running function, not a web
endpoint.

**g) Shape × platform compatibility.** Add to the `DeploymentAdapter` interface
(`deployment.go:53`):
```go
SupportedShapes() []DeployShape
```
Validate in `planDeploy` before dispatch: if the chosen platform doesn't support the resolved
shape, fail early with a plain-language message ("Fly.io can't run a scheduled job — try `prod
\"deploy this cron to modal\"`"). Web-only clouds (App Runner, Cloud Run services, the direct-API
PaaS) return `{ShapeWeb, ShapeMCPServer}`. Modal returns all four.

**h) Auto-rollback becomes conditional on shape.** Today a liveness failure fails the deploy (and,
per CLAUDE.md §6, drives auto-rollback). Gate that: only HTTP shapes auto-rollback on an HTTP
liveness failure. For worker/cron, the terminal signal is the adapter's `CheckLiveness` (process
never reached RUNNING / schedule never registered), not an HTTP code. In `workflow_aws.go`-style
workflows, wrap the `liveCheckOpts.RetryOptions.MaxAttempts = 15` block so it only runs for
`shape.HTTPShaped()`; otherwise call `verifyLiveness` once with a longer, non-HTTP timeout.

### End-to-end thread
`"deploy this mcp server to fly"` → `ExtractIntent{deployShape:"mcp-server"}` →
`ParseShape` → `DeployPlan.Shape` → `DeploymentBuilder.Build()` sets `spec.Shape` →
`SupportedShapes()` check passes (Fly returns mcp-server) → adapter generates an HTTP service →
workflow calls `verifyLiveness(ShapeMCPServer, url)` → `isURLLive` **+** `mcpHandshake` → success
output includes the URL **and** a copy-pasteable `mcpServers` block (ROADMAP Phase 1 exit).

### Acceptance criteria
- `prod "deploy this worker to <portless-capable platform>"` deploys and stays green with **no HTTP
  probe** issued (assert via test that `isURLLive` is not called for `ShapeWorker`).
- `prod "deploy this mcp server ..."` fails liveness if the URL is up but the MCP `initialize`
  handshake fails (a plain web app deployed as `mcp-server` is correctly rejected).
- Requesting `cron` on App Runner fails in **planning** with a helpful message, not mid-deploy.
- Default path unchanged: an app with no shape words still resolves to `web` and behaves exactly as
  today (golden test over the existing Fly/Render flows).

### Edge cases
- **Shape/platform mismatch** (worker on App Runner): caught by `SupportedShapes()` in planning.
- **Analyzer says worker, user typed nothing, platform is web-only:** treat as `web` but warn — a
  loop with a bound port is legal; a loop with none isn't. Prefer the explicit route: if no
  `Routes` were detected, don't silently deploy a portless process to a URL-expecting platform.
- **MCP handshake flakiness:** the 15-retry envelope already exists; reuse it for `mcpHandshake`.
- **go-workflows determinism:** `verifyLiveness` does network I/O — it must stay inside an activity
  (`AgentVerifyLiveness`), never in workflow code, same as `AgentIsURLLive` today.
- **Backward compat:** an old `DeployPlan` serialized without `Shape` deserializes to `""` →
  `ParseShape` defaults to `web`. Safe.

### Effort: **M**
BAML field + prompt (S), enum + spec/plan threading (S), `verifyLiveness` + `mcpHandshake` +
`LivenessChecker` + `SupportedShapes` (M), analyzer hints (S). No new cloud. This is the
highest-leverage change because §2 and the ROADMAP Phase-2 worker epic both depend on it.

---

## 2. Modal adapter — the agent-native compute play  `[net-new pattern]`

### Goal
`internal/deployment/modal/` implementing `DeploymentAdapter`/`Deployable`, giving prod a
**serverless + GPU, Python-native** target that matches the agent-builder audience. Modal is where
people already run LLM/agent workloads (GPU inference, long jobs, cron, sandboxes), so this is the
"agent-native compute" wedge. It is also the **first genuinely non-container, non-HTTP** adapter —
it exercises everything §1 builds.

### What's genuinely different from the container-image model
App Runner/Render/Cloud Run all follow: **prod builds a Docker image → pushes to a registry →
tells the cloud to run it → poll → URL.** Modal does **not** fit this:
- **No local Docker build, no registry.** Modal builds its own image server-side from a
  `modal.Image` spec declared *in the user's Python*. So the `DockerGenerator` +
  `registry.Registry` machinery (`ecr.go` etc.) is **not reused** for Modal.
- **Deploy is `modal deploy <app.py>`,** not an API `CreateService`. The unit is a `modal.App`
  containing `@app.function(...)` / `@app.cls(...)` (optionally `@modal.web_endpoint`,
  `@modal.asgi_app`, or `schedule=modal.Cron(...)`).
- **Compute is declared in code** (`gpu="A10G"`, `cpu=`, `memory=`), not in an API request body.
- **Liveness is not a URL** unless the app exposes a web endpoint.

### What still reuses the pattern
- **Auth-from-env, BYO creds** — identical philosophy to every other adapter. Modal auth is
  `MODAL_TOKEN_ID` + `MODAL_TOKEN_SECRET` (or `~/.modal.toml`). Provide `modal.AuthFromEnv()`
  mirroring `registry.FromEnv` / the AWS credential chain; never transmit it anywhere (CLAUDE.md
  §7). No prod account.
- **The adapter interface itself** — `Deployable.Deploy/GetPreviousDeployment/Rollback`,
  `DeploymentAdapter.SupportedStrategies/GenerateArtifacts/EstimateCost`.
- **The CLI-client precedent** — Netlify and Vercel already shell out to a platform CLI
  (`netlify.NewCLINetlifyClient`, `vercel.NewCLIVercelClient` in `deployment.go:138,147`). Modal
  follows that exact pattern: shell out to the `modal` CLI, parse its output. No Go SDK needed for
  v1.

### Interfaces / files to add
```
internal/deployment/modal/
  modal.go          // Deployer: wraps the `modal` CLI (deploy, app list, app rollback)
  modal_deploy.go   // Deployment implements deployment.Deployable + deployment.LivenessChecker
  auth.go           // AuthFromEnv(): MODAL_TOKEN_ID / MODAL_TOKEN_SECRET
  scaffold.go       // (optional) template a modal_app.py wrapper when the project has none
  modal_test.go
```
- `modal.New(cli execRunner)` — inject an `exec.CommandContext` runner (like the AWS SDK-subset
  interfaces in `apprunner.go`, so tests don't shell out).
- `Deployment.Deploy(ctx)`:
  1. Resolve token (`AuthFromEnv`); export into the child env.
  2. Locate the `modal.App` entrypoint. If the project already defines one (BYO), use it. If not,
     and the resolved `Shape` is agent-shaped, `scaffold.go` writes a `modal_app.py` that imports
     the user's entrypoint and wraps it in `@app.function(gpu=..., schedule=...)` per shape.
  3. Run `modal deploy <entrypoint> --name <sanitized>` (reuse `registry.Sanitize`), streaming
     progress through the `StatusWriter` (never `fmt.Print` — CLAUDE.md §6).
  4. Parse the deploy output for the app id and, for web/mcp shapes, the `*.modal.run` URL.
  5. Return `[]CreatedResource{{Type:"modal_app", ID: appID, Metadata:{"url": url}}}`.

### How GPU/compute is expressed in the spec
Add a reusable, platform-neutral compute block to `DeploymentSpec` (also useful to Cloud Run/App
Runner later):
```go
type Compute struct { GPU string; CPU string; Memory string } // GPU e.g. "A10G","T4","H100"
// on DeploymentSpec:
Compute Compute
```
Parse it from the Intent ("with an A10G GPU", "4 cpu") — add a small `ExtractIntent` extension or a
dedicated BAML function. For the **BYO `modal.App`** path prod can't rewrite user decorators, so it
surfaces the requested compute in the plan and warns if the app's declared GPU differs. For the
**scaffold** path, `scaffold.go` templates `gpu="{{.Compute.GPU}}"` into the generated decorator —
this is where prod's value shows: NL → a correct Modal function.

### How liveness works (ties into §1)
`Deployment` implements `deployment.LivenessChecker`:
- `ShapeWeb`/`ShapeMCPServer`: return the `*.modal.run` URL from `Deploy`; the agent's
  `verifyLiveness` does `isURLLive` (+ `mcpHandshake`). No adapter-side check needed.
- `ShapeWorker`: `modal app list --json` (or `modal app show`) → assert the app is `deployed` and
  the target function is registered; optionally tail `modal app logs` for a heartbeat line for N
  seconds. This is the "process-running / log-heartbeat" model from ROADMAP Phase 2.
- `ShapeCron`: assert the deployed function carries a `modal.Cron`/`modal.Period` schedule
  (schedule-registered = live; a cron that never fires isn't "down").

`SupportedShapes()` returns all four — Modal is the reference multi-shape adapter and the reason §1
must land first.

### Wiring (same three switches as every platform)
- `internal/agent/types.go`: add `Modal` to the `Platform` enum **before `UnknownPlatform`**, then
  `go generate` `types_string.go` (the `//go:generate stringer` directive is already there).
- `baml_src/intent.baml`: add `Modal` to the `ExtractIntent` platform list; `make generate`.
- `planning.go`: add `case "modal": platform = Modal` to the string switch.
- `deployment.go` `createDeployable`: add `case Modal:` returning the modal `Deployable`.
- `workflow.go` `Deploy` dispatch + a new `workflow_modal.go` (clone `workflow_aws.go`, but skip the
  `IsDockerAvailable` gate — Modal needs the `modal` CLI, not Docker — and route liveness through
  `verifyLiveness`).
- `detectPlatformsForRollback` platform list (`planning.go:562`) if rollback detection is wanted.

### Acceptance criteria
- With `MODAL_TOKEN_ID/SECRET` set and `modal` on PATH, `prod "deploy this to modal"` on a project
  with a `modal.App` deploys and returns the live `*.modal.run` URL.
- `prod "deploy this GPU agent worker to modal"` (no web endpoint) deploys and is verified live via
  `modal app list`, with **zero** HTTP probes.
- Missing `modal` CLI ⇒ a plain-language error via `SummarizeDeployError` (mirror the
  `IsDockerAvailable` guard), not a raw exec error.

### Edge cases
- **No `modal.App` and shape is `web`:** either scaffold a minimal ASGI wrapper or fail with a clear
  "point me at your `modal.App`" message — don't guess a GPU workload.
- **BYO decorator vs requested compute mismatch:** warn in the plan; never silently override user
  code.
- **Cost estimate:** Modal bills per-second GPU/CPU — `EstimateCost` should return a *rate* + a
  "usage-based, not a fixed monthly" confidence flag (aligns with the ROADMAP cost-estimation
  watch-item; don't fabricate a monthly total).
- **Secrets:** map `spec.EnvVars` with `Sensitive` to `modal secret` (not plain `--env`), analogous
  to App Runner's Secrets-Manager split in `splitEnvVars` (`apprunner_deploy.go:109`).

### Effort: **L**
Net-new deploy mechanism (CLI shell-out + output parsing), scaffold templating, non-HTTP liveness,
compute parsing. Hard-depends on §1. Highest product upside for the agent audience.

---

## 3. Google Cloud Run adapter — the cheap breadth win  `[App Runner twin]`

### Goal
`internal/deployment/gcprun/` — the GCP twin of App Runner: managed container → HTTPS. It reuses
the App Runner + registry-adapter pattern almost entirely; the only new work is Google Artifact
Registry auth, the Cloud Run API, and the ADC credential chain.

### Exactly what transfers from App Runner (≈80%)
| App Runner artifact | Cloud Run twin | Change |
|---|---|---|
| `aws/apprunner_deploy.go` (`Deployment` implements `Deployable`) | `gcprun/gcprun_deploy.go` | rename; same shape: resolve creds → `dockerGen.BuildAndPushToRegistry` → create/redeploy → wait → `CreatedResource{url}` |
| `apprunner/apprunner.go` (`Deployer`, SDK-subset iface, `WaitForRunning` poll loop) | `gcprun/gcprun.go` | swap App Runner SDK for the Cloud Run v2 API; `WaitForRunning` → `WaitForReady` polling the LRO/`Ready` condition |
| `registry/ecr.go` (`ecr` kind, built from `aws.Config`) | `registry/gar.go` (**new `gar` kind**) | same `Registry` interface (`Name/Credentials/Ref`); ensure-repo + short-lived token |
| `splitEnvVars` (plain vs Secrets Manager) | same, → GCP Secret Manager or inline `env` | copy |
| `createDeployable` `case AWS` | `case GoogleCloudRun` | copy |
| `workflow_aws.go` | `workflow_gcprun.go` | copy; keep the `IsDockerAvailable` gate — Cloud Run *does* build a local image |
| `Platform.AWS` enum + dispatch | `Platform.GoogleCloudRun` | add before `UnknownPlatform`, regen stringer |

The `DockerGenerator` (`deployment.NewDockerGenerator`) and the whole build→push→ref path are
identical — Cloud Run pulls an OCI image from a registry exactly like App Runner pulls from ECR.

### What's GCP-specific (the ≈20%)
1. **A new `gar` registry kind** in `internal/registry/` (mirror `ecr.go`):
   - Host: `<region>-docker.pkg.dev` (e.g. `us-central1-docker.pkg.dev`); repository path is
     `<project>/<repo>/<image>`.
   - `Credentials(ctx, project)`: ensure the Artifact Registry repo exists
     (`artifactregistry.CreateRepository`, "already exists" = success, like `ecr.go:77`), then mint
     a **short-lived OAuth2 access token** from ADC and return
     `Credentials{Username:"oauth2accesstoken", Token: <access-token>, URL: host, ...}`. (ECR
     decodes a base64 `user:pass`; GAR uses the literal username `oauth2accesstoken` + the bearer
     token — same `Credentials` struct, different mint.)
   - Constructed from an ADC token source, not `FromEnv` — add `registry.NewGAR(tokenSource, project,
     region)` paralleling `registry.NewECR(cfg, accountID)`.
2. **The `run.googleapis.com` v2 API** (`cloud.google.com/go/run/apiv2`): `CreateService` /
   `UpdateService` with a `RunService` containing one container (image, port, env, resources).
   `UpdateService` (not "start deployment") applies the new image + triggers a revision — same
   reasoning as the App Runner `UpdateService` comment (`apprunner.go:147`): prod pushes a fresh tag
   each deploy. Poll the returned LRO until the service's `Ready` condition is `True`, read
   `Service.Uri`.
3. **ADC credential chain** (`golang.org/x/oauth2/google.FindDefaultCredentials` /
   `google.golang.org/api/option`): honors `GOOGLE_APPLICATION_CREDENTIALS`, `gcloud` user creds,
   and the metadata server — the GCP analogue of the AWS standard chain. Project id from
   `GOOGLE_CLOUD_PROJECT` or the ADC quota project. Add `auth/gcp.go` paralleling
   `auth.NewAWSAuth(...).Config(ctx)`.
4. **Simpler IAM than App Runner.** App Runner needs `EnsureAccessRole` (an IAM role App Runner
   assumes to pull from ECR — `apprunner.go:92`). Cloud Run pulls from GAR in the *same project*
   using its runtime service account by default, so **there's no access-role dance** for the common
   case — one fewer step than App Runner.

### End-to-end thread
Identical to `workflow_aws.go`: build spec → `AgentDeploySteps` (build+push to GAR → `CreateService`
→ `WaitForReady` → `Service.Uri`) → `verifyLiveness(ShapeWeb, url)`. Cloud Run *can* host the
`mcp-server` shape too (it's HTTPS), so `SupportedShapes()` returns `{ShapeWeb, ShapeMCPServer}`.
(Non-HTTP worker/cron on GCP would be Cloud Run **Jobs** + Cloud Scheduler — a later, separate
deployable; the v1 here is the Services twin only.)

### Acceptance criteria
- With ADC configured (`gcloud auth application-default login`) and a project set, `prod "deploy
  this to google cloud run"` builds locally, pushes to GAR, creates the service, and returns the
  live `run.app` URL.
- Re-deploy updates the existing service to a new revision (no duplicate service), verified by name
  lookup like App Runner's `findService` (`apprunner.go:207`).
- `gar` registry kind passes the same repo-name validation tests as `ecr` (`registry` package
  `projectNameRe`/`tagRe`).

### Edge cases
- **Region/repo defaults:** pick a default region (e.g. `us-central1`) and a default AR repo name;
  make both overridable via env (`PROD_GCP_REGION`, `PROD_GCP_AR_REPO`) following the
  flags→env→config→default precedence (CLAUDE.md §5).
- **Artifact Registry API / repo not enabled:** detect the `PERMISSION_DENIED` / `API not enabled`
  error and surface an actionable message (enable `artifactregistry.googleapis.com` /
  `run.googleapis.com`).
- **Token expiry** on slow builds: mint the GAR token *after* the local build, right before push
  (short-lived), same ordering as `ecr.go`.
- **Rollback:** Cloud Run keeps revisions — real rollback is "route 100% traffic to revision N",
  strictly easier than App Runner (whose `Rollback` is still a stub, `apprunner_deploy.go:102`).
  Ship rollback here even though App Runner hasn't.

### Effort: **S–M**
Mostly a port. `gar.go` (S, mirrors `ecr.go`), Cloud Run client + poll (M, mirrors `apprunner.go`),
ADC auth (S), the 3-switch wiring (S). Cheapest breadth-per-line in this plan.

---

## 4. Prioritized roadmap

1. **`deployShape` model first (§1, M).** It is a dependency of the agent story and of Modal, and
   it fixes a live correctness bug: non-HTTP deploys are auto-failed by `isURLLive`. It also
   delivers the ROADMAP Phase-1 launch shapes (`web` + `mcp-server`) that need no new cloud. Highest
   leverage, lowest cloud risk.

2. **Google Cloud Run (§3, S–M) next.** It's a near-mechanical port of the App Runner reference and
   only needs the web/mcp shapes §1 already delivers — so it ships breadth cheaply while §1 is still
   warm, and it proves the `Registry` interface generalizes past ECR (validating the pattern for
   future clouds). Low risk, fast win, no dependency on Modal.

3. **Modal (§2, L) last of the three, but the strategic payload.** It's the agent-native compute
   story (GPU, serverless, Python-native, cron/worker) and the reason the thesis says "the deploy
   layer for the AI-native stack." It depends on §1's non-HTTP liveness and is the most net-new
   work, so it goes after the foundation is proven and after a low-risk port (§3) has exercised the
   "add a platform" path end-to-end.

Rationale in one line: **build the shape foundation, cash the cheap port to prove it, then spend the
big net-new effort on the differentiated Modal target.**

---

## 5. Where agent-sandbox platforms (E2B, Daytona) fit

**They don't fit `DeploymentAdapter` — and shouldn't be forced to.** E2B and Daytona *provision an
ephemeral sandbox* (spin up a microVM/container, exec code in it, expose a port, tear it down after
a TTL). The `Deployable` contract assumes a **persistent, addressable service** you deploy once and
keep live: `Deploy → CreatedResource{url}`, `GetPreviousDeployment`, `Rollback`, and a liveness
model built on "is the URL up / is the process running." A sandbox has no stable release to roll
back to, is expected to be torn down, and is interactive (create → exec → maybe expose → destroy).
Its lifecycle verbs are `Create/Exec/Expose/Destroy(+TTL)`, not `Deploy/Rollback`.

**Recommendation:** model sandboxes as a **sibling primitive**, not a `deployShape` and not a
`DeploymentAdapter`. A future `internal/sandbox/` package with its own small interface:
```go
type Sandbox interface {
    Create(ctx context.Context, spec SandboxSpec) (SandboxHandle, error)
    Exec(ctx context.Context, h SandboxHandle, cmd []string) (ExecResult, error)
    Expose(ctx context.Context, h SandboxHandle, port int) (string, error) // ephemeral URL
    Destroy(ctx context.Context, h SandboxHandle) error
}
```
This maps cleanly onto E2B and Daytona (and Vercel Sandbox / Cloudflare, later) and onto a distinct
NL verb — "*run* this in a sandbox" / "*give an agent* a sandbox" — versus "*deploy* this." It's a
separate MCP tool (`sandbox.create`/`exec`) rather than another `deploy` target. Keep it out of the
`DeploymentAdapter` hierarchy so the deploy path's rollback/liveness/cost invariants stay honest.
Trying to shoehorn a TTL'd sandbox into `Deployable` would weaken every guarantee that makes the
deploy path safe for autonomous agents (CLAUDE.md §7). Sandboxes are on-thesis for the AI-native
stack, but they're a **new abstraction**, scoped after the three tracks above.
