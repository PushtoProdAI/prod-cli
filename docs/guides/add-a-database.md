# Add a database

**What you'll accomplish:** deploy an app that needs Postgres (or Redis), and have prod either
**provision and wire the database for you** or tell you clearly to bring your own `DATABASE_URL` —
depending on the platform. You'll also see how migrations run.

prod detects that your app needs a database from your dependencies — it doesn't guess from the
prompt. What happens next depends on the target platform, so the most important thing this guide
gives you is an honest **per-platform capability table**.

## Prerequisites

- prod installed with an LLM key — see [Bring your own LLM](./bring-your-own-llm.md).
- A platform credential configured — see [Configuring your clouds](../clouds.md).
- An app whose dependencies reveal a database: a Postgres driver (`pg`, `psycopg2`, `asyncpg`,
  `lib/pq`, `pgx`, `gorm postgres`, …) or a Redis client (`redis`, `ioredis`, `go-redis`, …).

## How prod detects a database

During project analysis, prod maps dependencies to **service requirements**. A Postgres driver in
your `package.json` / `requirements.txt` / `go.mod` becomes a Postgres requirement; a Redis client
becomes a Redis requirement. Those requirements drive everything below — so a database is added
because your *code* needs one, not because you asked for it in English.

Connection strings (`DATABASE_URL`, `REDIS_URL`) are recognized as **backing-service** env vars.
prod marks them auto-populated rather than prompting you for them — either it fills them in after
provisioning, or (on bring-your-own platforms) you supply them as env vars.

## Per-platform support — read this first

Not every platform provisions a database. Three do; the rest expect you to bring a `DATABASE_URL`.

| Platform | Postgres | Redis | How `DATABASE_URL` is set |
|----------|----------|-------|---------------------------|
| **Fly.io** | provisioned & attached | provisioned & attached | prod creates the Postgres/Redis app, attaches it, injects `DATABASE_URL` / `REDIS_URL` as secrets |
| **Render** | provisioned | provisioned (Key Value) | prod creates the database, reads its connection info, injects `DATABASE_URL` / `REDIS_URL` |
| **Heroku** | provisioned (add-on) | provisioned (add-on) | prod adds the `heroku-postgresql` add-on; Heroku sets `DATABASE_URL` in config vars |
| **Vercel** | **bring your own** | bring your own | you supply `DATABASE_URL` as an env var |
| **Netlify** | **bring your own** | bring your own | you supply `DATABASE_URL` as an env var |
| **AWS App Runner** | **bring your own** | bring your own | supply `DATABASE_URL` (stored in AWS Secrets Manager) |
| **Google Cloud Run** | **bring your own** | bring your own | supply `DATABASE_URL` as a secret env var |
| **Azure Container Apps** | **bring your own** | bring your own | supply `DATABASE_URL` as a secret env var |
| **Modal** | **bring your own** | bring your own | supply `DATABASE_URL` as an env var |

> **Why the split?** The container clouds (AWS/GCP/Azure) deploy a container to a URL; a managed
> database there means RDS/Cloud SQL/Azure DB, which prod does not yet provision — v1 is
> bring-your-own `DATABASE_URL` (ROADMAP Phase 2 tracks managed RDS + a VPC connector). Vercel and
> Netlify are static/serverless and pair with an external managed database. Fly, Render, and Heroku
> all expose a first-class managed-database primitive prod can drive directly.

> **Fly.io caveat:** provisioning a Fly Postgres/Redis uses the **flyctl** path. Make sure `flyctl`
> is installed and authenticated (`fly auth login`) — the pure-token HTTP path can deploy an app
> but does not create databases.

## Path A — a platform that provisions (Fly, Render, Heroku)

Just deploy. prod sees the Postgres dependency, provisions the database, and wires the connection
string:

```bash
prod "deploy this to fly with a postgres"
```

```
🔍 Analyzing project… detected Node, Postgres (pg)
📦 Plan: deploy web to Fly.io + Postgres  ·  ~$X/month
🔄 The following variables will be auto-populated:
  • DATABASE_URL 🔒 (sensitive)
…
✅ Created Postgres, attached to app
✅ DATABASE_URL set (secret)
✅ URL is live
```

You do **not** type a `DATABASE_URL` — prod injects it (as a secret on Fly, from connection info on
Render, from the add-on on Heroku). Your app reads `process.env.DATABASE_URL` / `os.environ[...]`
as normal.

## Path B — a bring-your-own platform (Vercel, Netlify, AWS, Cloud Run, Azure, Modal)

Create the database wherever you like (Neon, Supabase, RDS, Upstash for Redis, …), then hand prod
the connection string. Because prod didn't detect the value from your code, and because it looks
sensitive, it routes to the platform's secret store — not plaintext.

Interactively, prod prompts for it. Headlessly:

```bash
prod run --yes \
  --env DATABASE_URL="postgres://user:pass@host:5432/dbname" \
  -- "deploy this to cloud run"
```

For Redis, add `--env REDIS_URL=...` the same way. See
[Environment variables & secrets](./environment-variables-and-secrets.md) for how these route.

## Migrations

When prod detects a database **and** your project has a migration tool (Prisma, Alembic, Rails,
Ecto, Flyway, Drizzle, …), it asks the LLM to determine the right migration command and runs it as
part of the deploy — at the platform's proper lifecycle hook, so migrations run **before** new
code serves traffic:

| Platform | When migrations run |
|----------|---------------------|
| **Heroku** | Procfile `release:` phase |
| **Fly.io** | `release_command` in `fly.toml` |
| **Render** | the service's pre-deploy command |
| **Vercel** | locally during the build step, before deploy |

For **Netlify, AWS App Runner, Cloud Run, Azure, and Modal**, prod does not currently wire an
automatic migration step — run migrations yourself (e.g. a one-off command against the database)
before or after the deploy.

## What success looks like

- On a provisioning platform: the plan shows the database, `DATABASE_URL` appears under
  "auto-populated," and the deploy reports the database created and attached.
- On a bring-your-own platform: you supply `DATABASE_URL`, it's stored as a secret, and the app
  connects.
- If you have migrations on a supported platform, they run at deploy time and you see them execute
  before the app goes live.

## Common pitfalls

- **Expecting AWS/GCP/Azure/Vercel/Netlify to spin up a database.** They won't — provide
  `DATABASE_URL`. Check the table above before you plan around auto-provisioning.
- **Fly database provisioning "does nothing."** You're likely on the HTTP-token path — install and
  log in with `flyctl` so prod can create the Postgres/Redis app.
- **A failed deploy left an orphaned database.** If a deploy fails *after* prod created a Postgres
  cluster (Fly) or database (Render) but before finishing, the database can be left behind — the
  per-step cleanup for DB creation is not fully implemented. Check your platform console and delete
  the orphan to avoid paying for it.
- **`destroy` won't remove your database.** Tearing down the app does not delete a separately
  created Fly Postgres or Render database (Heroku is the exception — its add-ons are removed with
  the app). See [Tear down a deployment](./tear-down-a-deployment.md).
- **Migrations didn't run on AWS/Cloud Run/Azure/Netlify/Modal.** That's expected — no automatic
  migration hook there yet. Run them manually.

## See also

- [Environment variables & secrets](./environment-variables-and-secrets.md) — how `DATABASE_URL`
  routes to the secret store.
- [Tear down a deployment](./tear-down-a-deployment.md) — what destroy leaves behind (backing
  databases).
- [Configuring your clouds](../clouds.md) — the container-registry setup Render needs.
