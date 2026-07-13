# Environment variables & secrets

**What you'll accomplish:** deploy an app that needs configuration and secrets — an API key, a
session secret, a third-party token — and understand exactly where each value lands: encrypted on
the platform, in plain config, or skipped because the platform owns it.

prod's rule is simple: **a value that looks sensitive goes to the platform's secret store, never
into plaintext config.** You don't have to tag anything. This guide shows how prod decides, how to
supply values (interactively or headlessly), and how to read the post-deploy checklist for the
handful of vars only you can fill in.

## Prerequisites

- prod installed and an LLM key set (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, or a local Ollama).
  See [Bring your own LLM](./bring-your-own-llm.md).
- One platform credential configured — see [Configuring your clouds](../clouds.md).
- A project that reads env vars (a `.env`, or `process.env.X` / `os.environ["X"]` in the code).

## How prod finds and classifies your variables

Before the deploy, prod runs a **categorize** step. It works in three passes:

1. **Discovery.** prod scans your source for env-var references and reads your `.env` family of
   files (`.env`, `.env.local`, `.env.development`, `.env.production`, `.env.example`) to seed
   default values.
2. **Classification.** Each variable is classified by the LLM into a role and a **sensitivity**
   flag — is this a secret (an API key, a token, a password) or plain config (a log level, a
   feature flag)? prod records the reason, so `DATABASE_URL` and `STRIPE_SECRET_KEY` come back
   marked sensitive while `LOG_LEVEL` does not.
3. **Routing.** Sensitive values are stored as **encrypted platform secrets**; non-sensitive
   values go into plain config. Backing-service vars (`DATABASE_URL`, `REDIS_URL`) are
   auto-populated by prod when it provisions the service — see [Add a database](./add-a-database.md).

You'll see this in the output:

```
🔍 Categorizing environment variables...
✅ Environment variables categorized
🔄 The following variables will be auto-populated:
  • DATABASE_URL 🔒 (sensitive)
ℹ️  Managed by the platform — prod won't set these: NODE_ENV, VERCEL_ENV

Found 2 environment variable(s) that need values:
  • STRIPE_SECRET_KEY 🔒 (sensitive)
  • LOG_LEVEL

🔒 marks sensitive values. prod stores them as encrypted secrets on the platform —
never in plain config. (Masked as you type in the interactive UI.)
```

> **Why not just ask you to tag secrets?** Because the failure mode of a manual tag is a leaked
> key. prod fails *safe*: if a value looks sensitive it's encrypted, and any variable you supply
> that prod didn't detect from your code is treated as a secret by default (see "Headless" below).

## What prod deliberately skips

Three categories of variable are **not** prompted for and **not** written by prod:

- **Empty values.** A variable left blank isn't guessed at — it lands on the post-deploy checklist
  (below) instead of being written as an empty string.
- **Platform-managed vars.** `NODE_ENV`, `CI`, `VERCEL`, `NEXT_RUNTIME`, `NEXT_PHASE`,
  `NEXT_TELEMETRY_DISABLED`, and anything starting with `VERCEL_` are set by the runtime or build
  platform itself. prod drops them from the deploy entirely — hand-setting `NODE_ENV` or overriding
  Next.js's per-function `NEXT_RUNTIME` breaks the app. You'll see a line noting which were skipped.
- **Framework-managed vars.** For a detected framework, vars the framework handler fills in itself
  (e.g. Django's `ALLOWED_HOSTS` and `CSRF_TRUSTED_ORIGINS`) are shown but populated during project
  preparation, not prompted.

## Supplying values interactively (the default)

Run a normal deploy. For each pending variable prod prompts you; sensitive values are **masked as
you type**:

```bash
prod "deploy this to fly"
```

```
Enter value for environment variable 'STRIPE_SECRET_KEY': ••••••••••••
Enter value for environment variable 'LOG_LEVEL': info
```

`STRIPE_SECRET_KEY` is written to Fly as an encrypted secret; `LOG_LEVEL` goes into plain config.

## Supplying values headlessly (CI, agents)

For automation there are two flags on `prod run`:

- `--env KEY=VALUE` — set one variable; repeatable.
- `--env-file <path>` — read `KEY=VALUE` lines from a file (e.g. `.env.ci`). Quotes and `#`
  comments are handled; blank lines and stray `=value` lines are skipped.

```bash
prod run --yes \
  --env-file .env.ci \
  --env STRIPE_SECRET_KEY=sk_live_xxx \
  -- "deploy this to fly"
```

**Precedence** (highest wins): `--env` → `--env-file` → your project's `.env`. A value supplied
this way is marked *collected* and never prompts — that's what makes `--yes` fully hands-free.

**A supplied value prod didn't detect in your code is treated as a secret** and routed to the
platform's secret store, not plaintext. This is the fail-safe default: if prod can't confirm a
variable is non-sensitive, it encrypts it.

> `--env` / `--env-file` live on `prod run`, the automation entrypoint. The plain `prod "…"` form
> and the MCP tool collect values interactively instead. See [Commands](../commands.md) and
> [PR preview deploys](../pr-previews.md) for the headless CI pattern.

## The post-deploy checklist

Some variables can only be filled in *after* the app is live — its own public URL (an auth
callback, a base URL) or a key you'll paste from a provider dashboard. If you left those blank,
prod prints a checklist once the deploy succeeds:

```
⚠️  2 variable(s) were left unset. Set them in your platform's dashboard now that the app is live:
  • NEXTAUTH_URL = https://your-app.fly.dev
  • SENTRY_DSN
Then redeploy (prod "push this to fly") to pick them up.
```

Notice `NEXTAUTH_URL` is **pre-filled with the live URL** — prod recognizes self-referential URL
vars (`AUTH_URL`, `APP_URL`, `BASE_URL`, `SITE_URL`, `NEXTAUTH_URL`, `ORIGIN`, …) and hands you the
value to paste. `DATABASE_URL` is deliberately *not* treated as a self-URL var even though it
contains the substring "BASE_URL" — it's a connection string, and prod populates it for you.

## What success looks like

- Secrets show a 🔒 and are written to the platform's encrypted store — never echoed back, never
  in plain config. Exactly *how* varies by platform (see nuances below).
- Non-sensitive config is set as plain environment values.
- Platform-managed vars are reported as skipped, not silently dropped.
- Any blank var you must set yourself is listed in the post-deploy checklist, with self-URL vars
  pre-filled.

### Per-platform routing nuances

The sensitive/non-sensitive *decision* is the same everywhere; where the value physically lands
differs:

- **Fly.io** — sensitive values become Fly **secrets** (`fly secrets`), non-sensitive go into
  `fly.toml [env]`.
- **Vercel** — sensitive values are added with `--sensitive`; non-sensitive as normal env.
- **Netlify** — sensitive values are stored `--secret`, **except** frontend-public prefixes
  (`NEXT_PUBLIC_`, `VITE_`, `REACT_APP_`, `NUXT_PUBLIC_`, `VUE_APP_`, `GATSBY_`, `EXPO_PUBLIC_`),
  which are intentionally non-secret so they inline into the client bundle.
- **Heroku** — everything goes into Heroku **config vars**, which *are* Heroku's encrypted store;
  there's no separate plaintext tier, so the 🔒 flag doesn't change where a Heroku value lands.

## Common pitfalls

- **"My editor-launched agent can't see my keys."** An MCP client (Cursor, Claude Code from a GUI)
  inherits the editor's environment, not your shell's. Export keys where the editor can see them,
  or launch it from a terminal that has them. Run `prod doctor` to see the environment prod
  actually sees. (Full write-up in [agent-deploy.md](../agent-deploy.md).)
- **A headless deploy stalls waiting for input.** A missing required var with no `--env`/`--env-file`
  value hits an interactive prompt a headless call can't answer. Supply every required value up
  front, or run the deploy once from your terminal to set them.
- **Committing secrets to `.env`.** prod reads `.env` for *defaults* — don't commit real secrets
  there. Pass live secrets with `--env`/`--env-file` (from CI secrets) or type them at the prompt.
- **Expecting `NODE_ENV` to be set.** It won't be — the platform owns it. Same for `VERCEL_*`.

## See also

- [Add a database](./add-a-database.md) — how `DATABASE_URL` is auto-populated.
- [Bring your own LLM](./bring-your-own-llm.md) — the keys prod itself needs.
- [PR preview deploys](../pr-previews.md) — `--env` in a real CI pipeline.
- [Configuring your clouds](../clouds.md) — per-platform credentials.
