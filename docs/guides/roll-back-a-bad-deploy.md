# Roll back a bad deploy

**What you'll accomplish:** revert a deployment to its previous working version — either on demand
("that broke it, roll back") or automatically when a fresh deploy fails its health check. You'll
also learn which platforms support rollback and how they do it, so you know what "roll back" will
actually do on your target.

## Prerequisites

- prod installed with an LLM key and a platform credential — see
  [Configuring your clouds](../clouds.md).
- At least one prior successful deployment on the platform (there has to be a previous version to
  return to). Check with `prod ls`.

## Roll back on demand

Ask in English, or use the MCP `rollback` tool. prod finds the existing deployment, looks up its
previous version, and reverts to it:

```bash
prod "roll back fly"
```

A rollback previews before it acts (and via MCP it's gated behind explicit confirmation):

```
📦 Plan: rollback — revert your-app to its previous release on Fly.io
Proceed? [y/N] y
✅ Rolled back to the previous version
```

`prod ls` marks apps that can be rolled back with a `↩` next to them:

```
NAME                 PLATFORM         STATUS    AGE      URL
your-app             flyio            ✅ ok     5m       https://your-app.fly.dev ↩
```

## How rollback works per platform

There are three mechanisms. Which one you get depends on the platform, and it affects what
"previous version" means and how fast it is.

| Platform | Mechanism | Notes |
|----------|-----------|-------|
| **Heroku** | **native** — reverts to the previous release | also restores the web dyno count |
| **Render** | **native** — reverts to the previous deploy | via Render's rollback API |
| **Vercel** | **native** — `vercel rollback` to the prior deployment | previous deployment URL |
| **Netlify** | **native** — restores the previous site deploy | via Netlify's API |
| **Google Cloud Run** | **native** — shifts traffic to the previous revision | instant, no rebuild |
| **Azure Container Apps** | **native** — activates the previous revision | revision-based |
| **Fly.io** | **image-swap** — redeploys the previous image tag | rolls back to the prior Docker image |
| **AWS App Runner** | **image-swap** — redeploys the previous image | prior image is recorded per deploy and survives in ECR |
| **Modal** | **not applicable** | Modal deploys from source — redeploy your previous version |

> **Native vs image-swap.** Native rollback uses the platform's own "go back to release N-1"
> primitive — fast and clean. Fly.io and AWS App Runner don't expose that directly, so prod rolls
> back by **redeploying the previous image** it recorded (the prior image survives in your registry).
> Functionally you land on the prior version; the difference is mechanical. Note: a rollback
> redeploys the old image with your *current* env/secrets — it reverts the code, not today's config.

> **Modal is the exception.** It deploys from your source (no image prod can re-point to), so
> "rollback" means redeploying your previous version yourself (git checkout the prior commit and
> `prod "deploy this to …"`). Modal is also experimental.

## Automatic rollback on a failed health check

When you deploy, prod verifies the new version is actually up before declaring success. If the
health check fails, prod treats the deploy as bad. **This safety net is conditional on the deploy
shape:**

- **`web`** — prod GETs the URL. A connection failure, timeout, or `5xx` means "not live." (An
  auth wall `401`/`403`, a redirect to `/login`, or any `2xx`/`3xx` all count as **live** — prod
  won't roll back an app just because it's behind auth.)
- **`mcp-server`** — a plain HTTP `200` isn't enough; prod POSTs a JSON-RPC `initialize` and
  requires a real MCP handshake. A container that serves HTTP but doesn't speak MCP fails.
- **`worker` / `cron`** — **no HTTP probe, so no health-check rollback.** A background worker has no
  URL to check, so prod does not fail it (and does not auto-roll-back) for "not responding." Its
  liveness is the platform's concern; verify it with `prod logs`.

This is why shape matters: applying an HTTP health check to a URL-less worker would roll back every
healthy worker. See [Deploy an AI agent or background worker](./deploy-an-agent-or-worker.md).

## What success looks like

- On-demand: the plan says "revert `<app>` to its previous release/deploy," and after confirmation
  the app is back on its prior version.
- Automatic: a failed web/mcp-server deploy is caught by the liveness check and doesn't leave a
  broken version serving traffic.
- `prod ls` shows the app with `✅ ok` on the version you rolled back to.

## Common pitfalls

- **"No previous deployment found to roll back to."** This is your first deploy — there's nothing to
  return to. Rollback needs a prior successful version.
- **You're on Modal.** Rollback isn't applicable there (source-deployed); redeploy the previous
  version of your app instead.
- **Rolling back doesn't undo a database migration.** A rollback reverts the app version, not your
  data. A migration that changed the schema stays applied — plan destructive migrations carefully.
- **A worker "wouldn't roll back automatically."** Correct — workers have no health-check rollback
  by design. If a worker deploy is bad, roll it back on demand (`prod "roll back <platform>"`) or
  redeploy the good version, and watch `prod logs`.
- **Rollback via an agent hangs waiting for approval.** `rollback` is destructive and gated —
  through MCP it must be confirmed explicitly. See [agent-deploy.md](../agent-deploy.md).

## See also

- [Deploy an AI agent or background worker](./deploy-an-agent-or-worker.md) — why auto-rollback is
  shape-conditional.
- [Tear down a deployment](./tear-down-a-deployment.md) — when you want it *gone*, not reverted.
- [Your coding agent ships it](../agent-deploy.md) — the `rollback` MCP tool and its approval gate.
