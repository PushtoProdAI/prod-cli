# F6 — Credentialed Nightly Smoke Harness — Implementation Plan

> One scheduled, credentialed CI job that deploys prod's real artifacts to real clouds and
> asserts liveness + shape. It is the **end-to-end backstop**, not the merge gate. Hermetic
> `go test ./...` + `make check` stay the per-PR gate (`.github/workflows/ci.yml:13-47`).

## REVISIONS FROM ADVERSARIAL REVIEW (must land before executing)
The review verified every claim against code; fork/secret safety and the external-oracle premise
are **confirmed sound**. Required fixes:

- **[BLOCKER B1] Render has no `Destroy` — the Render leg orphans a billable worker every night.**
  `prod destroy … on render` fails (Render doesn't implement `deployment.Destroyer`,
  `agent/deployment.go:242-246`; only heroku/aca/gcprun/flyio/aws do), and the `prod-deploy`
  action swallows the error with `|| true` (`action.yml:77`) and reports `status=destroyed`
  falsely. **Fix (pick one before any live run):** (a) implement `Destroy` on the Render adapter
  — it's a real product gap (`prod destroy` is broken for Render users too) — OR (b) make the
  Render sweep the *primary* teardown via the Render REST API (`GET /v1/services?name=smoke-…` →
  `DELETE /v1/services/{id}`), documented as primary not backup, and don't rely on `prod destroy`
  for Render in the cost model. **Recommendation: (a)** — it fixes a genuine gap and makes the
  harness's teardown uniform.
- **[BLOCKER B2] The LLM key never reaches `prod run`'s process env → intent parse falls to
  Ollama → every deploy fails.** Direct-LLM reads `OPENAI_API_KEY`/`ANTHROPIC_API_KEY` from the
  process env (`llm/client.go:122-135`), but the action passes the key only as a `--env` *app*
  flag (`action.yml:82-85`), not into the `prod run` step env (`action.yml:65-70`). **Fix:** set
  `OPENAI_API_KEY`/`ANTHROPIC_API_KEY` as a **job-level `env:`** in `smoke.yml` (propagates into
  composite-action run steps) IN ADDITION to the `--env` app line. Correct §3: the two uses are
  distinct (prod's own parse vs. the app's runtime env).
- **[SHOULD-FIX S1] Fly provisions Postgres/Redis as separate apps `<name>-postgres`/`-redis`
  that `prod destroy` does NOT remove** (`flyio/queued.go:257-278, 544-551`). **Fix:** keep ALL
  smoke fixtures DB-free (verified: no template `prod.template.yaml` declares a backing service —
  assert this in review) AND extend the Fly sweep to also match `smoke-*-postgres`/`-redis`. Drop
  the AC "releases the app AND Postgres/Redis" claim unless `prod destroy` is made to cascade.
- **[SHOULD-FIX S2] "Test tip-of-main" conflicts with "reuse the action verbatim."**
  `scripts/install.sh` installs only *release tags* (`install.sh:108-125`) and the action
  unconditionally self-installs (`action.yml:49-57`). **Decision to settle before Slice 1:** F6
  tests the **last release** (simplest; slightly weaker premise) OR add a build/main-install step
  AND modify the action to skip its self-install (a real action change). **Recommendation:** start
  with last-release (a nightly against the shipped binary is still valuable); add a tip-of-main
  channel later if regressions slip between releases.
- **[SHOULD-FIX S3] The worker "no false rollback" oracle isn't externally observable** — no
  rollback field on `deployment_complete`, and a worker's `verifyLiveness` returns nil immediately
  (`monitoring.go:109-111`) so it can't trigger a rollback anyway. **Fix:** state the worker
  oracle honestly as `url=="" && status=="success"`, OR make it truly external via the Fly
  Machines API (`GET /apps/<name>/machines` → exactly one running machine, no extra release).
- **[SHOULD-FIX S4] The sweep must be 100% platform-native** — `prod ls` reads only local
  `~/.prod/history.json` (empty in CI) with no name-prefix filter (`cmd/deploys/deploys.go:24-30`).
  The plan's `flyctl apps list` + Render API sweep is right; ensure the standalone `sweep` job
  installs `flyctl` explicitly (the action only pulls it in during a Fly *deploy*).
- **[CONSIDER] C1** Tier B's worker-on-container-cloud redirect is a spec test for UNSHIPPED
  behavior (no such guard in `planning.go` today) — keep it deferred, don't frame it as a 2B
  regression guard. **C2** Tier C is genuinely deployable (all 5 analyzers + Dockerfile mappings
  exist, `docker.go:196-202`) but authoring 5 real `$PORT`-binding apps + getting each generated
  Dockerfile to `docker build`+serve is ~1 debugging session per language — size that slice
  accordingly. **C4** After B1, the runner-death gap is ~cents on Fly; the "few $/month" estimate
  is only credible once Render teardown (B1) is fixed.

**Net:** start Slice 1 (`agent-worker → Fly`) only after B2 (LLM key at job env) and S1 (DB-free
fixtures) are settled; do B1 (Render `Destroy`) before adding any Render leg.

---

## 0. Why F6 exists (the problem it solves)

Nearly every differentiator acceptance criterion in the roadmap is phrased "deploys end-to-end
with the correct shape" (`docs/dx-roadmap.md:39`, `:112-124`, 2A `:` AC "each template scaffolds →
builds locally → deploys end-to-end with the correct shape", 2B AC "a real `agent-worker` deploy
to Fly and Render runs with no HTTP probe and no false rollback"). None of these are verifiable in
a hermetic unit test. The CLI *contains* the shape-aware liveness logic
(`cli/internal/agent/monitoring.go:107-117` `verifyLiveness`), but that logic runs *inside* the
deploy — a bug there both mis-deploys **and** mis-reports. F6 adds an **independent external
oracle**: after prod says "success", the CI job itself re-probes the URL / handshake / no-URL
claim, so a false positive in prod's own liveness check is still caught.

The roadmap flags F6 as "the real long pole" (`docs/dx-roadmap.md:23`, `:309-312`) and the
critical dependency for 2A/2B/3A/3B. It depends on F3 (the Linux release binary — **done**;
`install.sh` supports linux, `release.yml:build-linux`) and the `prod-deploy` composite action
(**done**; `.github/actions/prod-deploy/action.yml`).

---

## 1. Scope — what gets smoke-deployed

Three independent tiers, ordered by value and by how hard they are to keep cheap/green. Each row
is: **scaffold (or fixture) → deploy via `prod-deploy` action → external assert → destroy**.

### Tier A — the 5 `prod new` templates (shape-correctness proof; highest value)

Source: `cli/cmd/new/templates/{agent-worker,mcp-server,fastapi,go-api,nextjs}`, each carrying a
`prod.template.yaml` declaring `shape` + `suggestedPlatforms` (read all five — grounded below).

| Template | Shape (`prod.template.yaml`) | Smoke platform (v1) | External assertion (the oracle) |
|---|---|---|---|
| `fastapi` | `web` (fly/render/cloudrun) | **Fly** | `curl -fsS $url` returns 2xx/3xx/401/403 (mirrors `isURLLive`, `monitoring.go:119-144`) |
| `go-api` | `web` (fly/cloudrun/apprunner) | **Fly** | same URL probe |
| `nextjs` | `web` (vercel/netlify/fly) | **Fly** (or Vercel — see Open Decisions) | same URL probe |
| `mcp-server` | `mcp-server` (node) | **Fly** | POST JSON-RPC `initialize` to `$url`, assert a `serverInfo`/`capabilities`/`protocolVersion` frame comes back (mirrors `isMCPServerLive`, `monitoring.go:150-185`) |
| `agent-worker` | `worker` (python; fly/render/modal) | **Fly + Render** | assert `deployment_complete.url` is **empty** AND `status == success` AND the app was **not** rolled back (no false auto-rollback) |

The `agent-worker` on **both Fly and Render** is the load-bearing 2B assertion
(`docs/dx-roadmap.md:2B AC`): Fly emits `[processes]` with no `[http_service]`, Render emits
`background_worker`; liveness must skip the HTTP probe (`monitoring.go:108-111`). Deploying to both
proves the worker artifact branch on both adapters.

### Tier B — shape edge cases (small, high-signal)

- **worker-on-container-cloud redirect** (2B AC "worker-on-container-cloud gives the redirect
  message"): attempt `agent-worker` → an App Runner / Cloud Run target and assert prod returns the
  "use Fly/Render/Modal" message and **does not** create a half-built service. This is a
  *negative* test — assert a clean failure, no orphan. Cheap; no billable resource if it fails
  fast.
- **(optional) GPU agent → Modal** (2B "GPU agent → Modal", removes the `Experimental` flag).
  Modal bills GPU-seconds; gate this behind a separate `include-modal` dispatch input and run it
  **weekly**, not nightly (see Cost + Open Decisions).

### Tier C — the 5 new-language Dockerfiles actually build + run (proves F4 language work)

The Ruby/Rust/Java/C#/Elixir Dockerfiles (`cli/internal/deployment/templates/{ruby,rust,java,csharp,elixir}.dockerfile`,
embedded via `//go:embed` at `cli/internal/deployment/docker.go:88`) today are **only unit-tested
as template strings** (`docker_test.go`). A template that renders correctly can still fail
`docker build` (bad base image tag, missing build stage) or fail to bind `$PORT` at runtime. Tier C
deploys **one minimal real app per new language** to Fly to prove the generated Dockerfile builds
and the container serves.

| Language | Minimal fixture | Assertion |
|---|---|---|
| Ruby | tiny Sinatra/rackup app binding `$PORT` | URL probe |
| Rust | tiny axum/hyper "hello" on `$PORT` | URL probe |
| Java | minimal Spring Boot / plain HTTP on `$PORT` | URL probe |
| C# | minimal ASP.NET minimal-API on `$PORT` | URL probe |
| Elixir | minimal Plug/Bandit on `$PORT` | URL probe |

Fixtures live in a new `test/smoke/fixtures/<lang>/` dir (committed, tiny — a single web handler
each). These are web-shaped, so the oracle is the same `curl` probe. Node/Python/Go are already
covered by Tier A templates, so Tier C is exactly the 5 *new* languages.

### Matrix size + per-run cost

- Tier A: 5 templates, but `agent-worker` runs ×2 platforms ⇒ **6 deploy legs**.
- Tier B: 1 negative test (no billable), Modal excluded from nightly ⇒ **0–1 billable**.
- Tier C: **5 legs**.
- **Nightly total ≈ 11 billable deploy legs, all on Fly + 1 Render.** Each app is scaled to the
  smallest tier, lives < ~5 min (deploy → assert → destroy), so at Fly's shared-cpu-1x/256MB
  the metered runtime is minutes/day. **Expected cost: a few dollars/month** dominated by the
  Render worker's minimum billing granularity and any leg that hangs before teardown. Modal
  (weekly, GPU) is the only line item that could spike — keep it opt-in.

---

## 2. The workflow — `.github/workflows/smoke.yml` (new file)

### Triggers

```
on:
  schedule:
    - cron: "0 7 * * *"      # nightly, 07:00 UTC (off-peak; adjust)
  workflow_dispatch:          # manual, with inputs
    inputs:
      tier:          # all | templates | languages | edge   (default all)
      include-modal: # boolean, default false (GPU cost gate)
```

`schedule` and `workflow_dispatch` **never run on forks** — scheduled workflows only run on the
default branch of the repo that owns them, and `workflow_dispatch` requires write access. This is
the primary reason F6 can safely hold real cloud credentials (see §3).

### Concurrency + permissions

```
concurrency:
  group: smoke                 # one smoke run at a time — caps concurrent live apps
  cancel-in-progress: false    # DON'T cancel: a cancelled run skips teardown → orphans
permissions:
  contents: read
  issues: write                # for the failure-issue alert step (§5)
```

`cancel-in-progress: false` is deliberate and the opposite of `ci.yml:9-11` — cancelling a smoke
run mid-deploy would strand a billable app before its teardown step. Serialize instead.

### Job structure

**Job 1 — `smoke` (matrix over `{template/fixture × platform}`)**

- `runs-on: ubuntu-latest` (the F3 Linux binary; `install.sh` handles linux).
- `strategy: fail-fast: false` so one flaky leg doesn't mask the rest.
- Matrix entries carry: `name` (the fixture/template), `platform`, `shape`, `scaffold` (either
  `prod new <t>` for Tier A or "copy fixture dir" for Tier C), `assert` (web|mcp|worker).
- Per-leg env: a **unique app name** `smoke-<name>-<platform>-${{ github.run_id }}` — the
  `run_id` prefix is what the safety-net sweep (§4) keys on, and keeps parallel legs from
  colliding on Fly's global namespace (relevant once F5's no-auto-suffix lands,
  `docs/dx-roadmap.md:F5`).

Per-leg steps:
1. **Checkout** (`actions/checkout@v4`).
2. **Scaffold**: Tier A → install prod (the action does this) then `prod new <template> app`
   into `working-directory`; Tier C → `cp -r test/smoke/fixtures/<lang> app`. *(Note: `prod new`
   is invoked by a pre-step here; the `prod-deploy` action installs prod, so ordering means we
   install prod first — see "reuse" note below.)*
3. **Deploy** — `uses: ./.github/actions/prod-deploy` with `platform`, `name`, `env`
   (`OPENAI_API_KEY=${{ secrets.OPENAI_API_KEY }}` for the agent/mcp templates),
   `working-directory: app`. The action already runs
   `PROD_JSON_MODE=true prod run --yes --name … --env … -- "deploy…"`, parses
   `deployment_complete`, exposes `url`/`id`/`status`, and fails if `status != success`
   (`action.yml:82-106`).
4. **External assert** (the oracle — the part the action does *not* do): a `shape-assert`
   step keyed on `matrix.assert`, using the action's `steps.deploy.outputs.url`:
   - `web`: `curl -fsS --retry 5 --retry-delay 5 --max-time 20 "$url"` and require a
     non-5xx (mirror the `<500` rule at `monitoring.go:139-141`, but as an *independent* check).
   - `mcp`: `curl` POST the exact `initialize` JSON-RPC body from `monitoring.go:152` and grep
     the response for `serverInfo|capabilities|protocolVersion` (mirror
     `mcpInitializeOK`, `monitoring.go:217-229`).
   - `worker`: assert `steps.deploy.outputs.url` is **empty** and `status == success`. Optionally
     query the Fly/Render API to confirm the machine is in a running/started state and that no
     rollback event fired.
5. **Teardown** — `if: always()`: `uses: ./.github/actions/prod-deploy` with `action: destroy`,
   same `name` + `platform`. The action's destroy path is best-effort
   (`prod run … "destroy this on $PLATFORM" … || true`, `action.yml:75-80`) so a missing app
   never fails the teardown.

**Reuse note / one wrinkle:** the `prod-deploy` action installs prod *inside itself*
(`action.yml:49-57`), but Tier A needs `prod new` to run *before* the deploy. Two clean options:
(a) add a top-of-job step that runs `install.sh` once and `prod new`, then the action's
re-install is a fast no-op; or (b) add an optional `scaffold` input to the action. **Recommend
(a)** — keep the action single-purpose (deploy/destroy), do scaffolding in the workflow. Do **not**
fork the action's deploy logic; reuse it verbatim so F6 exercises the exact same 3B path users
get.

**Job 2 — `sweep` (safety net, always runs last)** — see §4.

**Job 3 — `alert` (on failure)** — see §5.

---

## 3. Credentials & security (the critical, adversarial part)

This job holds **live cloud credentials that can create billable infrastructure**. Treat it as the
highest-risk surface in the repo.

### Secrets needed (GitHub Actions repo/environment secrets)

| Secret | Used by | Scope guidance |
|---|---|---|
| `FLY_API_TOKEN` | every Fly leg | **Dedicated Fly org token**, org-scoped to a throwaway `prod-smoke` org — NOT a personal deploy token with access to real apps |
| `RENDER_API_KEY` | `agent-worker` on Render | A key on a **dedicated Render team/workspace** used only for smoke |
| `OPENAI_API_KEY` (or `ANTHROPIC_API_KEY`) | prod's own LLM parse (`prod run` needs it to plan the deploy) + injected into agent/mcp app env | A **low-limit key with a hard monthly cap**; smoke needs only a handful of parses/night |
| `MODAL_TOKEN_ID` / `MODAL_TOKEN_SECRET` | Modal leg (weekly/opt-in only) | Dedicated Modal workspace; omit from nightly |
| `SMOKE_ALERT_SLACK_WEBHOOK` (optional) | alert job | Only if Slack alerting chosen (§5) |

Container-cloud legs (App Runner/Cloud Run/ACR) would additionally need per-cloud registry creds;
**v1 deliberately targets Fly + Render only** to avoid AWS/GCP creds in CI. The Tier B
worker-on-container-cloud test is a *negative* test that should fail before authenticating, so it
needs no real cloud creds (or a stub) — verify this during implementation.

### Hard rules

- **Dedicated throwaway accounts/orgs**, isolated from any production tenant. A bug or a leaked
  token then blast-radius-limits to disposable infra. This matches CLAUDE.md §7 "a dedicated
  throwaway/CI account or org so smoke apps are isolated from production."
- **Least privilege**: Fly org token scoped to the smoke org only; OpenAI key with a spend cap;
  no token that can touch a real customer app.
- **Never expose to forks.** `schedule`/`workflow_dispatch` don't run on forks and can't be
  triggered by fork PRs, so `pull_request_target` is **never** used here. State this explicitly in
  a comment at the top of `smoke.yml`. Do not add a `pull_request` trigger.
- **Masking**: pass every secret via `env:` (Actions auto-masks registered secrets in logs). The
  `prod-deploy` action already routes secret env values to the platform's secret store and passes
  inputs as env, never interpolated into the script (`action.yml:63-70`) — reuse that discipline;
  don't `echo` a secret or bake it into an app image.
- **Optionally use a GitHub Environment** named `smoke` holding the secrets, with the schedule/
  dispatch job referencing `environment: smoke` — gives one more scoping boundary and an
  audit/approval hook if desired.
- **Version-pin the binary**: the action installs `version: latest` by default
  (`action.yml:31-33`). For a nightly backstop of `main`, install the tip build (see Open
  Decisions) so smoke tests the code that just merged, not the last release.

---

## 4. Teardown & cost control

Defense in depth — three layers, because a stranded billable app is the worst-case failure:

1. **Per-leg `if: always()` destroy** (§2 step 5). Runs even when the assert fails. Best-effort
   via the action's destroy path.
2. **`concurrency: group: smoke` + `cancel-in-progress: false`** caps live apps to one run's
   matrix at a time and prevents a cancel from skipping teardown.
3. **`sweep` job (safety net), `needs: [smoke]`, `if: always()`**: enumerate apps by the
   `smoke-*` name prefix and destroy any whose run-id is **not** the current run (or any older
   than N hours). Fly: `flyctl apps list` filtered on the prefix → `flyctl apps destroy -y`.
   Render: list services by name prefix → delete. This catches an app orphaned by a runner that
   died between deploy and the `always()` teardown (the one gap layer 1 can't cover). The sweep is
   idempotent and safe to run even on a green night.

Additional controls:
- **Smallest instance tiers** on every leg (Fly `shared-cpu-1x`/256MB; Render smallest worker).
- **Timeouts**: `timeout-minutes` on each leg (e.g. 15) so a hung deploy can't bill indefinitely
  before `always()` fires.
- **Name-prefix convention** `smoke-<name>-<platform>-<run_id>` is the contract the sweep relies
  on — document it in `smoke.yml`.
- **Expected steady-state cost: a few $/month** (§1); the sweep + timeouts bound the tail risk.

---

## 5. Alerting

Keep it simple and reliable — layered:

1. **Baseline (free, zero-config): GitHub's native scheduled-workflow failure email.** GitHub
   emails the workflow file's last committer / repo watchers when a *scheduled* run fails. This
   alone satisfies the "failures alert" AC.
2. **`alert` job, `if: failure()`** that opens **or updates** a single tracking issue
   (`peter-evans/create-issue-from-file` or a `gh issue` call, needs `issues: write`) titled e.g.
   `nightly smoke failed (<date>)`, body = which legs failed + run URL. De-dupe by searching for an
   open `smoke-failure` label so consecutive red nights update one issue instead of spamming.
3. **Optional Slack**: an extra step gated on `if: secrets.SMOKE_ALERT_SLACK_WEBHOOK != ''`
   posting a one-line summary. Only wire this if the webhook secret exists.

**False-alarm hygiene (critical for trust):** a nightly that cries wolf gets ignored. Build in the
`--retry` on the external probes (transient cold-start), `fail-fast: false` so one flake doesn't
red the whole matrix, and consider a "2 consecutive failures before paging" rule for the Slack/
issue layer (email still fires immediately). Distinguish *infra flake* (provider 5xx during
deploy) from *real regression* (shape assertion wrong) in the issue body where possible.

---

## 6. Interaction with the merge gate (explicit)

**F6 is the nightly end-to-end backstop, NOT a per-PR / merge gate.** The merge gate stays the
hermetic, credential-free `.github/workflows/ci.yml` (`test` on ubuntu+macos, `go build`/`go vet`/
`go test ./...`/gofumpt at `ci.yml:35-47`; `lint` advisory; `gitleaks` gated). Rationale, all
load-bearing:

- Live deploys are **slow** (minutes/leg) and **flaky** (provider hiccups) — unacceptable latency
  and false-red rate for a merge gate.
- They **cost money** and hold **real credentials** — must never run on fork PRs, which the merge
  gate must serve.
- Unit tests use the `llm.Client` mock (CLAUDE.md §8) and never hit a cloud; that property is what
  makes the merge gate fast and hermetic. F6 does not change it.

`smoke.yml` therefore has **no `pull_request` trigger** and is invisible to the PR checks list.

---

## 7. What it proves vs. what it doesn't

**Proves** (only F6 can):
- Each `prod new` template scaffolds → deploys → is live **with the correct shape** on a real
  cloud (2A AC).
- `agent-worker` deploys as a worker on Fly **and** Render with no HTTP probe and **no false
  rollback** (2B AC) — asserted independently of prod's own liveness code.
- `mcp-server` is live only after a real MCP `initialize` handshake (2B / ACD.4).
- The 5 new-language Dockerfiles actually `docker build` and serve `$PORT` on a real cloud — the
  gap `docker_test.go` can't cover.
- The end-to-end 3B path (`prod-deploy` action → `prod run --yes --name --env` → parse
  `deployment_complete` → destroy) works against live providers.

**Does NOT prove / does NOT replace:**
- Unit correctness of adapters, analyzers, the FSM — those stay on the merge gate.
- Every platform (v1 = Fly + Render; AWS/GCP/Azure/Vercel/Netlify/Heroku not smoked yet).
- Windows/macOS runtime (CI already builds/tests macOS; smoke runs on Linux only).
- Cost/quota correctness, security of user creds — separate concerns.

F6 is a **backstop**, not a substitute for hermetic tests (CLAUDE.md §8).

---

## 8. Effort, sequencing, dependencies, files, acceptance criteria

### Dependencies (status)
- **F3 Linux binary — DONE.** `install.sh` handles linux (`scripts/install.sh` platform detect);
  `release.yml:build-linux` publishes linux archives.
- **`prod-deploy` action — DONE.** `.github/actions/prod-deploy/action.yml` (deploy + destroy +
  JSON parse). F6 reuses it verbatim.
- **F5 idempotent-by-name** (`docs/dx-roadmap.md:F5`) — *soft* dependency: the `run_id`-suffixed
  unique names sidestep the global-collision issue, but landing F5 first makes retried legs safer.
  Not a hard blocker.
- Tier A needs the 5 templates (**exist**: `cli/cmd/new/templates/*`) and `prod new` (2A).

### Sequencing (recommended)
1. **Slice 1 (proves the moat, ~1 day):** `smoke.yml` with **only** the `agent-worker → Fly` leg
   + external no-URL/no-rollback assert + `always()` teardown + native failure email. This is the
   "minimal credentialed smoke job — the seed of F6" the roadmap calls for
   (`docs/dx-roadmap.md:322`, the recommended first slice).
2. **Slice 2:** add the remaining Tier A legs (web ×3 + mcp-server + agent-worker→Render) and the
   web/mcp external oracles.
3. **Slice 3:** add the `sweep` safety-net job + the issue-alert job.
4. **Slice 4:** add Tier C (5 language fixtures + `test/smoke/fixtures/`).
5. **Slice 5 (later/opt-in):** Tier B negative test; Modal weekly leg.

Total: **~M** (roadmap sizes F6 as M). Slice 1 is a couple of days; full harness ~1 week
including account setup and de-flaking.

### Files to create / change
- **Create** `.github/workflows/smoke.yml` (the harness).
- **Create** `test/smoke/fixtures/{ruby,rust,java,csharp,elixir}/` (minimal `$PORT`-binding web
  apps for Tier C).
- **Optionally add** a `scaffold` input to `.github/actions/prod-deploy/action.yml` (only if the
  "install-once + `prod new`" workflow-level approach proves awkward; prefer **not** to).
- **Optionally add** a `make templates-check` target (`cli/Makefile`) the roadmap references
  (`docs/dx-roadmap.md:2A AC`) — a local dry-run/scaffold-and-build check that smoke.yml can also
  invoke for the non-credentialed portion.
- **Docs:** a short `docs/smoke-harness.md` (or a section) documenting the throwaway accounts,
  secret names, and the `smoke-*` sweep convention.
- **No production Go code changes required** — F6 is pure CI + fixtures, riding existing paths.

### Acceptance criteria (detailed, testable)
1. A `workflow_dispatch` run of `smoke.yml` deploys all Tier A legs to Fly (+ Render worker),
   each reaching `deployment_complete` with `status == success`.
2. The **external** oracle passes independently for each shape: web `curl` non-5xx; mcp-server
   `initialize` handshake returns a valid frame; worker leg confirms **empty URL + success + no
   rollback**.
3. Every leg is **destroyed** afterward (verified: `flyctl apps list` / Render list shows no
   `smoke-*` app from the run after completion), including when an assert **fails**.
4. The `sweep` job removes a deliberately-orphaned `smoke-*` app (test by skipping one teardown).
5. Inducing a real failure (point mcp-server assert at a web template) turns the run red **and**
   fires the alert (native email + tracking issue).
6. The nightly `schedule` trigger runs on `main` only and is confirmed **not** runnable from a
   fork PR.
7. No secret appears unmasked in any log.
8. The PR checks list for a normal code PR is **unchanged** (smoke does not gate merges).
9. Steady-state monthly cloud cost stays within the single-digit-dollar budget (spot-check a
   week of runs).

---

## 9. Open decisions (surface for review)

1. **Dedicated CI cloud accounts?** Strongly recommended (a `prod-smoke` Fly org, a smoke Render
   workspace, a capped LLM key). Confirm who owns/pays and where the tokens are stored. *Blocker
   for going live.*
2. **Which languages to smoke given cost?** All 5 new ones (Ruby/Rust/Java/C#/Elixir) on Fly is
   ~5 cheap legs — recommend **all 5**, since the whole point is they're only template-tested
   today. Could gate the slower-building ones (Java, Rust) behind a weekly cadence if runtime
   minutes matter.
3. **Nightly vs weekly cadence?** Recommend **nightly for Tier A + Tier C** (fast, cheap, high
   signal) and **weekly (or opt-in dispatch) for Modal/GPU**. Consider weekly-only for the
   slowest language builds.
4. **Include Modal?** Only behind the `include-modal` dispatch input / weekly job — GPU-seconds
   are the one real cost risk. Removing the `Experimental` flag (2B) is the payoff; weigh against
   spend.
5. **`nextjs` on Fly or Vercel?** Fly keeps the platform set to Fly+Render (fewer creds); Vercel
   is the template's first `suggestedPlatform` and its native home. Recommend **Fly for v1** to
   avoid a third credential, add Vercel when Track A previews land.
6. **Binary version under test:** `latest` release vs tip-of-`main`. A backstop for merged code
   argues for **tip-of-main** (build the binary in the job or add a `main` install channel);
   `latest` only tests the last release. Recommend tip-of-`main` — decide the install mechanism.
7. **GitHub Environment gating?** Whether to put smoke secrets behind an `environment: smoke`
   (extra scoping/approval) vs plain repo secrets. Recommend the Environment.

---

## Risk register (adversarial view)

- **Real credentials in CI** → dedicated throwaway orgs + least-privilege scoped tokens + capped
  LLM key + never-on-forks (schedule/dispatch only). The single biggest risk; §3 is the mitigation.
- **Orphaned billable resources** → three-layer teardown: `if: always()` per-leg destroy +
  serialize (no cancel) + `smoke-*` prefix sweep job + per-leg timeouts. A runner dying between
  deploy and teardown is the residual gap the sweep exists to close.
- **Flaky live deploys → false alarms erode trust** → external probes with `--retry`,
  `fail-fast: false`, infra-flake vs regression distinction in alerts, and a "2 consecutive fails
  before paging" option on the noisy channels (email still immediate). A nightly nobody trusts is
  worse than none.
