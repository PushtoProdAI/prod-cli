# Road to 1.0 — reviewed implementation plans

These three plans cover the **buildable-now, no-secrets-required** work that makes prod's 1.0
claims *true and proven*. Each was drafted from a grounded code investigation and then
**adversarially reviewed** (a red-team pass that verified every claim against the source); the
findings are folded into a "REVISIONS FROM ADVERSARIAL REVIEW" section at the top of each plan.
Nothing here is implemented yet — these are the approved-before-coding artifacts.

| Plan | Theme | Net effort (post-review) |
|------|-------|--------------------------|
| [C — Backend cleanup / trust signal](./v1-backend-cleanup-plan.md) | Make "no backend, your creds only" obviously true in the running product + the source | **Small.** C1/C2 tiny + safe; C3 re-scoped to delete-by-symbol after removing a dead harness |
| [A — Universal reversibility](./v1-reversibility-plan.md) | Destroy on Netlify/Vercel + rollback on AWS App Runner (Modal documented) | Netlify/Vercel destroy **small**; AWS rollback **medium** (right-sized up by review) |
| [B — Orphan hygiene](./v1-orphan-hygiene-plan.md) | Stop a failed deploy from leaving billed backing DBs / registries | B1/B2 **cheap**; B3 destroy-cascade is the **big** item (three prerequisites surfaced by review) |

## What the reviews changed (why this order)
- **C is the cheapest, highest-signal, lowest-risk** — do it first. C1 alone removes a "could not
  fetch base images from backend" log that fires on *every* local deploy and contradicts the core
  pitch. The review caught that C3's original line-range deletion would have broken every container
  deploy — now delete-by-symbol only, after removing the zero-caller `scratchpad/render_live.go`.
- **A-destroy (Netlify + Vercel) is small and sound** — quick reversibility wins. **A-rollback
  (AWS)** is real, self-contained work: it must share the deploy closure (no exported
  `UpdateService`), plumb the history store into the deployable, skip the current image when
  picking a rollback target, and filter by region+account.
- **B1 (idempotent Postgres, provenance-safe) is cheap; B3 (destroy cascade) is the largest single
  item** — it needs history→destroy plumbing, a `--delete-data` opt-in transport, and new per-cloud
  delete methods, plus a go-workflows activity restructuring for B2. Treat B3 as its own milestone.

## Recommended implementation sequence
1. **C1 + C2** (backend log guard + dead detector field) — tiny, immediate trust win.
2. **A: Netlify destroy**, then **Vercel destroy** (verify the `vercel project rm` verb first).
3. **B1** (idempotent Postgres creates, prod-created-only reuse) — independent, cheap.
4. **A: AWS App Runner rollback** — the medium build; enables both manual + auto rollback.
5. **C3** (delete the dead backend-registry cluster, by symbol, after removing the harness).
6. **B2 → B3** (partial-resource tracking → destroy cascade with loud `--delete-data` opt-in).
7. **One consolidated docs PR, landed last:** reconcile the guides + `CLAUDE.md` (AWS rollback
   now real; Netlify/Vercel destroy now supported; Modal carve-outs) and soften the
   `ROADMAP.md` "resumable workflows" line. Consolidated to avoid rebase conflicts.

## Deliberately NOT in these plans
- **F6 smoke harness** — already has a reviewed plan (`f6-smoke-harness-plan.md`); it's the
  live-proof layer and is gated on cloud secrets, not on new design.
- **Modal automated destroy/rollback** — blocked on Modal leaving experimental.
- **Durable workflow resume (`--resume`)** and **full mid-deploy compensation** — legitimately
  post-1.0 (higher risk; addresses process-crash, not the bounded orphan money-gap B closes).
