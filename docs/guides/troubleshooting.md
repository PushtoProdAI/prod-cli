# Troubleshooting

**What you'll accomplish:** diagnose the handful of things that actually go wrong with a
prod deploy — a missing LLM key, a cloud credential prod can't see, a headless run that
stalls, Docker not running — and fix each fast. Start with `prod doctor`; most issues show
up there.

## First: run `prod doctor`

`prod doctor` is read-only and reports what prod can see: your LLM provider and whether
Docker is available. It exits non-zero when no usable LLM is found, so
`prod doctor && prod "…"` short-circuits cleanly.

```
$ prod doctor
✅ LLM: OpenAI (gpt-4o) — reachable
✅ Docker: available
```

> **What `doctor` does *not* check: platform credentials.** Those are verified when you
> deploy, not by `doctor`. To confirm a cloud is set up first, run its whoami — see
> [Verify your setup](../clouds.md#verify-your-setup).

## "No LLM configured" / "no usable LLM provider"

prod needs an LLM to parse your request. Set one of `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`,
or run a local Ollama. Priority is OpenAI > Anthropic > Ollama. See
[Bring your own LLM](./bring-your-own-llm.md).

- **Wrong provider used.** Both cloud keys set? OpenAI wins the tie. Unset `OPENAI_API_KEY`
  to force Anthropic.
- **Ollama fallback ignored.** prod only falls back to Ollama when *no* cloud key is set — a
  stale `OPENAI_API_KEY` silently pre-empts it. Confirm `ollama serve` is up and
  `PROD_LLM_MODEL` names a model you've pulled.

## The env-inheritance gotcha (this bites everyone)

**Symptom:** prod works in your terminal but reports "no LLM configured" (or can't find a
cloud token) when launched from your editor or coding agent.

**Cause:** an MCP client launched from a GUI — Cursor, Claude Code from the Dock/Start menu,
a JetBrains IDE — inherits the **editor's** environment, not your shell's. A key exported in
`~/.zshrc` or `~/.bashrc` is invisible to it.

**Fix:** launch the editor from a terminal that already has the keys exported, or set the
keys where the editor reads its environment. Run `prod doctor` from inside that context to
see exactly what prod sees. Full write-up in
[agent-deploy.md](../agent-deploy.md#the-one-gotcha-that-bites-everyone).

## A cloud deploy can't authenticate

prod uses your own credentials for each cloud and checks them at deploy time. If a deploy
fails to authenticate:

1. **Run the platform's whoami** ([the full list](../clouds.md#verify-your-setup)) — e.g.
   `fly auth whoami`, `aws sts get-caller-identity`, `az account show`. If *that* fails,
   prod can't authenticate either; fix the credential first.
2. **Check for a stale token overriding a CLI login.** The environment variable wins over a
   CLI session — an old `FLY_API_TOKEN`/`VERCEL_TOKEN`/etc. in your shell overrides
   `fly auth login`. Unset it if the wrong account is being used.
3. **Cloudflare "account id is missing."** Cloudflare needs **both** `CLOUDFLARE_API_TOKEN`
   and `CLOUDFLARE_ACCOUNT_ID`. See [Cloudflare setup](../clouds.md#cloudflare-pages-setup).
4. **Render "registry not configured."** Render pulls your image from a registry you own —
   set the `PROD_REGISTRY_*` vars ([registry setup](../clouds.md#container-registry-render-and-custom-setups)).

## "Docker isn't running"

The managed-container clouds (AWS App Runner, Google Cloud Run, Azure Container Apps) and
Render build a container image on your machine. Start Docker Desktop (or your engine) first —
`prod doctor` flags it when Docker is down. The static/serverless clouds (Vercel, Netlify,
Cloudflare Pages) and Fly.io token deploys don't need Docker.

## A headless / CI deploy stalls or hangs

**Symptom:** `prod run` in CI never finishes, or a scripted deploy waits forever.

- **A required env var has no value.** With no `--env`/`--env-file` for a required variable,
  prod hits an interactive prompt a headless run can't answer. Supply every required value up
  front — see [Environment variables & secrets](./environment-variables-and-secrets.md#supplying-values-headlessly-ci-agents).
- **An agent/MCP rollback or destroy is waiting for approval.** `deploy`/`rollback`/`destroy`
  are gated and require explicit confirmation. Pass `--yes` (CLI) or the explicit `confirm`
  flag (MCP). See [Use prod from an agent (MCP)](../agent-deploy.md).

## A deploy rolled back on its own

For **web** and **mcp-server** shapes, prod verifies the new version is live and rolls back
if the health check fails. This is by design. A **worker/cron** has no URL and is **not**
health-checked, so it won't auto-roll-back. If a web deploy rolled back unexpectedly, check
that the app actually serves the URL prod probed — see
[Roll back a bad deploy](./roll-back-a-bad-deploy.md).

## Still stuck?

- `PROD_DEBUG=true prod "…"` turns on verbose logs (and the go-workflows debug UI).
- Re-run `prod doctor` inside the exact environment the failing command runs in.
- File an issue with the `prod doctor` output and the (redacted) command:
  [github.com/PushtoProdAI/prod-cli/issues](https://github.com/PushtoProdAI/prod-cli/issues).

## See also

- [Configuring your clouds](../clouds.md) — per-cloud credentials and the whoami checks.
- [Choosing a cloud](./choosing-a-cloud.md) — setup from zero for each platform.
- [Bring your own LLM](./bring-your-own-llm.md) — provider selection and keys.
- [Your coding agent ships it](../agent-deploy.md) — the MCP env-inheritance gotcha in full.
