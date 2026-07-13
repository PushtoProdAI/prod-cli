# Tear down a deployment (destroy)

**What you'll accomplish:** permanently remove a deployment you no longer need — and, just as
important, understand **what destroy removes and what it leaves behind**, so you don't keep paying
for an orphaned database, container image, or registry repo.

Destroy is irreversible. prod treats it like rollback and deploy: it previews first, and through an
agent it's gated behind explicit human approval.

## Prerequisites

- prod installed with an LLM key and the platform credential for the app you're removing.
- The app's name — run `prod ls` to see what you've deployed.

## Destroy an app

Ask in English (or use the MCP `destroy` tool):

```bash
prod "destroy this on fly"
```

```
📦 Plan: destroy — permanently delete your-app on Fly.io (irreversible)
Proceed? [y/N] y
✅ Destroyed your-app
```

For per-PR preview environments, teardown is best-effort and idempotent by name — a
`prod run --yes --name myapp-pr-7 "destroy this on fly"` is exactly how the PR-preview workflow
cleans up on close. See [PR preview deploys](../pr-previews.md).

## What destroy removes — and leaves — per platform

Destroy is implemented for some platforms and not others. When it isn't, prod tells you to remove
the app from the platform's console rather than pretending to succeed.

| Platform | Destroy | What it removes | What it leaves behind |
|----------|---------|-----------------|-----------------------|
| **Heroku** | supported | the app **and its add-ons** (Postgres, Redis) | — (add-ons cascade with the app) |
| **Fly.io** | supported | the app (machines + attached volumes) | a separately-created **Postgres/Redis app** stays — delete it yourself |
| **AWS App Runner** | supported | the App Runner service | the **ECR repo + pushed image**, and any Secrets Manager secrets |
| **Google Cloud Run** | supported | the Cloud Run service | the **Artifact Registry image** and Secret Manager entries |
| **Azure Container Apps** | supported | the Container App | the **ACR image** and secrets |
| **Render** | supported | the Render service (web / worker / cron) | a separately-created **Render Postgres / Key-Value** instance stays — delete it yourself |
| **Vercel** | not supported yet | — | delete the project in the Vercel dashboard |
| **Netlify** | not supported yet | — | delete the site in the Netlify dashboard |
| **Modal** | not supported yet | — | remove the app with the `modal` CLI / dashboard |

When destroy isn't supported you'll see:

```
Teardown isn't supported for Vercel yet — remove it from the platform's console.
```

> **The cost-hygiene point.** On every platform *except Heroku*, destroying the app does **not**
> remove its backing database, container image, or registry repository. Those keep costing money
> (a running Fly Postgres, ECR storage, an Artifact Registry image) until you delete them. Heroku
> is the one platform where add-ons are torn down with the app.

## Cost hygiene checklist

After a destroy, clean up the resources prod left behind:

- **Fly.io** — delete the Postgres/Redis app: `fly apps list` then `fly apps destroy <db-app>`.
- **AWS App Runner** — delete the ECR repository (and any leftover Secrets Manager secrets) in the
  AWS console or `aws ecr delete-repository`.
- **Google Cloud Run** — delete the Artifact Registry image and Secret Manager entries.
- **Azure Container Apps** — delete the ACR image; remove the resource group if prod created a
  dedicated one and you're done with it.
- **Render** — prod destroys the service, but its backing **Postgres / Key-Value** stays; delete it
  in the Render dashboard.
- **Vercel / Netlify / Modal** — remove the project/site/app (and any managed database) from the
  platform's own console — prod doesn't destroy these yet.

## What success looks like

- On a supported platform: the plan says "permanently delete `<app>` … (irreversible)," and after
  confirmation the service is gone. It no longer appears as an active app in `prod ls` (use
  `prod ls --all` to see the destroy operation in history).
- On an unsupported platform: prod refuses cleanly and points you at the console — nothing is
  half-removed.

## Common pitfalls

- **You still get billed after destroy.** Almost always an orphaned backing resource — a Fly
  Postgres, an ECR/Artifact Registry image, a managed database on a bring-your-own platform. Work
  the cost-hygiene checklist above.
- **Destroy "did nothing" on Vercel/Netlify/Modal.** It's not implemented there — prod told you to
  use the console. That's expected, not a failure. (Render, Fly, Heroku, AWS, Cloud Run, and Azure
  are destroyed by prod directly.)
- **You destroyed the wrong app.** Destroy is irreversible and identifies the app by name — check
  `prod ls` first, and pass the exact `--name` in automation.
- **Destroy via an agent stalls.** `destroy` is the most destructive verb and is gated — through MCP
  it must be explicitly confirmed. See [agent-deploy.md](../agent-deploy.md).

## See also

- [Add a database](./add-a-database.md) — which platforms provision a database (and thus leave one
  behind on destroy).
- [Roll back a bad deploy](./roll-back-a-bad-deploy.md) — when you want to revert, not remove.
- [PR preview deploys](../pr-previews.md) — destroy as automated teardown on PR close.
