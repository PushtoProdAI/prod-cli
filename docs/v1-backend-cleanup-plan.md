# Plan C — Backend cleanup / trust signal (#2, reframed)

## REVISIONS FROM ADVERSARIAL REVIEW (C1/C2 safe; C3 was UNSAFE — re-scoped)
- **C3 BLOCKER: the line range `868-1048` includes LIVE, load-bearing methods.**
  `BuildAndPushToRegistry` (`docker.go:949-972`) is the backend-free push used by **every**
  managed-container cloud (`managedcontainer.go:66`) + Render (`render/api_steps.go:381`); also
  `PushToUserRegistry` (`:886`) and `pushImage` (`:895`). **Never delete by line range — delete by
  exact symbol name with an explicit KEEP list.**
- **C3 BLOCKER: "no live caller" is FALSE.** `BuildAndPush` (`docker.go:988`) has a live non-test
  caller: `internal/scratchpad/render_live.go:232`. That keeps the whole backend-registry
  phone-home subgraph compiled. BUT its container `LiveTestRenderDeployment`
  (`render_live.go:18`) has **zero callers** — it's dead dev scaffolding. **Fix: delete
  `internal/scratchpad/render_live.go` FIRST; then the backend-registry cluster becomes genuinely
  dead and deletable.** (The whole `scratchpad` package is that one file.)
- **C3 safe-to-delete set (verified zero live callers) after `render_live.go` is gone:**
  `GetPushCredentials` (867), `GetPullCredentials` (974), `getPullCredentialsWithLocation` (979),
  `BuildAndPushExternal` (992), `CreateDockerRepository` (1031), `CreateDockerRepositoryExternal`
  (1036), `createDockerRepositoryWithLocation` (1041) — PLUS `BuildAndPush`,
  `buildAndPushWithLocation`, `getPushCredentialsWithLocation`, `PushToRegistry` once the harness
  is deleted. **KEEP** `PushToUserRegistry`, `pushImage`, `BuildAndPushToRegistry`. The
  `*RegistryCredentials*` methods are on `backend.Client` (`internal/backend/docker.go`) — KEEP
  (managed mode).
- **C1 correction: the doomed GET is in `initTemplates` (`docker.go:132`, block `174-184`), gated
  on `if dg.beClient != nil`, NOT in a func named `GetBaseDockerImages`** (that's
  `internal/backend/docker.go:191`). The fix is correct — add `&& config.BackendConfigured()` to
  the `docker.go:175` guard — just edit the right line. Confirmed behavior-preserving (failed GET
  and skip both yield the same empty map; managed mode still fetches; no import cycle).
- **C2: also drop the now-unused `backend` import** at `project_detectors.go:10` (only uses are
  the two dead lines 345/350).
- **C4: "deploy logging is local" is only true for LOCAL mode** (managed still logs via
  `planning.go:447`). Word the CLAUDE.md edit as "*local mode* is backend-free"; preserve the
  ROADMAP target-vs-current caveat. Sequence the CLAUDE.md hunk with Plan A's (one PR owns it).
- **CONFIRMED SAFE:** `config.BackendConfigured()` semantic (`config.go:52-54`); C1 no import
  cycle + behavior-preserving; C2 detector genuinely dead (`beClient` never read, not in the
  `ProjectDetector` interface, only caller `platforms.go:227`, no test refs); keep `internal/
  backend` (managed mode uses it); `beClient` always non-nil (no panic path); no test asserts the
  removed behavior.

---


**Goal:** make the "no backend, your creds only, local state" pitch *obviously* true — in the
running product AND in the source a skeptical engineer reads. The investigation found AWS (and
the rest of local mode) is **already backend-free at runtime**; this is cleanup + killing one
misleading signal, not a functional fix. **Keep `internal/backend` — it's the real substrate of
the opt-in managed tier.**

**Grounding (verified on `main`):**
- AWS deploy path touches the backend **zero times** on the critical path; all mechanics run on
  the user's `~/.aws` SDK creds (`aws/apprunner_deploy.go:1-5` docstring: "no backend, no
  CloudFormation, no central account"). ECR (`registry/ecr.go`) + Secrets Manager
  (`apprunner/secrets.go`) are direct SDK.
- Three residual couplings, none functional:
  1. **Misleading log (the one that matters):** `GetBaseDockerImages` (`docker.go:175-183`) does
     an **unconditional** GET; in local mode the base URL is relative so it fails and logs
     *"could not fetch base images from backend, using defaults"* — on **every** deploy across
     all container clouds + Fly/Render. A "backend" error line on every local deploy directly
     contradicts the pitch.
  2. **Dead field:** `AWSProjectDetector` stores `beClient` but `DetectExistingProject` is a
     no-op ignoring all args (`project_detectors.go:343-362`). Nothing to replace — it's dead.
  3. **Dead legacy registry cluster:** `docker.go:~868-1048`
     (`Get*RegistryCredentials*`/`BuildAndPushExternal`/`CreateDockerRepository*`) only call each
     other — no live caller (grep-verified). Residue of the old backend-brokered-ECR design,
     superseded by direct-SDK `registry.NewECR`.
- Managed mode legitimately still uses the backend cross-platform: `planning.go:368/447/478`
  (guarded by `!config.BackendConfigured()`), `slash_commands.go:131` (`/history`). `beClient` is
  always constructed (`cmd/main.go:88`), never nil → no panic path.

---

## Changes

### C1 — Gate the base-images fetch on `BackendConfigured()` (**the trust-signal fix; tiny**)
In `GetBaseDockerImages` (`docker.go:175`), return the template defaults immediately when
`!config.BackendConfigured()` instead of attempting the doomed GET. Kills the misleading
"backend" log on every local deploy. **Highest symbolic value; ~15 min + a test.**
- *Adversarial:* confirm managed mode still fetches (guard on `BackendConfigured()`, not on a
  hard-coded local flag); confirm the defaults path is exactly what the failed-GET fallback
  already uses (so behavior is unchanged except the wasted call + log).

### C2 — Remove dead `beClient` from the AWS detector
Drop the `beClient` field/param from `AWSProjectDetector`/`NewAWSProjectDetector`
(`project_detectors.go:344-355`) and update the one caller (`platforms.go:227`). Zero behavior
change (it's dead). Makes "AWS uses your creds, not a backend" legible in the source.

### C3 — Delete the dead legacy backend-registry cluster in `docker.go`
Remove `Get*RegistryCredentials*` / `BuildAndPushExternal` / `CreateDockerRepository*`
(~`docker.go:868-1048`) after **re-confirming no live caller** (grep for each symbol in `cmd/`
+ `internal/`, excluding self-references and tests). Reduces the "does prod phone home?" surface.
- *Adversarial:* this is the riskiest of the three (deleting code). Do it as its own commit; if
  any symbol has a non-test caller, leave it and just annotate as dead. `make check` must pass.

### C4 — (optional) Doc/comment tidy
`CLAUDE.md`'s "the code today still routes through a Supabase backend for auth, LLM, and deploy
logging" is now overstated for local mode (LLM is direct per the earlier finding; deploy logging
is local; auth is per-cloud). Tighten it to reflect that local mode is backend-free and the
backend is managed-tier-only. *(Coordinate with the Plan A CLAUDE.md edit so they don't collide.)*

---

## Sequencing / PRs
- PR1: C1 (base-images guard) + C2 (dead detector field) — small, safe, high signal. Ship first.
- PR2: C3 (delete dead legacy cluster) — separate commit, extra grep verification.
- C4 folds into whichever PR touches CLAUDE.md (coordinate with Plan A).
- Each: `make check` from root, CI green, squash-merge.

## Out of scope
- Removing `internal/backend` or the `beClient` field from `Activities`/`NewWorkflows` — it's
  still needed for managed mode. This plan makes local mode *clean*, not backend-less-everywhere.
- Any managed-mode change.

## Value framing
C1 alone is worth doing immediately regardless of the rest: it removes a line of output that
actively contradicts the product's core claim on every single local deploy. Cheapest credibility
win on the whole 1.0 list.
