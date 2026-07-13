# Plan B — Orphan hygiene (close the mid-deploy "money gap")

## REVISIONS FROM ADVERSARIAL REVIEW (B3 grows real prerequisites; B1/B2 tightened)
- **B3's "don't delete BYO data" guard is NOT implementable as written** — destroy has zero
  provenance. `workflow_destroy.go:34-40` builds a fresh spec (only `ExistingProjectID`); it never
  loads the deploy's `metadata["resources_created"]` from history. **Prerequisite: destroy must
  first load the history record and thread its recorded resource list (with type tags) into
  `Destroy`.** Only cascade resources that appear in prod's own history — never an unqualified
  name match.
- **B3 needs a data-deletion opt-in TRANSPORT** — `Destroyer.Destroy(ctx)` takes only a context;
  the MCP `confirm` bool is a single whole-destroy gate. Add `DeleteBackingData bool` on the spec,
  fed by a `--delete-data`/`--keep-data` flag through `DeployPlan`→spec→`Destroy`. Default MUST be
  keep-data (a dropped Postgres is irreversible).
- **B3 category error: Fly Postgres is NOT an app.** It's `flyctl mpg create`
  (`flyctl_client.go:395`); `DestroyApp` (`flyctl apps destroy`) won't remove it. Need new client
  methods `DestroyPostgres` (`flyctl mpg destroy`) + `DestroyRedis` (`flyctl redis destroy`).
- **B3 new delete methods don't exist on Render either** — interface has only `DeleteService`;
  need `DeletePostgres` + `DeleteKeyValue`, **and** a Key-Value LIST (only `GetKeyValue` by id
  exists) for any name fallback. ECR needs `DeleteRepository` on the `Registry` interface
  (`registry/registry.go:68`) — and **GAR (Cloud Run) + ACR (Azure) have the identical image
  orphan**; either scope all three container registries or state the omission.
- **B3 naming-fallback is wrong per cloud:** Render Postgres is named `<name>_db`
  (`render/api_steps.go:55-57`), NOT `<name>-postgres`; Fly uses
  `NormalizeFlyAppName(name)+"-postgres"`. Derive per-cloud exactly or it targets nothing/wrong.
- **B2 "return partials on error" can't cross the go-workflows activity boundary** — an activity
  that returns `(slice, err)` delivers the **zero value** to the workflow
  (`workflow_flyio.go:82`, `workflow_container.go:72`). Real fix: the activity returns `nil` error
  and a **result struct** carrying `{partialResources, serializedFailure}`; the workflow inspects
  the failure field. This is a restructuring, not the additive "D-lite" tweak. (History-layer
  write itself is fine — `Record.Metadata` is `map[string]any`, `Store.Update` merges,
  `store.go:57,124-145` — no schema change.) Must add the resources to **every** `"failed"` write
  branch (`workflow_flyio.go:100,120,228,249`; `workflow_container.go:44,60,81,195`).
- **B1 data-loss footgun: get-or-create can ADOPT the user's pre-existing same-named DB** (names
  are deterministic). Only reuse a resource prod can *prove* it created (a prod tag/label, or
  presence in prod's own history) — never a bare name match. Also: **no Redis existence primitive
  exists** (Fly has `ListPostgres` but no `ListRedis`; Render has `ListPostgres`, no
  `ListKeyValue`), so B1 idempotency ships for **Postgres first**; Redis needs a new list method.
- **Deferring C (compensation) + E (durable resume) is still right, but state the gap honestly:**
  the in-memory workflow backend means a hard crash between create-DB and the activity return
  never records the resource → B2 can't see it and B3's only recourse is the unreliable
  naming-fallback. Also soften `ROADMAP.md:79`'s "resumable workflows" claim (CLAUDE.md already
  hedges correctly).
- **NET RE-SCOPE:** B1 (Postgres, provenance-safe) + B2 (history-layer write) are cheap and sound.
  **B3 is the big item** with three unlisted prerequisites (history→destroy plumbing, the
  data-delete flag, new per-cloud delete methods) + the B2 activity-boundary restructuring. Treat
  B3 as its own milestone, not a quick follow-on.
- **CONFIRMED:** partials discarded on failure; no existence check on backing-DB creates; destroy
  doesn't cascade; history write is schema-flexible. Nit: Render `CreatePostgres` submits
  `DatabaseName` as the service name and ignores the step `Name` (`render/api_steps.go:55-57`) —
  fix alongside B1/B3 so create and cascade agree on the name.

---


**Goal:** stop a failed multi-step deploy from silently leaving **billed** backing resources
the user can't easily find or remove. This is the devops persona's literal fear. Scope is the
**bounded, high-value** cut — NOT durable resume (deliberately post-1.0).

**Grounding (verified on `main`):**
- Durable resume is deliberately OFF: `cmd/main.go:51-60` builds `WorkflowsConfig` with no
  `SQLitePath` → in-memory backend (`workflowext.go:57-99`); comment says never auto-resume
  (avoids the double-deploy footgun). So a process crash mid-deploy loses all in-flight state.
  **We are NOT changing this** — resume re-runs half-succeeded creates and reintroduces that
  footgun; it addresses crashes, not orphans.
- Backing resources are created **before** the main service and orphan on later failure:
  - Fly: Managed Postgres (`flyctl mpg create`, separately billed — `flyctl_client.go:395`) +
    Upstash Redis, created at `flyio/queued.go:155-173` before the app.
  - Render: Postgres + Key-Value waited-to-`available` before the web service
    (`render/queued.go:69`, `api_steps.go:54-73,198-213`).
  - AWS/Cloud Run/Azure: ECR repo + pushed image always created first
    (`managedcontainer.go:60-69`); on `WaitForRunning` timeout the service is left running
    (`aws/apprunner_deploy.go:85-90`).
- On failure the partial `CreatedResource` list is **discarded**: `deploySteps` returns an empty
  slice on any error (`agent/deployment.go:46,54,58`); Render's executor returns the partial list
  but the caller throws it away (`step_executor.go:105`). So history records `status:failed` with
  **zero resource IDs**.
- `prod destroy` works by **name** on the main service (Fly `DestroyApp(name)`
  `flyio/queued.go:544-551`, Render name-fallback `render/queued.go:497-517`, AWS
  `Delete(Sanitize(name))`) — but **does NOT cascade** the backing DBs/ECR (Render explicitly
  documents this `render/queued.go:491-496`). So the exact orphans survive destroy.
- No `prod cleanup`/gc command exists.
- Retry-multiplication: `ActivityOpts.MaxAttempts:10` (`workflow.go:42-49`) re-runs `Deploy` from
  step 1 on a transient error; `CreatePostgres`/`CreateRedis` have **no existence check**
  (Fly `api_steps.go:210-260`, Render `api_steps.go:54,198`) → can duplicate a DB or collide.
- Strongest existing mitigation: `detectExisting` (`workflow.go:236-252`) makes a **user re-run**
  mostly idempotent (reuses app + detected DBs). Only helps across separate invocations.

---

## The 1.0 cut (do these; they close the confirmed money gap)

### B1 — Make `CreatePostgres`/`CreateRedis` idempotent-by-name (**cheap, ~0.5-1d**)
Add a "get-or-create by name" existence check to the Fly (`api_steps.go:210`) and Render
(`api_steps.go:54,198`) backing-DB create steps, mirroring `CreateFlyioAppStep` which already
does `GetApp` first (`flyio/api_steps.go:35-44`). Defangs the retry-multiplication path so a
transient failure + activity retry can't create a second DB. **Highest value/effort ratio; do
first.** Independent of everything else.

### B2 — Track backing-resource IDs in history (**enables B3**)
Today `resources_created` is written only on success (`workflow_flyio.go:285`,
`workflow_container.go:210-217`). Two parts:
1. Ensure the **backing** resources (Fly `-postgres`/Redis app names, Render Postgres/KV ids,
   ECR repo name) are captured as `CreatedResource`s with a stable type tag, not just the main
   service.
2. **D-lite:** on failure, persist the *partial* `CreatedResource` list instead of discarding it
   — change `deploySteps` (`agent/deployment.go:34-62`) to return the partial slice on error and
   record it into the failed history entry. Render's executor already returns it
   (`step_executor.go:105`); Fly/managed need the partial list threaded out of their `Deploy`
   loops (`flyio/queued.go:36-60`, `managedcontainer.go:59-76`).
   *(Adversarial: this must not change the success path or the error returned; it only adds the
   partial list to the failed record.)*

### B3 — Make `destroy` cascade backing resources (**the money-gap fix; ~2-4d**)
Extend each `Destroy` to also remove the backing resources recorded in B2 (or derived by naming
convention as a fallback, e.g. Fly `<name>-postgres`):
- Fly: destroy the `-postgres` Managed Postgres app + Redis after the main app.
- Render: delete the Postgres + Key-Value instances (needs `DeletePostgres`/`DeleteKeyValue`
  client methods — check whether stubs exist; Render `Destroy` currently deletes only the
  service).
- AWS: delete the ECR repo (`registry/ecr.go` — add a `DeleteRepository`) after the service.
- **Guard rails (adversarial):** never delete a resource the user **pre-existed / brought their
  own** — only cascade resources prod created (use the B2 record / `IsUpdate`/`ExistingDatabases`
  from `detectExisting`). A `--keep-data`/confirmation for DB deletion is worth considering since
  dropping a Postgres is irreversible data loss. Destroy of a DB should be **opt-in-loud**, not
  silent.
- Reconcile the guides + `render/queued.go:491-496` comment once cascade lands (they currently
  say backing DBs are NOT cascaded).

### B4 — Document honestly (**~0.5d; do regardless**)
Add a Troubleshooting guide section: a failed multi-step deploy can leave a backing DB / ECR
repo; how to find it (dashboard), that re-running repairs the main app, and (post-B3) that
destroy now cascades prod-created DBs. Fold into the queued "Troubleshooting" guide.

---

## Explicitly OUT of scope for 1.0 (document as known, revisit later)
- **C. Full cleanup-on-failure / compensation** (real step `Rollback` bodies — today no-op in
  Render `api_steps.go` / "not implemented" in Fly `api_steps.go:198,304,476`): higher risk
  (deleting mid-deploy must respect pre-existing resources), ~4-8d. B1+B2+B3 cover the recovery
  path more safely.
- **E. Durable resume (`--resume`, flip `SQLitePath`)**: ~1-2wk + reintroduces the double-deploy
  footgun; addresses process-crash, not orphans. Not a 1.0 item.
- A standalone `prod cleanup` command: nice UX but B3 (destroy cascades) covers the money gap;
  add later if the recovery flow needs a dedicated entrypoint.

## Sequencing / PRs
- PR1: B1 idempotent DB creates (cheap, independent, immediate value).
- PR2: B2 partial-resource tracking (enables B3; low risk — additive to the failed record).
- PR3: B3 destroy cascade (the money-gap fix; gated on B2). **Loud opt-in for DB deletion.**
- B4 docs ride along.
- Each: `make check` from root, CI green, squash-merge. Live proof via F6.

## Interaction with Plan A
- B3's destroy-cascade extends the A1/A2 destroys (Netlify/Vercel) and the existing Render/Fly/AWS
  destroys. Land Plan A's service-level destroys first, then B3 layers cascade on top. No conflict
  (different concern), but same files (`*/queued.go` Destroy) → sequence A before B3 to avoid churn.
