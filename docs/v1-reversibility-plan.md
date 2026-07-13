# Plan A — Universal reversibility (destroy + rollback on every GA cloud)

## REVISIONS FROM ADVERSARIAL REVIEW (apply these; they re-size A4)
- **A4-step3 was wrong: there is NO exported `Deployer.UpdateService`.** `apprunner.go:150-157`
  is a private call inside `Deployer.Deploy`. Rollback must call `Deployer.Deploy(ctx,
  ServiceConfig{...})` and **reconstruct the full `ServiceConfig`** (EnsureAccessRole, the
  port/cpu/mem constants `apprunner_deploy.go:21-23`, `splitEnvVars(spec.EnvVars)`,
  `prodreg.Sanitize(spec.Name)`) + `WaitForRunning`. **Refactor the deploy closure
  (`apprunner_deploy.go:63-95`) so Deploy and Rollback share it, differing only in the image.**
- **A4-step2 BLOCKER: `aws.Deployment{spec,dockerGen,writer}` has no history store**, so
  `GetPreviousDeployment` can't reach `history`. The store is on `Activities.history`
  (`activities.go:65`, may be nil). Plumb `a.history` through the `platforms.go:222-225` factory
  into `NewAppRunnerDeployment` (constructor + all call sites + tests); a nil store must degrade
  to `(nil,nil)`.
- **A4 "latest successful deploy" resolves to the CURRENT image on manual rollback** (the bad
  deploy is already a `success` record, `store.go:152` most-recent-first) → **skip the most-recent
  matching record, return the one before.** The auto-rollback path is different (failing deploy is
  `failed`, not `success`) → there "latest success" is correct. Implement both explicitly.
- **A4 match must include region+account** (both in `Metadata`, `apprunner_deploy.go:93`) or you
  can roll back to the wrong AWS account/region with the same app name.
- **A4 flipping `SupportsRollback:true` also enables AUTO-rollback for App Runner**
  (`workflow_container.go:172`) — the comment at `:166` says "not App Runner" and goes stale.
  First-deploy case is graceful (confirmed `previous==nil` falls through). Update that comment +
  the stale refs at `agent.go:1524, 1755-1756`, and confirm auto-rollback-on-App-Runner is wanted.
- **A3/A5 Modal messages are single generic `Sprintf`s** (`agent/deployment.go:244`,
  `agent.go:1765`), NOT per-platform — reframing for Modal is a **code change** (add per-platform
  branching without changing other platforms' wording), not doc-only.
- **A2 Vercel: only the `prj_` id exists at destroy time** (`.vercel/project.json` has no name,
  `vercel/detection.go:16-19`); `spec.Name` comes from the plan and *usually* matches the created
  project, but `vercel project rm` takes a **name** and may reject the id. Resolve name→ or verify
  `project rm` accepts the id; keep the "verify the CLI verb" gate.
- **A6/S6 sequencing: consolidate ALL doc + shared-message edits into ONE final PR** — otherwise
  every PR touches the same two guides + CLAUDE.md + the shared message funcs and conflicts on
  rebase.
- **CONFIRMED SOUND:** A1 Netlify (`DeleteSite` exists + shells `netlify sites:delete <id>
  --force`; site id available at destroy via detection; idempotency wrapper genuinely needed;
  client is a mockable interface); A2 trap premise; A4 image-ref recording chain
  (`imageRef` param → `Identifiers` → history) and the `targetDeploymentID`=image design; env
  drift characterization; AWS already implements `Destroyer` (scope = rollback only); no
  `CanDestroy` field exists so destroys need **no** `deploytarget` change; `deploytarget.go:100`
  is the right flag flip for AWS rollback. Nit: it's the closure's `imageRef` param, not
  `cfg.ImageRef`; each test defines its own inline mock (like Render's `destroy_test.go`).

---


**Goal:** make "you can cleanly undo it" true on every cloud prod ships as GA. Today
destroy is missing on Vercel/Netlify/Modal and rollback is a stub on AWS App Runner/Modal.
This closes the *service-level* undo (destroy the app; roll back a bad deploy). Backing-DB
cascade is Plan B.

**Grounding (verified on `main`):**
- `Destroyer` iface: `deployment/deployment.go:51-53`; dispatch `agent/deployment.go:235-254`
  (falls to "Teardown isn't supported for <DisplayName> yet" on failed assertion).
- Render is the reference: `render/queued.go:497-528` (`Destroy`, id-then-name resolution),
  `render/client.go:519-544` (`DeleteService`, 200/204/404 idempotent), `render/destroy_test.go`.
- AWS rollback stub: `aws/apprunner_deploy.go:105-107`; `GetPreviousDeployment` stub `:100-102`.
  `SupportsRollback:false` at `platforms.go:221`; `CanRollback=false` at `deploytarget.go:100`.
- Image survives to roll back to: unique per-deploy tag `docker.go:901` (`time.Now().Unix()`),
  no ECR lifecycle policy (`registry/ecr.go:77`) → old tags persist. But the tag is **never
  recorded** in history (`workflow_container.go:205-217` success meta omits it).
- Auto-rollback + manual rollback are already wired and only gated on `SupportsRollback` +
  non-nil `GetPreviousDeployment` (`workflow_container.go:172-191`, `workflow_rollback.go:103-146`).

---

## A1 — Netlify destroy (clean mirror of Render; **low effort**)

- Adapter `NetlifyQueuedDeployment` (`netlify/queued.go:18-31`); client `NetlifyClient`
  (`netlify/types.go:10-27`, CLI-backed).
- Delete API **already exists**: `CLINetlifyClient.DeleteSite(siteID)` →
  `netlify sites:delete <id> --force` (`netlify/client.go:135-148`). Semantically correct
  (deletes the whole site). Only used in the failed-create rollback path today.
- Resource id: `spec.ExistingProjectID` = site id (detector `netlify/detection.go:20`,
  wired `project_detectors.go:237-258`). Optional name fallback via `sites:list`.

**Change:**
1. Add `Destroy(ctx) error` on `*NetlifyQueuedDeployment`: resolve id from
   `spec.ExistingProjectID`, else name-lookup; call `DeleteSite`.
2. Make idempotent: swallow the CLI "not found"/"does not exist" output (mirror Render's 404).
   → needs a small change in `DeleteSite` or a wrapper that inspects the CLI error.
3. `var _ deployment.Destroyer = (*NetlifyQueuedDeployment)(nil)` + mock-based unit test
   (id path, name-fallback path, not-found idempotent).
- No backing DBs → nothing orphaned. Note in a comment.

## A2 — Vercel destroy (**low-med; one real semantic trap**)

- Adapter `VercelQueuedDeployment` (`vercel/queued.go:15-28`); client CLI-backed.
- **Trap:** existing `DeleteProject` shells `vercel remove <arg>` (`vercel/client.go:87-106`),
  which removes **deployments** by name; it works in the failed-create rollback path only
  because that passes the project **name** (`CreateProject` returns `ID: req.Name`). In destroy,
  the id from detection is a `prj_…` **project id** → `vercel remove` will reject/no-op.
- Detector sets `spec.ExistingProjectID` = `prj_…` (`vercel/detection.go:22`, wired
  `project_detectors.go:272-293`).

**Change:**
1. Add a client method `DeleteProjectByName(name)` (or fix semantics) → shell
   `vercel project rm <name> --yes`. **VERIFY the exact subcommand against the installed CLI
   version** before finalizing (adversarial item — the CLI verb is the whole risk here).
2. `Destroy(ctx)`: prefer `spec.Name` with `vercel project rm`; if only a `prj_` id is known,
   resolve name (or accept the id if `project rm` takes it — verify). Idempotent on not-found.
3. Assertion + mock test.
- Vercel-provisioned storage (Postgres/KV) is a separate resource, but this adapter doesn't
  provision DBs (`vercel/adapter.go:183-187`) → low orphan risk. Note it.

## A3 — Modal destroy → **document, don't force** (experimental)

- No client interface, no detector, and the Modal app name lives in the user's Python code
  (`modal.App("name")`), which prod never captures (`modal/modal.go:28-38,52`). There is no
  reliable id to pass to `modal app stop`.
- **Decision:** keep Modal on the "not supported" branch; reframe the dispatch message for
  Modal specifically to name the manual path: *"Destroy isn't automated for Modal — run
  `modal app stop <your-app>`."* Document in the teardown guide. Revisit if/when Modal graduates
  from experimental. (Forcing a brittle output-scraping `Destroy` on an unvalidated adapter is
  net-negative.)

## A4 — AWS App Runner rollback (**med; the real reversibility build**)

Image-swap, exactly the Fly pattern (`flyio/queued.go:465,519`). Dependency order:
1. **Record the image ref.** Add the pushed image ref to the App Runner `DeployResult.Identifiers`
   (`aws/apprunner_deploy.go:91-94`) so it flows to history via `workflow_container.go:211-216`.
   Key it clearly (e.g. `imageRef`). *(Confirm the value is available at that point — it's the
   `cfg.ImageRef`/`PushedImageURL` used by `UpdateService`.)*
2. **`GetPreviousDeployment`** (`aws/apprunner_deploy.go:100-102`): read local history
   (`history.Store.List`) for the previous **successful** AWS deploy of this app (match by
   name/platform), return its recorded `imageRef` as `DeploymentInfo.ID`. If none →
   return `(nil, nil)` (NOT an error) so callers say "nothing to roll back to".
3. **`Rollback`** (`aws/apprunner_deploy.go:105-107`): call the existing
   `apprunner.Deployer.UpdateService` with the previous image (`apprunner.go:150-157`) +
   `WaitForRunning` (`apprunner.go:173`).
4. **Flip flags:** `SupportsRollback:true` (`platforms.go:221`); `CanRollback` + drop the
   "not supported" note (`deploytarget.go:100-104`).

**Adversarial caveats to bake in (pre-surfaced by the investigation):**
- History is the ONLY rollback source. If `~/.prod/history.json` is deleted or the app was
  deployed from another machine, there's no target → must degrade to "nothing to roll back to",
  never a hard error.
- Env/secret drift: a rollback `UpdateService` re-sends **today's** spec env/secrets with the
  **old** image. That's probably desired (roll back code, keep current config) — but state it
  explicitly in the code comment + guide so it isn't a surprise.
- ECR deletion edge: if the target image was manually deleted (or Plan B's future ECR cascade
  removed it), `UpdateService` fails at pull time → surface a clear error.
- First deploy has no previous image → auto-rollback path already handles "no previous version";
  confirm it stays graceful now that `GetPreviousDeployment` is real.

## A5 — Modal rollback → **document-as-unsupported-by-design**

- Source-deployed, no image/version recorded, experimental (`modal/modal.go:78-80`,
  `platforms.go:257`). Reframe the message from "isn't supported **yet**" (implies imminent) to
  *"not applicable — Modal deploys from source; roll back by redeploying your previous version."*
  Keep `SupportsRollback:false`, `CanRollback=false`.

## A6 — Reconcile docs (REQUIRED — or the guides go stale again)
- `docs/guides/tear-down-a-deployment.md`: Netlify + Vercel move to **supported**; Modal note
  updated to the `modal app stop` manual path.
- `docs/guides/roll-back-a-bad-deploy.md`: AWS App Runner moves to **supported (image-swap)**;
  Modal reframed as not-applicable.
- `CLAUDE.md`: fix the "image-swap for AWS" line so it matches reality (now true after A4);
  keep the honest Modal carve-out.

## Sequencing / PRs
- PR1: Netlify destroy (A1) — smallest, proves the pattern.
- PR2: Vercel destroy (A2) — after verifying the CLI verb.
- PR3: AWS rollback (A4) — the meaty one.
- A3/A5/A6 ride along in the relevant PRs (Modal doc + guide reconcile per PR).
- Each PR: `make check` from root, CI green (ubuntu+macos), squash-merge.
- **Live proof** (real Netlify/Vercel/AWS round-trips) is F6's job — unit tests use mocks.

## Out of scope (this plan)
- Backing-DB / ECR cascade on destroy → Plan B.
- Modal automated destroy/rollback (blocked on it leaving experimental).
