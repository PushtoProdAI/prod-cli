# prod — Cloud-Framework Level 3: out-of-tree provider plugins

**Status:** design (for review before build). Level 3 of the cloud-adapter framework
([cloud-framework-plan.md](./cloud-framework-plan.md)): let a third party ship a cloud
provider — `prod-provider-acme` — as a **separate binary** that prod discovers and
drives, so a company or community can add a deploy target **without forking prod**.
The Terraform-provider model, and the differentiator for the devops/platform audience.

## Review corrections (authoritative — supersede anything below they conflict with)

A principal-engineer + security review verified the architecture against the code
(a plugin genuinely is an L2 `Provider` over a subprocess; the registry/build/push
boundary is correct — `BuildAndPushToRegistry` only calls `reg.Name()`/`reg.Credentials()`,
never `Ref()`, so a static `pluginRegistry` suffices; the catalog + go-workflows
int-serialization accommodate a dynamic `Platform`; the subprocess lifetime fits inside
the single `AgentDeploySteps` activity). Verdict: **build, with these changes.**

**BLOCKING:**
- **[SECURITY, do first] Plugins must NOT inherit prod's environment.** go-plugin defaults
  `SkipHostEnv:false` and injects `os.Environ()` into the child — which would hand every
  plugin the user's `FLY_API_TOKEN`, `RENDER_API_KEY`, `VERCEL_TOKEN`, `NETLIFY_AUTH_TOKEN`,
  `HEROKU_API_KEY`, AWS creds, `PROD_REGISTRY_TOKEN`, and `OPENAI_API_KEY`/`ANTHROPIC_API_KEY`.
  Set `ClientConfig.SkipHostEnv:true` and pass a **curated `Cmd.Env`** (`PATH`, `HOME`, and
  the plugin's own cloud-cred vars only) so it can still resolve its ambient creds but
  cannot harvest prod's. → **new AC9** + a test asserting the child env excludes prod's
  sensitive vars. Also enable go-plugin **`AutoMTLS`** (stops a local rogue process
  connecting to the plugin socket) and **bound RPC response sizes**/enforce the deploy-ctx
  timeout on every call (a hostile plugin returning a huge response is a memory DoS).
- **[TECHNICAL] The JS-framework handlers do NOT dispatch through the catalog** — they
  `switch` on hardcoded enum lists (`javascript_frameworks.go` Svelte/Remix/Nuxt/TanStack),
  and Svelte's default even *errors* ("unsupported platform for Svelte"). A plugin (a
  node-container platform) hits `default` → SvelteKit/Remix/Nuxt apps **fail or misconfigure**.
  Fix: replace the enum lists with a catalog predicate — treat any `ManagedContainer`
  platform as a node-container (`if isNodeContainer(platform)` reading
  `LookupPlatform(p).ManagedContainer`). → **new AC10** (a plugin deploys a SvelteKit app
  correctly). This is the deferred L1b JS-switch fold, now unblocked by the `ManagedContainer` flag.

**SHOULD-FIX (fold into the build):**
- **URL install is trust-on-first-use, not authenticity.** SHA-256 via `SecureConfig` only
  catches drift *after* install. `prod plugin install <url>` must require **HTTPS**, accept
  an out-of-band **`--checksum <sha256>`** verified *before* first execution, print
  publisher + checksum + require explicit confirmation, and give install its **own handshake
  timeout** (a binary hanging on handshake must not hang `install`). Signature verification
  (minisign/cosign) → roadmap; ideally gate URL installs on it.
- **Wrong-cloud resume hazard.** Name-hash `Platform` values can collide across *sequential*
  install/uninstall, so a resumed workflow could deploy an app + its `SecretEnv` to the wrong
  cloud. Persist **`PluginName`** (and the pinned checksum) in `DeployPlan`; on resume, if
  `Platform` is in the plugin range, assert `LookupPlatform(Platform).Name == PluginName` else
  fail cleanly. (Confirms dynamic-int is the right call — the catalog is already int-keyed and
  go-workflows serializes the int — but it needs the name tiebreak.)
- **`Platform.String()` is user- and LLM-facing in ~10 sites** (plan/status/success/TUI/errors →
  `SummarizeDeployError` feeds the raw string to the LLM). Add a catalog-aware
  **`Platform.DisplayName()`** (fallback to `String()`) and sweep all user/LLM-facing sites
  (`agent.go`, `teawriter.go`, `errors.go`, `planning.go`, `workflow_container.go`); pure `slog`
  telemetry may keep `String()`.
- **Plugin spec must set `ManagedContainer:true`** (else `workflow.go` dispatch `default` →
  "unsupported platform") — enforce in the spec builder. **Reject alias collisions** with
  built-ins/other plugins (namespace or refuse).
- **`RegistryInfo` under-specified:** set `Credentials.AuthServer = Host`; thread the project
  name (`RegistryInfo(ctx, project)`) or namespace `Repository` per app.
- **`CheckAuth` needs a `pluginAuthProvider`** bridging the plugin RPC to `auth.AuthProvider`
  (unlisted piece of work).
- **The plugin SDK can't live in `internal/`** — Go forbids importing another module's
  `internal/`. Publish the contract as an **exported package** (`pkg/plugin`) or a separate
  `prod-plugin-sdk` module. (The `internal/plugin` naming below is wrong; use `pkg/plugin`.)

**New acceptance criteria (required before "done"):**
- **AC9 — env isolation:** a launched plugin's process env excludes prod's platform tokens,
  registry credential, and LLM key (test-asserted).
- **AC10 — Node-framework compatibility:** a SvelteKit (and Remix/Nuxt) app deploys to a plugin
  with the correct adapter + start command — the framework handlers treat plugins as
  node-container platforms via the catalog, not enum lists.

---

## The load-bearing insight

A plugin is **an L2 `Provider` proxied across a subprocess boundary, registered through
the L1 catalog.** Everything already built composes:

- **L2** already splits a managed-container deploy into "host does build+push" and
  "provider does the cloud-service half." A plugin is exactly that provider — the host
  keeps the LLM, analyzer, and **docker build+push** (using registry info the plugin
  supplies); the plugin only creates/polls/rolls-back the service. **The plugin never
  sees the user's source tree** — it gets an image ref and returns a URL.
- **L1** already dispatches every platform (deployable, auth, detector, menu, Django
  hosts, rollback gate) from a `PlatformSpec` in a catalog keyed by a `Platform` value.
  A plugin registers a `PlatformSpec` whose factories proxy to the subprocess and whose
  `Platform` is assigned dynamically. No new dispatch path.

So L3 is: a **transport** (go-plugin), a **host runtime** (discover → launch → proxy),
and a **catalog registration** for discovered plugins. The deploy flow itself is
unchanged.

---

## 1. Architecture

```
prod (host)                                    prod-provider-acme (plugin subprocess)
  discover ~/.prod/plugins.json  ─────────────────────────────────────────────────
  register PlatformSpec (dynamic Platform, factories proxy the plugin)
      │
  prod "deploy to acme"
      │  createDeployable → managedcontainer.Run(pluginProvider, spec, dockerGen)
      │
      ├─ pluginProvider.Prepare:
      │     launch subprocess (go-plugin, checksum-verified) ──handshake──▶ Serve()
      │     RPC: RegistryInfo() ◀───────────────────────────────────────── {url, auth}
      │     → pluginRegistry (implements registry.Registry)
      │
      ├─ host: dockerGen.BuildAndPushToRegistry(pluginRegistry)   [host-side build+push]
      │
      └─ deploy closure:
            RPC: Deploy(imageRef, spec-subset) ─────────────────▶ create cloud service
                 {id, name, url} ◀──────────────────────────────── poll ready → url
      → CreatedResource{Primary, url}  (base guarantees Primary)
```

**Transport:** `github.com/hashicorp/go-plugin` over **net/rpc** for v1 (Go plugins,
gob-encoded — no protobuf/protoc in the build). Cross-language plugins via go-plugin's
**gRPC** transport are a documented follow-up (same interfaces, swap the transport +
add a `.proto`); deferred because it needs protoc/buf codegen the repo doesn't have yet.

**Lazy launch via a manifest.** `prod plugin install` handshakes **once**, records the
plugin's metadata + binary checksum in `~/.prod/plugins.json`. Startup reads the
manifest (fast, no subprocesses) and registers lightweight `PlatformSpec`s; the plugin
subprocess is launched **only when its platform is actually deployed**. Startup never
spawns every plugin.

---

## 2. The plugin contract (`internal/plugin`)

The interface a provider implements (host and plugin share this Go interface; the RPC
stubs are hand-written for net/rpc):

```go
type Provider interface {
    Metadata() (Meta, error)                         // name, aliases, domain suffix, capabilities
    RegistryInfo(ctx) (RegistryInfo, error)          // where/how to push the image
    CheckAuth(ctx) (Status, error)                   // are the user's creds for this cloud usable?
    Deploy(ctx, DeployRequest) (DeployResult, error) // create/update service from imageRef → id,name,url
    PreviousDeployment(ctx, Ref) (DeployInfo, error) // rollback target (optional)
    Rollback(ctx, Ref, target string) error          // optional
}

type Meta struct {
    Name             string   // "Acme Cloud"
    Aliases          []string // "acme", "acme-cloud"
    DomainSuffix     string   // ".acme.app" (Django hosts)
    SupportsRollback bool
    ProtocolVersion  uint
}
type RegistryInfo struct { Host, Repository, Username, Token string } // → registry.Credentials
type DeployRequest struct { ImageRef string; Name string; Port int; PlainEnv, SecretEnv map[string]string }
type DeployResult struct { ID, Name, URL string }
```

Host-side proxies:
- `pluginRegistry` implements `registry.Registry` from `RegistryInfo` (Ref/Credentials).
- `pluginProvider` implements `managedcontainer.Provider` — `Prepare` launches the
  subprocess, calls `RegistryInfo`, returns the `pluginRegistry` + a deploy closure that
  calls `Deploy`; `ResourceType()` returns `"<name>_service"`.
- A `PlatformSpec` builder turns a manifest entry into a catalog registration.

---

## 3. Acceptance criteria

- **AC1 — end-to-end, no recompile.** A sample `prod-provider-example` binary, installed
  via `prod plugin install ./prod-provider-example`, makes `prod "deploy to example"` run
  the full flow: host builds+pushes to the registry the plugin names, the plugin's
  `Deploy` returns a URL, and prod reports it — **with no change to the prod binary**.
- **AC2 — discovery + listing.** `prod plugin list` shows installed plugins with name,
  version, and validity. A registered plugin appears in the deploy menu and resolves by
  its aliases in natural language.
- **AC3 — protocol negotiation.** A plugin built against an incompatible protocol version
  is rejected at install/launch with a clear "rebuild against protocol vN" message — not
  a crash, not a silent skip.
- **AC4 — checksum trust.** `prod plugin install` records the binary's SHA-256; prod
  refuses to launch a plugin whose checksum changed since install (via go-plugin
  `SecureConfig`), telling the user to re-install. Prod never downloads or runs a plugin
  without an explicit `install`.
- **AC5 — fault isolation.** A plugin that crashes, panics, or hangs during a deploy fails
  that deploy cleanly (bounded by the deploy timeout, surfaced via `SummarizeDeployError`)
  and never hangs or crashes prod.
- **AC6 — framework integration.** A plugin dispatches through the **L1 catalog**
  (createDeployable/getAuthProvider/getProjectDetector/menu/rollback-gate/Django all derive
  from its `PlatformSpec`) and deploys through the **L2 base** (host build+push + plugin
  `Deploy`). A completeness-style test asserts a registered plugin resolves through every
  path. `SupportsRollback=false` → the friendly rollback gate fires.
- **AC7 — deterministic identity.** A plugin's dynamically-assigned `Platform` value is
  stable across restarts (derived from its name) and cannot collide with a built-in or
  another plugin; a collision is rejected at registration with an error.
- **AC8 — lazy.** Startup reads the manifest and registers specs without launching any
  plugin subprocess; a plugin is spawned only on a deploy to it.

---

## 4. Edge cases & mitigation

- **[SECURITY #1] The plugin runs as the user and receives the app's env — including
  secrets.** A plugin deploys the app, so it necessarily gets the app's `SecretEnv` and
  runs with the user's privileges (same trust surface as a Terraform provider or a VS
  Code extension). Mitigation: (a) install is explicit + checksum-pinned (AC4); (b) prod
  passes a plugin **only the deploy request** (image ref, name, this app's env) — never
  other platforms' tokens or unrelated secrets; (c) the plugin resolves its **own** cloud
  creds from the user's ambient config (like built-in adapters read `~/.aws`), so prod
  isn't a secret conduit; (d) `prod plugin install` prints the trust model plainly. This
  is the single most important constraint — document it loudly.
- **Dynamic `Platform` value / serialization.** Built-ins occupy `0..UnknownPlatform`.
  A plugin's value is `UnknownPlatform + 1 + (stable hash of name)` in a reserved high
  range, checked for collision at registration. Because go-workflows persists
  `DeployPlan.Platform` as an int, the value must be **deterministic per plugin name**
  across restarts (it is — derived from the name) so an in-flight deploy resumes to the
  same plugin. Deploys are short-lived, so a manifest change mid-deploy is a documented,
  low-probability edge.
- **`Platform.String()` for plugins.** The stringer only knows built-ins → returns
  `"Platform(N)"`. The container workflow currently logs `strings.ToLower(Platform.String())`.
  Fix (small L1c change): log `LookupPlatform(p).Name` (falling back to `String()`), so a
  plugin logs "Acme Cloud", not "platform(1002)".
- **Plugin crash / hang / panic.** go-plugin manages the subprocess; a dead plugin makes
  the in-flight RPC error, which the deploy activity surfaces. Bound each RPC with the
  deploy context; never block unbounded. The plugin client is killed on deploy completion.
- **Protocol-version mismatch.** go-plugin `HandshakeConfig.ProtocolVersion` +
  `MagicCookie` reject an incompatible or non-prod binary at launch. Surface a clear
  message; skip registration.
- **Checksum drift.** `SecureConfig{Checksum, Hash: sha256}` refuses a changed binary.
  `prod plugin install` re-records on an intentional update.
- **Wrong OS/arch binary.** Discovery only lists executables; a mismatched binary fails
  the handshake with a clear error, doesn't register.
- **Dependency weight.** go-plugin pulls `yamux` + `oklog/run` (no protobuf with net/rpc)
  — modest. Acceptable; the base binary already links cloud SDKs far larger. (gRPC's
  protobuf weight is deferred with the cross-language transport.)
- **Concurrent deploys to one plugin.** Each deploy launches its own client subprocess
  (simple, isolated); no shared mutable plugin state. Revisit pooling only if it matters.
- **Windows.** Discovery matches `prod-provider-*.exe`; go-plugin supports Windows. The
  sample plugin cross-compiles.
- **A plugin lies about its registry / returns a bad image ref.** The host build+push to a
  bogus registry fails at push (surfaced); the plugin can't get the host to push
  elsewhere silently. A plugin returning no URL → the base's "no URL" error.

---

## 5. UX & DX

**User (operator) UX**
- `prod plugin install <path|url>` — validate (handshake), record metadata + SHA-256 in
  the manifest. A URL downloads to `~/.prod/plugins/` first. Prints: the provider name,
  the checksum, and a one-line trust notice ("plugins run as a subprocess with your
  permissions; only install ones you trust").
- `prod plugin list` — table: name, version, path, valid (checksum OK / protocol OK).
- `prod plugin remove <name>`.
- After install, the plugin is a first-class target: it's in the `prod` menu and
  `prod "deploy this to acme"` resolves via its aliases. Nothing else changes for the user.
- On a checksum/protocol failure, the error names the plugin and the exact remedy.

**Developer (plugin author) DX**
- Writing a provider = implement the `Provider` interface + call `plugin.Serve(provider)`
  in `main()`. The sample `prod-provider-example` is the copy-paste template.
- `prod`'s repo publishes the `internal/plugin` contract as an importable package (or a
  small `prod-plugin-sdk`) so authors compile against a stable interface + protocol
  version. Versioning the protocol is explicit (`ProtocolVersion`).
- A provider is ~the cloud API calls (Deploy/poll/rollback) + Metadata/RegistryInfo — the
  same ~100 lines an in-tree L2 Provider is, minus the fork.

---

## 6. Sequencing (reviewable PRs)

1. **Protocol + harness + sample** — `internal/plugin` (the `Provider` interface + request/
   response types + the go-plugin net/rpc client/server stubs + `Serve`/handshake) and a
   sample `prod-provider-example` binary. Unit-test the RPC round-trip via an in-memory
   plugin. *No host integration yet.*
2. **Host runtime + catalog integration** — discovery + `~/.prod/plugins.json` manifest,
   the `pluginRegistry`/`pluginProvider` proxies, dynamic-`Platform` registration into the
   L1 catalog, and the L1c `Platform.String()`→`Name` logging fix. `prod "deploy to
   example"` now works end-to-end (AC1/AC5/AC6/AC7/AC8).
3. **`prod plugin` CLI** — install/list/remove + `SecureConfig` checksum trust + the trust
   notice (AC2/AC3/AC4).
4. **Follow-up** — go-plugin **gRPC** transport for cross-language plugins.

**One-line thesis:** a plugin is an L2 `Provider` behind a checksum-trusted subprocess,
registered through the L1 catalog — so third parties add clouds without forking prod, and
the deploy path, dispatch, and rollback machinery are all reused, not rebuilt.
