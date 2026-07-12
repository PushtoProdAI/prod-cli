# Implementation Plan — F8 (shape-aware plugin SDK) → 2D (prod-provider-daytona)

_Plan document, no code. Dependency chain: **F1 (done) → 2B (done) → F7 (done) → F8 → prod-provider-daytona.**
Two repos: `prod-cli` (`/Users/devarisbrown/Code/prod/prod`), `prod-plugin-sdk`
(`/Users/devarisbrown/Code/prod/prod-plugin-sdk`), `prod-plugins`
(`/Users/devarisbrown/Code/prod/prod-plugins`)._

## REVISIONS FROM ADVERSARIAL REVIEW
**Verdict: no blockers.** The riskiest call — **no ProtocolVersion bump** — is CONFIRMED SAFE
(verified against go-plugin v1.8.0: net/rpc + gob, adding a named-string field is additive in
both skew directions; `Shapes==nil ⇒ web` degrades gracefully at every hop; a bare bump WOULD
strand DO/Koyeb/Railway, so keeping `ProtocolVersion=1` is correct; `VersionedPlugins` is the only
acceptable fail-closed alternative if ever wanted). Required refinements before building **2D**
(F8's host+SDK mechanics are safe to build now):

- **[SHOULD-FIX S1 — the key gap] Add `Shape DeployShape` to `DeployRequest` (host→plugin).**
  Today `DeployRequest` (`prod-plugin-sdk/provider.go:77-83`) carries no shape, so the plugin's
  `Deploy` can't know whether the host wants a URL. Putting shape only on `Meta` (capability) and
  echoing via `DeployResult.Shape` are both host-facing — neither tells the plugin *at deploy
  time*. The "core Fly/Render don't dictate shape" rationale is a false analogy: those adapters are
  in-process and read `input.Shape` directly; a plugin is across an RPC that strips it. Populate
  `DeployRequest.Shape` from `input.Shape` in `bridge.go:65` — gob-additive, same zero-risk skew as
  `Meta.Shapes`. This removes the "return a URL and hope the host discards it" trap and lets a
  Daytona plugin decide whether to allocate a preview. **Do not defer this.**
- **[SHOULD-FIX S2] The URL-less path currently depends on the ANALYZER, not the plugin — a
  web-classified agent fails at `workflow_container.go:93`.** The gate `!input.Shape.HTTPShaped()`
  uses `input.Shape` from `planning.go:157-163` (LLM/analyzer), which defaults unknown → `web`
  (`shape.go:30`). So `prod "deploy this agent to daytona"` takes the URL-less path ONLY if the
  project classifies as worker — the exact pre-2B failure, resurfacing for the flagship use case.
  **Fix:** adopt S1 **and treat `DeployResult.Shape` as in-scope for 2D (not the deferred optional)**
  so the plugin is authoritative for its own runtime; at minimum, fail FAST at plan time
  ("Daytona runs agents/workers; your project looks like a web app") instead of mid-workflow.
- **[SHOULD-FIX S3] Cross-module constant drift needs a compiled test, not a doc note.** The SDK
  genuinely can't import `deployment` (separate module + `internal/`), so the four shape strings
  are mirrored and must stay byte-identical (`shape.go:14-18`). But `prod-cli` CAN import the SDK
  (`plugins.go:13`), so add a `prod-cli` test asserting `string(plugin.ShapeWeb)==string(deployment.ShapeWeb)`
  for all four AND enumerating the full core set, so a NEW core shape added without an SDK mirror
  trips CI. (On mismatch `ParseShape` silently returns web — safe for a truly-unknown string, but
  silent-wrong for a real worker; the test, not the runtime default, is the guarantee.) Add to F8's AC.
- **[CONSIDER C1] Prefer Daytona's SIGNED preview link** (`GetSignedPreviewLink`, token embedded)
  over the bare `GetPreviewLink` + separate token, so prod's existing liveness probe (which treats
  a 401 token-gated URL as merely "reachable", `monitoring.go:139-177`) actually reaches the agent
  and the MCP handshake becomes meaningful. `DeployResult` has no token/header field, so this is
  the only way the host can verify a token-gated agent. Use it in the Deploy arc (2D.2.4).
- **[CONSIDER C2] Plugin worker records render as no-URL workers (F7 handles it, good), but** the
  `deploytarget` `switch plat` has no plugin case, so the record gets a misleading "identifier not
  recorded" note (`deploytarget.go:131-132`). Also ensure the container early-return persists the
  plugin's *canonical* platform token (not the raw enum string) so `history.CanonicalPlatform`
  maps it back. Minor.
- **[CONSIDER C3] Insertion point precision:** the early-return goes at `workflow_container.go:92`
  — AFTER the primary-resource loop + `svc.ID==""` check (`:82-91`, keep that as a real failure)
  and BEFORE the `u==""` assert (`:93-95`).
- **[CONSIDER C4] Add `docs/plugins.md` to the F8 change inventory** — it has zero shape/URL-less
  content today; document the `Meta.Shapes` declaration + the URL-less contract.

**Net:** **F8 (SDK + host) is safe to build now** — additive, no protocol bump, unit-testable with
no cloud. Land **S1 + S2 (`DeployRequest.Shape` + in-scope `DeployResult.Shape`) and S3 (the drift
test)** before building **prod-provider-daytona**, or its headline URL-less-agent story fails when
the analyzer guesses `web`.

---

## 0. The problem, grounded in the tree

A provider plugin is HTTP-service-only today. Two hard walls:

1. **The SDK contract carries no shape.** `plugin.Meta` is only
   `Name/Aliases/DomainSuffix/SupportsRollback`
   (`prod-plugin-sdk/provider.go:52-58`). A plugin cannot declare "I'm a worker/agent
   runtime, I may return no URL."

2. **The plugin deploy path hard-requires a URL.** A plugin registers with
   `ManagedContainer: true` (`cli/internal/agent/plugins.go:72`), so its deploy routes
   through the shared container workflow (`workflow.go:182-183` → `deployContainer`).
   That workflow finds the primary resource and then **hard-errors on an empty URL**:

   ```
   workflow_container.go:89-95
     if svc.ID == ""  → "…returned no primary service"
     if u == ""       → "…returned no URL"
   ```

   So a worker/agent plugin fails exactly like a worker-on-a-container-cloud did before
   2B — mid-workflow, on the URL assertion.

The core already solved this for its own workers. The **model to mirror** is the Fly
worker early-return: after the deploy activity, before any URL fetch/liveness,
`workflow_flyio.go:174-189` does `if !input.Shape.HTTPShaped() { record URL-less
success; return }`. Liveness itself is already shape-aware —
`verifyLiveness` (`monitoring.go:107-117`) returns immediately for
`ShapeWorker`/`ShapeCron`. The shape type and the `HTTPShaped()` predicate live in
`deployment/shape.go:11-41`. F8 extends that same shape awareness across the plugin RPC
boundary and into `deployContainer`.

---

# PART F8 — Shape-aware plugin SDK

## F8.1 — SDK change: how shape is represented across the RPC boundary

**Constraint (hard):** the SDK package "imports nothing from prod's internal packages"
(`prod-plugin-sdk/provider.go:1-10`) — it **cannot** import
`prod-cli/internal/deployment`. So `deployment.DeployShape` cannot cross the boundary as
that type.

**Decision — mirror the enum in the SDK as a string type.** Add to
`prod-plugin-sdk/provider.go`:

- `type DeployShape string` with the **same string values** the core already uses
  (`shape.go:14-18`): `"web"`, `"mcp-server"`, `"worker"`, `"cron"`. Same strings ⇒ the
  host maps SDK↔core with the existing `deployment.ParseShape` (`shape.go:22-33`) and
  `String()` — no new mapping table, no drift as long as the constants match. Add a
  package-level doc comment cross-referencing `shape.go` as the source of truth.
- Represent it over gob as a plain string field (a named-string type gob-encodes as its
  underlying string), so the wire form is a string regardless.

**Decision — declared capability on `Meta`, not (only) per-Deploy.** Add:

```
Meta.Shapes []DeployShape   // shapes this provider can deploy; empty ⇒ {web} (back-compat)
```

Rationale for `Meta.Shapes` (capability) over a shape on `DeployRequest`:
- The host needs the plugin's shape support **at plan/liveness-decision time**, derived
  from install-time metadata, without a live deploy — same way `SupportsRollback`
  already gates rollback in `bridge.go:115`. `Meta` is the natural home; it's read once
  at install (`Inspect`, `install.go:33-40`) and persisted to the manifest.
- The **actual** shape of a given deploy still comes from the user's project
  (`input.Shape`, set by the analyzer/intent) — consistent with core Fly/Render, where
  the plugin doesn't dictate shape, it declares which shapes it *can* serve.

**Optional (recommended additive) — `DeployResult.Shape DeployShape`.** Let the plugin
**echo the authoritative shape it actually deployed** on the result
(`provider.go:85-90`). Daytona may run a project the analyzer called `web` as a pure
URL-less sandbox worker; letting the plugin state "I deployed this as `worker`, here's no
URL" removes ambiguity. Host precedence: `DeployResult.Shape` (if non-empty) overrides
`input.Shape` for the URL/liveness decision. This is a zero-risk additive gob field. See
F8 open decision (§9).

**No change needed to the six method signatures** — shape rides inside the existing
`Meta` / `DeployResult` structs already carried by the gob envelopes
(`MetaReply`, `DeployReply` in `rpc.go:47-66`).

## F8.2 — ProtocolVersion: the riskiest part, and a correction to the roadmap

The roadmap (dx-roadmap.md:206) says *"Bump `plugin.ProtocolVersion` (it's a contract
change) so old plugins are rejected cleanly."* **This plan recommends NOT bumping — and
that recommendation is load-bearing, so here is the full reasoning.**

**How the version gate works.** `ProtocolVersion = 1` (`provider.go:14-17`) is the
`HandshakeConfig.ProtocolVersion` (`rpc.go:16-20`), used by both `Serve`
(`serve.go:11-16`, plugin side) and `Launch`
(`pluginhost/client.go:23-32`, host side). go-plugin compares them during
`client.Client()`; a mismatch returns an error that `Launch` already surfaces as
*"failed to launch plugin … (protocol/checksum mismatch?)"* (`client.go:39-41`). So a
bump to `2` means **a host built with the v2 SDK refuses to launch any plugin still
compiled against v1** — DigitalOcean, Koyeb, and Railway
(`prod-plugins/providers/*`) would all fail at launch until each is rebuilt against the
v2 SDK, re-released, and **re-installed** (the manifest pins an absolute binary path +
sha256 checksum, `manifest.go:13-20`; a rebuilt binary changes the checksum, so `prod
plugin list` would already flag them "checksum changed — reinstall",
`cmd/plugin/plugin.go:243-248`).

**Why a bump is unnecessary here — the transport is gob, and the change is purely
additive.** The RPC is net/rpc with gob envelopes (`rpc.go:43-78`). gob tolerates struct
evolution in both directions: an **unknown** field is ignored on decode, a **missing**
field is zero-valued. Concretely:
- **v2 host ← v1 plugin:** the v1 plugin returns a `Meta` with no `Shapes` field. gob
  decodes it into the v2 `MetaReply.Meta` with `Shapes == nil`. Host maps `nil → {web}`.
  The plugin behaves exactly as it does today (web-only). ✅
- **v1 host ← v2 plugin** (a v2-built plugin installed on an older prod): the v2 plugin
  sends `Shapes`; the v1 host's gob decoder ignores the unknown field. Old behavior. ✅

Because `Shapes` **defaults to `{web}`** and the host's URL relaxation is **gated on a
non-HTTP shape that the plugin declares**, an old plugin (no declared non-web shape) is
never routed down the URL-less path — it still returns a URL, the host still requires one,
nothing breaks. There is **no semantic a v1 plugin can get wrong** under the v2 host.

**Recommendation.** Keep `ProtocolVersion = 1`. Ship the field addition as additive.
Add a regression test proving a fixture plugin built against the *pre-F8* SDK still loads
and deploys web under the post-F8 host (see AC). Reserve the bump for a genuinely
breaking change (a changed/removed method, a re-typed field). **If** the maintainer
prefers the roadmap's fail-closed posture anyway, the correct mechanism is **not** a bare
bump (which strands all three existing plugins on the next prod release) but
go-plugin's `VersionedPlugins map[int]goplugin.PluginSet` in the `ClientConfig`
(`client.go:23-32`), which lets the host speak **both** v1 and v2 during a migration
window — call this out explicitly rather than accepting a hard break. This is the single
riskiest decision in F8; the additive path retires the risk entirely.

## F8.3 — Relax the plugin deploy path (host side, prod-cli)

Goal: a plugin whose declared shape is non-HTTP returns no URL without tripping
`workflow_container.go:93-95`, and its liveness skips the HTTP probe.

**(a) Thread the plugin's declared shapes to the workflow's decision point.**
- Add `Shapes []deployment.DeployShape` to `PlatformSpec` (`platforms.go:30-58`).
  Built-ins register `nil`/`{ShapeWeb}` (unchanged behavior). Add a helper
  `func (p Platform) SupportsShape(s deployment.DeployShape) bool` (nil/empty ⇒ web-only),
  living beside `LookupPlatform` (`platforms.go:74-78`).
- Populate it for plugins in `registerPlugin` (`plugins.go:63-81`): map
  `Entry.Shapes` (new manifest field, F8.4) through `deployment.ParseShape` into
  `PlatformSpec.Shapes`, and pass the same into the `plugin.Meta` it builds at
  `plugins.go:63`.

**(b) Add the URL-less early-return to `deployContainer`, mirroring Fly.** In
`workflow_container.go`, **before** the primary-service / URL asserts at `:82-95`, insert:

```
if !input.Shape.HTTPShaped() && platformSupportsShape(input.Platform, input.Shape) {
    // find the Primary resource for its ID (ID==""/no resource is still a real failure)
    // record URL-less success (operationId → "success" with shape + resourceId + no url),
    //   exactly like workflow_flyio.go:178-188
    return deployResult{Url: ""}, nil
}
```

Notes:
- Gate on **both** `!HTTPShaped()` **and** `SupportsShape` so a container cloud (AWS /
  Cloud Run / Azure — `Shapes == {web}`) that somehow received a worker shape still falls
  through to the existing error/redirect rather than silently succeeding URL-less. (Core
  2B already steers worker/cron away from container clouds; this is defense in depth.)
- Still require a non-empty **`svc.ID`** (a real "the deploy did nothing" failure).
- The success metadata must carry `"shape": input.Shape.String()` so F7's URL-less
  rendering in `prod ls/open/status/logs` works (the same key Fly writes,
  `workflow_flyio.go:186`).
- Liveness (`AgentVerifyLiveness`, `workflow_container.go:111`) already no-ops for
  worker/cron (`monitoring.go:109-111`) — but it's after the URL asserts, so the
  early-return is what actually unblocks it. For an **exposed** agent (plugin returns a
  URL, shape `mcp-server`/`web`), we skip the early-return and flow through normal
  liveness unchanged.

**(c) `DeployResult.Shape` override (if F8.1 optional is taken).** Thread the plugin's
returned shape from `bridge.go` up to `deployContainer` so it can override `input.Shape`.
Path: add `Shape` to `plugin.DeployResult` (SDK) → `managedcontainer.DeployResult`
(`managedcontainer.go:19-28`) → carry it on the primary `CreatedResource.Metadata`
(`managedcontainer.go:76-88`, e.g. `md["shape"]`) → read it in `deployContainer` before
the gate. Keep v1 simple (gate on `input.Shape`); wire this only if the analyzer-vs-Daytona
mismatch (§9) proves real in validation.

## F8.4 — Manifest + install plumbing (host side)

The manifest records plugin metadata at install so startup needn't launch each plugin
(`manifest.go:11-20`). Add the declared shapes so they survive a restart:

- `Entry.Shapes []string` in `manifest.go:13-20` (`json:"shapes,omitempty"`).
- Populate it in the install path: `Inspect` reads `Meta` (`install.go:33-40`); the
  install command copies `meta.Shapes` into the `Entry` at
  `cmd/plugin/plugin.go:149-152` (today it copies Name/Aliases/DomainSuffix/
  SupportsRollback — add Shapes, stringifying `[]plugin.DeployShape`).
- `registerPlugin` reads `Entry.Shapes` → `PlatformSpec.Shapes` (F8.3a).
- Back-compat: an existing manifest without `shapes` decodes to `nil` ⇒ web-only. No
  migration needed.

## F8.5 — Scaffolding: `prod plugin new` learns shape

Update the scaffolder (`cmd/plugin/new.go:92-167`, `scaffoldMainGo`) so a fresh plugin's
`Metadata` stub includes `Shapes: []plugin.DeployShape{plugin.ShapeWeb}` with a comment
pointing at worker/agent runtimes, and the `Deploy` stub comments that a non-HTTP shape
may return an empty `URL`. Purely additive to the template; no behavior change for
existing web plugins.

## F8.6 — F8 file-change inventory

**prod-plugin-sdk:**
- `provider.go` — `DeployShape` type + constants; `Meta.Shapes`; optional
  `DeployResult.Shape`; doc comment. `ProtocolVersion` **unchanged** (see F8.2).
- `rpc.go` — no signature change (structs ride existing envelopes); confirm gob round-trips
  the new fields (add to `rpc_ctx_test.go`).
- CI (`.github/`) — the SDK's `go test ./...` (roadmap 0.4) now also exercises shape
  round-trip.

**prod-cli:**
- `internal/deployment/shape.go` — no change (source of truth); optionally export a note
  that the SDK mirrors these strings.
- `internal/agent/platforms.go` — `PlatformSpec.Shapes`; `SupportsShape` helper.
- `internal/agent/plugins.go` — populate `Shapes` from `Entry`; pass to `plugin.Meta`.
- `internal/agent/workflow_container.go` — the URL-less early-return gate (F8.3b).
- `internal/pluginhost/manifest.go` — `Entry.Shapes`.
- `internal/pluginhost/bridge.go` — (only if F8.1 optional) map `plugin.DeployResult.Shape`
  → `managedcontainer.DeployResult.Shape`.
- `internal/deployment/managedcontainer/managedcontainer.go` — (optional) carry shape on
  the primary resource metadata.
- `cmd/plugin/plugin.go` (`install.go` Entry population) — copy `meta.Shapes`.
- `cmd/plugin/new.go` — scaffold `Shapes`.
- Tests: `workflow_container` shape branch; `plugins_test`/`platforms_test`
  SupportsShape; a **back-compat fixture** (pre-F8 plugin binary or a `Meta{Shapes:nil}`)
  proving web still works.

**Effort: F8 ≈ M** (contract + gob + host relax + manifest/scaffold plumbing + tests;
no protocol bump keeps it from ballooning).

---

# PART 2D — prod-provider-daytona

## 2D.1 — Daytona's current model (researched; cite before building)

Daytona pivoted to **secure elastic infrastructure for running AI-generated code** —
agent sandboxes, not a web-service PaaS. Confirmed against the docs:

- **Sandboxes** are created **from a Docker image or from a snapshot**; default 1 vCPU /
  1 GB / 3 GiB, up to 4 vCPU / 8 GB / 10 GB.
  ([Sandboxes](https://www.daytona.io/docs/en/sandboxes/))
- **Snapshots** are reusable pre-built sandbox templates (image + resources), created via
  SDK/CLI/API; a snapshot can also be captured from a running sandbox's filesystem
  (`createSnapshot`). ([Snapshots](https://www.daytona.io/docs/en/snapshots/))
- **Go SDK** (this plugin is Go, matching DO/Koyeb/Railway):
  `NewClient()` reads `DAYTONA_API_KEY` (or JWT + `DAYTONA_ORGANIZATION_ID`) from env;
  `Client.Create(ctx, params, opts...)` accepts `types.ImageParams` **or**
  `types.SnapshotParams`; `ProcessService.ExecuteCommand(...)` / `CodeRun(...)` run a
  process; `Sandbox.GetPreviewLink(ctx, port)` → `{url, token}`,
  `GetSignedPreviewLink(...)`; `Sandbox.SetAutoDeleteInterval(...)`, `Stop`, `Delete`.
  ([Go SDK](https://www.daytona.io/docs/en/go-sdk/daytona/))
- **Durability:** `autoStopInterval` defaults to **15 min idle**; **set to `0` to disable
  auto-stop** and keep a sandbox running indefinitely — essential for a long-lived agent.
  Critically, *"interactions using Sandbox Previews are not counted"* as activity, so even
  an exposed HTTP agent would be auto-stopped unless the interval is `0`.
  ([TS SDK / Sandbox](https://www.daytona.io/docs/en/typescript-sdk/sandbox/))
- **Preview URL** carries a **token** for private sandboxes (the token grants access; it's
  returned alongside the URL, not an ambient public URL).
  ([TS SDK / Sandbox](https://www.daytona.io/docs/en/typescript-sdk/sandbox/))

**Confirmed:** Daytona **does** support programmatic *image → (snapshot) → sandbox → run
process → preview URL → teardown* from the Go SDK with an API key. This is exactly the
plugin's Deploy arc.

## 2D.2 — The six `plugin.Provider` methods mapped to Daytona

Reference implementation for structure: `prod-plugins/providers/prod-provider-koyeb/main.go`
(external-registry pattern, upsert-by-name, image-swap rollback). Credentials resolved
from env inside the subprocess (the plugin never sees prod's creds; `client.go:52-77`
deliberately does **not** strip third-party tokens, so `DAYTONA_API_KEY` reaches the
plugin).

1. **`Metadata`** — `Name:"Daytona"`, `Aliases:["daytona"]`,
   `DomainSuffix` = Daytona's preview host suffix (fill from the `GetPreviewLink` URL
   host), `SupportsRollback: true` (snapshot recreate, 2D.2.6),
   **`Shapes: []plugin.DeployShape{plugin.ShapeWorker, plugin.ShapeMCPServer}`** —
   declares it as an agent/worker runtime that may (worker) or may not (mcp-server) expose
   a URL. This is the field F8 adds; it's what unlocks the URL-less path.

2. **`RegistryInfo`** — **Decision needed (§9).** Two options:
   - **(A, recommended v1) External registry** — mirror Koyeb
     (`koyeb/main.go:100-116`): the user names a registry they own
     (`DAYTONA_REGISTRY` / `_USER` / `_TOKEN`); prod (the host) builds+pushes there; the
     Daytona sandbox is created from that pushed image ref via `types.ImageParams`. Least
     Daytona-specific work; reuses the proven host build+push seam
     (`bridge.go:55-77`, `managedcontainer.Run`).
   - **(B) Daytona-native snapshot from image** — if Daytona can pull directly from a
     public/registry image or ingest a local image into a snapshot, `RegistryInfo` still
     names *some* registry prod pushes to (Daytona needs a pullable ref); the plugin then
     wraps that ref in a snapshot. Investigate whether Daytona has a first-party registry;
     if not, (A) stands.
   Either way the **host still does the docker build+push** — the plugin only names the
   registry (the `RegistryInfo` contract, `provider.go:60-67`).

3. **`CheckAuth`** — validate `DAYTONA_API_KEY` by a cheap authenticated call (e.g. list
   sandboxes/snapshots), plus assert the external-registry env is present (Koyeb's
   `registryConfigMissing`, `koyeb/main.go:90-96`). Return a precise `Detail` on failure
   so prod fails fast before building (the pattern `pluginhost.NewAuthProvider` surfaces,
   `bridge.go:157-171`).

4. **`Deploy(req)`** — the agent-sandbox arc:
   1. `NewClient()` (reads `DAYTONA_API_KEY`).
   2. Optionally create/update a **snapshot** from `req.ImageRef` + `Resources`
      (enables fast recreate and gives rollback a target); or create the sandbox directly
      from `types.ImageParams{Image: req.ImageRef}`.
   3. `Client.Create(ctx, params, WithEnv(req.PlainEnv+SecretEnv), WithAutoStop(0))` —
      **`autoStopInterval = 0`** so the agent runs durably (2D.1).
      Map `req.SecretEnv` to Daytona secrets/env; `req.PlainEnv` to plain env.
   4. Start the agent process: `ProcessService.ExecuteCommand(spec start command)` (a
      long-lived process) — the host passes the run command via env/spec; the sandbox's
      entrypoint or an explicit `ExecuteCommand` launches it.
   5. **Return shape:**
      - **Pure worker** (no HTTP port): `DeployResult{ID: sandboxID, Name, URL: ""}`
        (+ optional `Shape: plugin.ShapeWorker`). The F8 host relaxation records URL-less
        success. This is the flagship "deploy an autonomous agent into an isolated
        computer" outcome.
      - **Exposed agent** (agent serves a port, e.g. an MCP server): call
        `GetPreviewLink(ctx, port)` and return its `URL`. Note the preview URL is
        **token-gated** for private sandboxes — prod's `isURLLive` treats 401/403 as
        "reachable/live" (`monitoring.go:119-144`), so a token-gated preview passes web
        liveness as reachable; an **mcp-server** shape would require the JSON-RPC
        handshake (`isMCPServerLive`, `monitoring.go:150-185`) which a token-gated
        endpoint returns 401 for → also treated as "reachable". Acceptable for v1;
        document the token caveat (§9).
   6. Idempotency: upsert by `req.Name` (find existing sandbox/snapshot for the name;
      update rather than duplicate) — mirrors Koyeb `ensureApp`/`upsertService`
      (`koyeb/main.go:223-266`) and satisfies prod's create-or-update expectation
      (plugins register with no pre-detector, `plugins.go:80`).

5. **`PreviousDeployment(appName)`** — list the sandbox's prior **snapshots** (or the
   prior sandbox revision) for `appName`, return the most recent healthy one's ID as the
   rollback target (empty ⇒ first deploy / nothing to roll back). Mirrors Koyeb
   `PreviousDeployment` (`koyeb/main.go:320-343`). Gated by `Meta.SupportsRollback` on the
   host (`bridge.go:114-117`).

6. **`Rollback(appName, targetID)`** — recreate the sandbox from snapshot `targetID`
   (image/config swap) — the same "re-apply the prior definition" rollback Koyeb uses for a
   registry with no rollback-by-id endpoint (`koyeb/main.go:345-372`). Set
   `SupportsRollback: true` in `Metadata` accordingly.

## 2D.3 — Credentials, scaffolding, listing

- **User supplies:** `DAYTONA_API_KEY` (required); `DAYTONA_ORGANIZATION_ID` if using a
  JWT; the external-registry trio `DAYTONA_REGISTRY` / `_USER` / `_TOKEN` (option A).
  All resolved from env in the subprocess; `pluginhost/client.go` does not strip them.
- **Scaffold:** `prod plugin new daytona` → `./prod-provider-daytona/` with the six
  methods stubbed and (post-F8.5) `Shapes` prefilled. Fill against the Daytona Go SDK,
  `go build`, `prod plugin install ./prod-provider-daytona` (`cmd/plugin/new.go:39-77`).
  Then publish a GitHub release + checksum for `prod plugin install
  github.com/…@daytona-vX --checksum <sha256>` (`cmd/plugin/plugin.go:170-204`).
- **Listing:** add an entry to `prod-plugins/plugins.json` (name `daytona`, repo,
  maintainer, description, the exact `--checksum` install command) — a PR against the
  curated index (`pluginhost/index.go:18-27`), validated by the plugins.json schema
  (`prod-plugins/plugins.schema.json`). It lands as a sibling of digitalocean/koyeb/railway.

## 2D.4 — Siblings on the same F8 foundation (note only)

Once F8 lands, **E2B** and **Modal-sandbox** are the same category — image/template →
sandbox → run agent → optional preview URL → teardown — and become sibling plugins
(`prod-provider-e2b`, `prod-provider-modal-sandbox`) declaring
`Shapes: [worker, mcp-server]`, reusing the identical host relaxation. No further core
change. Not planned here beyond noting the shared foundation.

---

## 8. Effort, sequencing, dependency chain, ACs, validation

**Dependency chain (all prereqs done):** F1 → 2B → F7 → **F8** → **prod-provider-daytona**.
Daytona lands *after* the worker foundation is proven, as an ecosystem plugin, not core.

**Effort:** F8 ≈ **M** (additive contract + host relax + plumbing + tests).
prod-provider-daytona ≈ **M–L** (a real cloud integration; parallel once F8 lands).

**Sequencing:**
1. F8 SDK change (additive `Shapes`, no protocol bump) + gob round-trip test.
2. F8 host relax (`PlatformSpec.Shapes`, `SupportsShape`, `workflow_container` early-return,
   manifest + install + scaffold plumbing) + tests incl. back-compat fixture.
3. Rebuild/re-release the SDK; **verify DO/Koyeb/Railway still load & deploy web
   unchanged** (they need no rebuild under the additive path — this is the key
   backward-compat gate).
4. prod-provider-daytona: scaffold → implement 6 methods → local build/install →
   validate against a live Daytona account.
5. List in `prod-plugins/plugins.json`.

**Acceptance criteria — F8:**
- A plugin declaring `Shapes:[worker]` that returns `DeployResult{URL:""}` deploys with
  **no** "returned no URL" error; the record persists with `shape:"worker"` and renders
  cleanly in `prod ls/open/status/logs` (F7).
- Its liveness **skips** the HTTP probe; no false auto-rollback.
- A plugin declaring only web (or `Shapes:nil`) is **unchanged** — still requires a URL.
- A container cloud (AWS/Cloud Run/Azure) is unaffected (`Shapes:{web}`; a non-web shape
  falls through to the existing path, no silent URL-less success).
- **Back-compat:** a plugin binary built against the **pre-F8** SDK still loads and
  deploys web under the post-F8 host (proves no protocol break). `prod plugin list` shows
  DO/Koyeb/Railway "ok" without rebuild.
- gob round-trips `Meta.Shapes` (and `DeployResult.Shape` if adopted) in
  `rpc_ctx_test.go`.

**Acceptance criteria — prod-provider-daytona (needs a live Daytona account):**
- `CheckAuth` passes with a valid `DAYTONA_API_KEY` + registry env; fails clearly without.
- `prod "deploy this agent to daytona"` on an `agent-worker` project → builds+pushes the
  image (host), creates a snapshot+sandbox with `autoStopInterval=0`, starts the agent,
  returns **no URL**, and prod records a URL-less worker success (no probe, no rollback).
- An exposed variant returns a working **preview URL**; web/mcp liveness treats it as
  reachable (token caveat documented).
- Rollback: a second deploy, then `PreviousDeployment` + `Rollback` recreates the sandbox
  from the prior snapshot; the agent runs again.
- Listed in `plugins.json`; installable via the checksum flow.

**What strictly needs a live Daytona account to validate:** the entire Deploy/rollback arc
(image→snapshot→sandbox→process→preview→teardown), `autoStopInterval=0` durability, the
preview-URL token behavior, and the exact Go SDK types/opts
(`ImageParams`/`SnapshotParams`/`WithAutoStop`) — the docs confirm the surface but the
field/opt names must be pinned against the installed SDK version. Everything host-side
(F8) is unit-testable with the `llm.Client` mock + a fake plugin and needs **no** cloud.

---

## 9. Open decisions (surface for review)

1. **Shape across RPC — Meta capability vs per-Deploy result.** Recommended:
   `Meta.Shapes` (declared capability, drives the host decision) as primary + optional
   additive `DeployResult.Shape` (authoritative override) if analyzer-vs-Daytona shape
   mismatch proves real. Both are gob-additive strings; the SDK mirrors
   `deployment.DeployShape`'s string constants and must not drift from `shape.go:14-18`.

2. **Protocol-version policy — the risky one.** Recommended: **do not bump**; ship
   additive, add a back-compat regression test, and leave DO/Koyeb/Railway untouched. The
   roadmap's "bump so old plugins are rejected" would strand all three on the next release.
   If fail-closed is still wanted, use go-plugin `VersionedPlugins` (speak v1+v2 during
   migration), **not** a bare bump. Decision owner: maintainer.

3. **Daytona: pure URL-less worker vs exposed preview URL.** Recommended default: treat
   Daytona as the **URL-less agent runtime** (`worker`) — that's the ICP-differentiated
   "autonomous agent in an isolated computer" story — and support the exposed preview URL
   as a secondary mode (`mcp-server`/`web`) where the agent serves a port. The token-gated
   preview URL (private sandboxes) passes prod's "reachable ⇒ live" liveness but not a
   strict handshake; acceptable v1, documented.

4. **Registry: Daytona-native vs external.** Recommended v1: **external registry** (Koyeb
   pattern) — the user names a registry they own, prod builds+pushes, Daytona pulls the
   image into a snapshot/sandbox. Revisit if Daytona exposes a first-party image ingest
   that removes the external-registry requirement.

5. **`autoStopInterval` value.** Recommended `0` (disable) for a durable agent; consider
   exposing `DAYTONA_AUTO_STOP` env for users who want cost-capped idle shutdown. Note the
   docs caveat that preview traffic doesn't count as activity, so anything < ∞ can stop a
   live-but-quiet agent.
