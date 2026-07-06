# prod — Cloud-Adapter Framework Plan

**Status:** design. Ladder of three levels for making prod extensible with new
clouds, from an internal registration table to out-of-tree provider plugins.
**Motivation, measured:** wiring Google Cloud Run required editing **~10 separate
`switch platform` sites**, and a review caught a **blocking miss** (`getProjectDetector`)
that would have failed *every* Cloud Run deploy — silently, because no build/unit
test exercises runtime dispatch. Adding a cloud is O(scattered switches), not
O(one adapter). This plan fixes that, then opens the door to third-party clouds.

The three interfaces are already good — `deployment.Deployable`,
`deployment.DeploymentAdapter`, `registry.Registry`. The problem is purely the
**dispatch wiring** around them.

---

## Review corrections (authoritative — supersede anything below they conflict with)

A principal-engineer review verified this plan against the code. Verdict: sound;
build with these seven changes:

1. **The catalog lives in `internal/agent`, and the "factory interface in
   `deployment`" fallback is dropped.** Factual correction that makes it cleaner:
   `registry.Registry` is in `internal/registry` (image registries), *not*
   `deployment`, and there is **no `deployment→output` cycle** (`output` is a leaf).
   `PlatformSpec`'s factories name `*agent.Activities`/`agent.ProjectDetector`/
   `agent.Platform`, all agent-internal, and `agent` already imports `deployment`/
   `auth`/`registry` — so the catalog in `agent` closes over everything with zero new
   edges. Putting it in `deployment` would *force* `deployment→agent` (the real cycle).
2. **Rename off "registry."** "Registry" already means `internal/registry` (image
   registries) and `workflowext.Registry` (the go-workflows registerer). Call the new
   concept **`PlatformCatalog`** (`internal/agent/platforms.go`).
3. **The generic workflow needs primary-resource resolution.** The container workflows
   hard-code the resource-type string to find the URL (`cr.Type=="apprunner_service"`
   / `"cloudrun_service"`) — that's a per-platform detail living *workflow-side*, so
   it doesn't fully "live inside the Deployable." Add `Primary bool` to
   `deployment.CreatedResource` (cleaner than a `PrimaryResourceType` on the spec) and
   have the generic `deployWorkflow` take the primary resource's `Metadata["url"]`.
   Without this a switch survives inside the "collapsed" workflow.
4. **Split the one flag into two.** `IsContainerBased` is overloaded: the JS-framework
   switches group `{Render, FlyIO, Heroku, AWS, GoogleCloudRun}` = "runs a persistent
   Node server / needs adapter-node" (**includes Heroku**), but the Level-2 base is
   `{App Runner, Cloud Run, Azure}` = managed container (**excludes Heroku**, which is
   buildpack/git). Use `ServerRuntime bool` (JS grouping) and a separate
   `ManagedContainer bool` (L2). Netlify/Vercel stay explicit `case` arms (unique
   adapter logic). `DomainSuffix` alone *is* enough for Django (both host + CSRF derive
   from it).
5. **AC2, stated precisely.** Single-registration is what eliminates the
   getProjectDetector-class bug (one entry, not seven switches; `NewDetector` is
   nil-legal → `noopProjectDetector`), so the table test catches nil *required*
   factories, not detectors. Add a **per-framework-family table test** (assert every
   `ServerRuntime` platform gets a non-empty result from each JS handler) — the *only*
   thing that catches switch drift like the live bug below. This works only once §4's
   flag drives the shared branch.
6. **Enum is append-only / never renumber** — durable go-workflows state persists
   `DeployPlan.Platform` as an int (history is string-keyed and safe). Make it a rule
   or pin explicit iota values. Append Azure after `UnknownPlatform` (ugly but safe) or
   keep before-Unknown with the append-only rule understood.
7. **Menu reads `spec.Name`, not `Platform.String()`** (`agent.go:508`) — fixes the
   "GoogleCloudRun" vs "Google Cloud Run" label. `parseDeployPlatform` +
   `planning.go` aliases fold into `PlatformByString`.

**Live bug the review surfaced (fixed alongside this plan):**
`javascript_frameworks.go:717` `TanStackStartHandler` omits `GoogleCloudRun` from its
guard, so TanStack Start on Cloud Run returns no config — while the very next switch
includes it. Patched now; §4's flag conversion prevents the whole class.

**Sequencing tweak (optional but recommended):** do a *light* Level 2 extraction from
the two container clouds you have (AWS + Cloud Run) **before** Azure, so Azure lands
*on* the base instead of being written bespoke then refactored. So: **L1 → light L2 →
Azure-on-base → L3.**

---

## 0. The exact touchpoints today (the thing to collapse)

Adding one cloud (verified against the Cloud Run PR) touches:

| # | Site | What it does | Miss = |
|---|---|---|---|
| 1 | `Platform` enum + stringer | typed identity | won't compile |
| 2 | `createDeployable` | Platform → Deployable | "unsupported platform" |
| 3 | `getAuthProvider` | Platform → AuthProvider | auth check fails |
| 4 | `getProjectDetector` | Platform → existing-project detector | **every deploy dies at detectExisting** |
| 5 | `workflow.go` dispatch + register + `workflow_<p>.go` | run the deploy workflow | no workflow |
| 6 | `planning.go` string switch + `parseDeployPlatform` | NL/menu string → Platform | platform not recognized |
| 7 | `deployPlatforms` menu | interactive picker | absent from the menu |
| 8 | JS-framework switches (~9 `case …, AWS:`) | per-framework artifact prep | framework projects error |
| 9 | `framework_django.go` host/CSRF | `ALLOWED_HOSTS`/CSRF domains | Django runtime rejects requests |
| 10 | `unsupportedLocalPlatform` | friendly rollback-unsupported gate | rollback fails mid-flow |
| 11 | BAML `ExtractIntent` platform list (+ `make generate`) | teach the LLM the platform | LLM never picks it |

Levels 4/8/9/10 are the dangerous ones: they compile fine and are only hit at
runtime for specific paths, so `make check` is green while deploys break.

---

## 1. Level 1 — a registration table + a completeness test  `[do first]`

### Goal
Adding a cloud = **one adapter package + one `Register(...)` call**. Every switch
above *derives from* a registry instead of being hand-edited, and a table-driven
completeness test fails loudly if any path can't service a registered platform.

### Design
A `PlatformSpec` registered once per adapter (in the adapter package's file or a
central `platforms.go`), keyed by the existing `Platform` enum (keep the enum for
type-safety and serialization stability; the registry is a lookup *keyed by it*):

```go
// internal/agent (or internal/deployment) — the registry.
type PlatformSpec struct {
    Platform        Platform                        // the enum key (still the wire/serialized identity)
    Name            string                          // canonical display name, e.g. "Google Cloud Run"
    Aliases         []string                        // lowercase NL/menu matches: "cloud run","gcp",…
    NewDeployable   func(*deployment.DeploymentSpec, *Activities) (deployment.Deployable, error)
    NewAuthProvider func(io.Writer) auth.AuthProvider
    NewDetector     func(*Activities) ProjectDetector // default: noopProjectDetector (idempotent deploys)
    WorkflowName    string                          // registered go-workflow name
    IsContainerBased bool                           // true → groups with the Docker clouds in framework switches
    DomainSuffix    string                          // e.g. ".run.app" — for Django ALLOWED_HOSTS/CSRF (empty = none)
    SupportsRollback bool                           // false → friendly "rollback unsupported yet" gate
}

func RegisterPlatform(s PlatformSpec)          // called once per adapter
func LookupPlatform(p Platform) (PlatformSpec, bool)
func PlatformByString(s string) (Platform, bool) // matches Name/Aliases, lowercased
func RegisteredPlatforms() []PlatformSpec        // menu, iteration, tests
```

Rewire each site to read the registry:
- **2/3/4** `createDeployable`/`getAuthProvider`/`getProjectDetector` → `LookupPlatform(p).NewDeployable(...)` etc. (nil `NewDetector` ⇒ the shared `noopProjectDetector`).
- **6** `planning.go`/`parseDeployPlatform` → `PlatformByString(s)`.
- **7** `deployPlatforms` → `RegisteredPlatforms()` (ordered).
- **8** the framework switches change from `case Render,FlyIO,Heroku,AWS:` to a guard on `LookupPlatform(p).IsContainerBased` — so a container cloud is grouped by a *flag*, not by re-listing it in nine places.
- **9** Django host/CSRF read `DomainSuffix` from the spec.
- **10** `unsupportedLocalPlatform` reads `!SupportsRollback`.
- **5 — the workflow.** The seven `workflow_<p>.go` are near-identical (build spec → `AgentDeploySteps` activity → find `CreatedResource` → shape-aware liveness → rollback-on-failure); the platform specifics already live *inside* `createDeployable`'s `Deployable`. Replace them with **one generic `deployWorkflow(ctx, input)`** that dispatches by the registry. Register it once. (The PaaS adapters that today have bespoke steps keep their own workflow via an optional `WorkflowName` override until they're migrated — incremental, not big-bang.)
- **1/11** the enum + the BAML list stay hand-maintained (the enum is the wire identity; BAML is a generated prompt), but a **test asserts the registry, the enum, and the BAML platform list agree** (§ below).

### Acceptance criteria
- **AC1 — one-touch add.** A new container cloud is added by: an adapter package implementing `Deployable` + a `RegisterPlatform` call + a BAML list line + an enum constant. No edits to `createDeployable`, `getAuthProvider`, `getProjectDetector`, the workflow dispatch, `parseDeployPlatform`, the menu, the framework switches, or Django. (Enforced by review + the diff being ~1 file.)
- **AC2 — completeness test (the anti-`getProjectDetector`-bug guard).** A table-driven test iterates `RegisteredPlatforms()` and asserts each resolves a non-nil Deployable-factory, AuthProvider, detector, and workflow name, and round-trips `Name`/each alias through `PlatformByString`. This test *fails today* would have caught the Cloud Run detector miss.
- **AC3 — registry/enum/BAML agreement test.** A test asserts every `RegisteredPlatforms()` name+aliases appears in the embedded BAML `ExtractIntent` prompt, and every non-Unknown enum value is registered. Drift fails CI.
- **AC4 — no behavior change for existing clouds.** Golden test: the existing Fly/Render/AWS/GCR flows produce identical dispatch (same Deployable type, same workflow name) before/after the refactor.
- **AC5 — determinism.** `RegisteredPlatforms()` returns a stable order (registration order or sorted), so go-workflows replay and the menu are deterministic.

### Edge cases & mitigation
- **go-workflows determinism / registration timing.** Register in a single, ordered `init()`-free `registerPlatforms()` called at startup (not scattered `init()`s, which have undefined cross-package order). Mitigation: one explicit registration function; a test asserts order stability.
- **Serialized `Platform` int.** Keep the enum as the serialized identity (unchanged wire format); the registry is an in-memory lookup only. No migration risk.
- **The generic workflow vs bespoke PaaS steps.** Not every adapter's workflow is a pure clone. Mitigation: `WorkflowName` override lets an adapter keep a custom workflow; migrate them opportunistically. The *container* clouds (App Runner/GCR/ACA) share the generic one immediately — which is where the pain is.
- **Framework switches that genuinely differ per platform** (not just container-vs-not). Mitigation: `IsContainerBased` covers the Docker grouping; anything finer stays an explicit switch but is now the exception, and AC2-style tests can be extended per framework family.
- **Registry must live where both `agent` and `deployment` can reach it** without an import cycle (`agent` imports `deployment`, not vice-versa; `PlatformSpec.NewDeployable` references `*Activities` which is in `agent`). Mitigation: put the registry in `internal/agent` (it already depends on everything), or define `PlatformSpec` with a narrow factory interface in `deployment` and register concrete factories from `agent`. Resolve during design; the cycle is the one real structural risk.

### Migration strategy — yes, the existing clouds MUST move onto the registry (that's what L1 *is*)
A registry that only new clouds use while AWS/Cloud Run stay hardcoded in the
switches is **two dispatch systems** — worse than one. So L1 includes migrating the
existing platforms; the value only materializes when the switches read the registry
for *everything*. Do it incrementally, safe at each step:

1. **Land the registry + the switch rewiring behind a total migration of all 7
   platforms' *lookup* factories** (Deployable, AuthProvider, Detector, string match,
   menu, framework flags, Django suffix, rollback flag). This is mechanical and
   golden-test-guarded (AC4) — same dispatch results, sourced from the registry.
2. **Migrate the container clouds' *workflows* first (AWS + Cloud Run → the generic
   `deployWorkflow`).** They're near-identical clones; this deletes `workflow_aws.go`
   + `workflow_gcprun.go` and is the biggest single win.
3. **PaaS clouds (Fly/Render/Vercel/Netlify/Heroku) register their factories but keep
   their bespoke workflows via the `WorkflowName` override**, migrating to the generic
   workflow opportunistically (only if/when their steps prove genuinely generic).

Net: after L1, AWS and Cloud Run are fully registry-driven (dispatch + workflow); the
PaaS clouds are registry-driven for dispatch and keep custom workflows until it's
worth unifying them. No "two systems" end-state — the registry is the single source of
truth for *which* platforms exist and how they dispatch.

### Effort: **M–L** (mechanical rewiring of ~10 sites + migrating all 7 platforms onto the registry + the generic container workflow + 3 tests). Pays for itself at cloud #4 (Azure) and every cloud after.

---

## 2. Level 2 — a shared managed-container base  `[as container clouds accumulate]`

### Goal
App Runner, Cloud Run, and Azure Container Apps are the *same shape*: build image →
push to a registry → create/update a managed service → poll ready → URL, with a
cloud-specific IAM/public-access step. Extract that flow so a new container cloud is
its API calls only (~100 lines), not a full ~250-line Deployable.

### Design
```go
// internal/deployment/managedcontainer
type Service interface {
    Registry(ctx context.Context) (registry.Registry, error)     // ecr / gar / acr
    CreateOrUpdate(ctx context.Context, imageRef string, cfg ServiceConfig) (id string, err error)
    WaitReady(ctx context.Context, id string) (url string, err error)
    MakePublic(ctx context.Context, id string) error             // optional hook (Cloud Run IAM; App Runner no-op)
    Rollback(ctx context.Context, id, target string) error       // optional
}
func Deploy(ctx, spec, dockerGen, svc Service) ([]CreatedResource, error) // the shared flow
```
App Runner/GCR/ACA implement `Service`; the base owns build+push, PORT forcing,
CreatedResource shaping, and the shape-aware liveness handoff.

### Acceptance criteria
- **AC1** — App Runner and Cloud Run are refactored onto the base with **no behavior change** (golden dispatch test still green).
- **AC2** — a hypothetical new container cloud needs only a `Service` impl; the base is untouched.
- **AC3** — the optional hooks (`MakePublic`, `Rollback`) are genuinely optional (nil/no-op safe).

### Edge cases & mitigation
- **Clouds diverge** (App Runner needs an access-role dance; Cloud Run needs IAM invoker; secrets differ). Mitigation: the hooks (`MakePublic`) + per-service pre/post steps; don't over-abstract — keep escape hatches.
- **"Config-driven generic adapter"** (a `PROD_CLOUD=custom` with an endpoint in config) is tempting but the APIs differ too much to fully data-drive. Mitigation: scope Level 2 to a *code* base with hooks, not a config DSL. Revisit a config adapter only if a real long-tail demand appears.

### Effort: **M.** Best done right after Azure (three container clouds is enough signal to factor the base correctly, not one).

---

## 3. Level 3 — out-of-tree provider plugins (gRPC)  `[the platform play]`

### Goal
A company or community can ship a cloud provider **without forking prod** —
`prod-provider-acme` as a separate binary prod discovers and drives over gRPC (the
HashiCorp Terraform-provider model). This is what turns prod from "a tool with N
clouds" into "a deployment platform with an ecosystem," and directly serves the
"devops use prod for their own internal PaaS" goal.

### Design
- **Transport:** `hashicorp/go-plugin` over gRPC (proven; handles subprocess
  lifecycle, handshake, version negotiation). Not Go's native `plugin` (fragile,
  same-toolchain-only).
- **The interface split matters.** prod (host) keeps the LLM, the analyzer, and the
  **local docker build+push** (using registry info the plugin supplies); the plugin
  implements only the cloud-service half:
  ```proto
  service Provider {
    rpc RegistryInfo(RegistryReq) returns (RegistryCreds);   // where/how to push
    rpc Deploy(DeployReq) returns (DeployResult);            // create/update from an image ref → id
    rpc WaitReady(WaitReq) returns (ReadyResult);            // → url
    rpc Rollback(RollbackReq) returns (Empty);
    rpc CheckAuth(Empty) returns (AuthStatus);
    rpc Metadata(Empty) returns (ProviderMeta);              // name, aliases, domain suffix, capabilities
  }
  ```
- **Discovery:** scan `~/.prod/plugins/` and `$PROD_PLUGINS` for `prod-provider-*`
  binaries; register each via the same Level-1 `PlatformSpec` (a plugin is just a
  registration whose factories proxy to gRPC). So Level 3 *rides Level 1* — that's
  why Level 1 comes first.
- **UX:** `prod plugin list` / `prod plugin install <url-or-path>` (v1: manual
  drop-in + `prod plugin list`).

### Acceptance criteria
- **AC1** — a sample `prod-provider-example` binary, dropped in `~/.prod/plugins/`, makes `prod "deploy to example"` work end-to-end with **no prod recompile**.
- **AC2** — a plugin implementing a bad/older protocol version is rejected with a clear message, not a crash (version negotiation).
- **AC3** — `prod plugin list` shows discovered providers, their version, and validity.
- **AC4** — a crashing/hanging plugin fails the deploy cleanly (bounded, surfaced), never hangs prod.

### Edge cases & mitigation (this level is mostly risk management)
- **[SECURITY] Arbitrary binaries with the user's cloud creds.** A plugin runs with
  the user's privileges and receives scoped creds for its cloud. Mitigation: (a) an
  explicit trust step — `prod plugin install` records a checksum; prod refuses to run
  a plugin whose checksum changed without re-consent; (b) document the trust model
  loudly (same as Terraform providers / VS Code extensions); (c) pass a plugin *only*
  the credentials/registry for its own cloud, never the full environment. This is the
  single most important design constraint — get the credential-scoping right.
- **Protocol versioning.** The gRPC contract must be versioned; host+plugin negotiate
  via go-plugin's handshake + a `ProviderMeta.protocol_version`. Breaking changes bump
  the version; old plugins get a clear "rebuild against protocol vN" error.
- **Dependency weight.** go-plugin + grpc + protobuf. Justified for the platform play;
  gated behind a build tag or kept lean so the base binary isn't bloated for users who
  never use plugins.
- **The build/push boundary.** Docker build stays host-side (the plugin can't see the
  user's source safely); the plugin returns registry creds and consumes an image ref.
  Nail this in the proto so plugins never need the source tree.
- **Windows/cross-platform plugin binaries.** Discovery must match the host OS/arch.

### Effort: **L (the biggest bet).** Do it once Level 1 exists (plugins register through it) and there's real third-party demand. It's the differentiator for the devops/platform audience.

---

## 4. Prioritized sequence & rationale

1. **Level 1 now** — it removes the ~10-switch tax (and the class of silent
   `getProjectDetector` bug) *before* Azure, so Azure is a one-file add. Highest
   leverage; the completeness test alone is worth it.
2. **Azure Container Apps** — rides Level 1 (proves the framework on a real cloud #4).
3. **Level 2** — factor the managed-container base once three container clouds exist.
4. **Level 3** — the ecosystem/platform bet, when third-party demand is real; it
   registers *through* Level 1, so no rework.

**One-line thesis:** make "add a cloud" cost one adapter + one registration — first
for us (Level 1), then for the community without forking (Level 3) — because the
breadth of "deploy anywhere in English" is a top reason to choose prod, and today
that breadth is gated by boilerplate and silent-miss bugs.
