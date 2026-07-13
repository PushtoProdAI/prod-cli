# Bring your own LLM

**What you'll accomplish:** point prod at the LLM you want it to use — OpenAI, Anthropic, or a
fully-local Ollama — and understand exactly how prod picks one so there are no surprises.

prod parses your English request into a typed deployment plan with an LLM. That call goes **direct
to the provider with your own key** — there's no prod proxy, no account, nothing sent to a prod
server. This is the *only* LLM prod needs; it's separate from any provider keys your deployed app
uses at runtime.

## Prerequisites

- prod installed.
- One of: an OpenAI key, an Anthropic key, or [Ollama](https://ollama.com) running locally.

## How prod selects a provider

Selection is automatic, from environment variables, in a fixed priority order:

| Priority | Condition | Provider | Default model |
|----------|-----------|----------|---------------|
| 1 | `OPENAI_API_KEY` is set | OpenAI | `gpt-4o` |
| 2 | else `ANTHROPIC_API_KEY` is set | Anthropic | `claude-3-5-sonnet-20241022` |
| 3 | else (no cloud key) | local Ollama | `llama3.1` |

So if **both** cloud keys are set, **OpenAI wins**. To force Anthropic while an OpenAI key is also
in your environment, unset `OPENAI_API_KEY` for that shell.

Ollama is the zero-key fallback: with no cloud key set, prod talks to a local Ollama at
`http://localhost:11434/v1`. Override the endpoint with `OLLAMA_BASE_URL` (include the `/v1`).

### Override the model

Set `PROD_LLM_MODEL` to use a specific model with whichever provider was selected:

```bash
# OpenAI, but a cheaper/faster model
export OPENAI_API_KEY=sk-...
export PROD_LLM_MODEL=gpt-4o-mini

# Anthropic with a specific Claude model
unset OPENAI_API_KEY
export ANTHROPIC_API_KEY=sk-ant-...
export PROD_LLM_MODEL=claude-3-5-haiku-20241022

# Ollama with a model you've pulled
export PROD_LLM_MODEL=qwen2.5-coder
```

`PROD_LLM_MODEL` sets the model; the *provider* is still chosen by which key is present. Make sure
the model name is valid for that provider.

## Set it up

### OpenAI

```bash
export OPENAI_API_KEY=sk-...
prod doctor
```

### Anthropic

```bash
export ANTHROPIC_API_KEY=sk-ant-...
prod doctor
```

### Fully local with Ollama (no cloud key, nothing leaves your machine)

```bash
ollama serve            # if it isn't already running
ollama pull llama3.1    # or any model you prefer, then set PROD_LLM_MODEL
# ensure no cloud key is set, so prod falls back to Ollama:
unset OPENAI_API_KEY ANTHROPIC_API_KEY
prod doctor
```

`prod doctor` reports which provider it found and whether it's reachable. It's read-only and exits
non-zero when no usable LLM is available, so `prod doctor && prod "…"` short-circuits cleanly.

## Which should you pick?

- **OpenAI / Anthropic** — most reliable intent parsing; the default choice. Either handles prod's
  planning prompts well. Cost per deploy is tiny (a few short calls), but if you deploy in a tight
  CI loop, `PROD_LLM_MODEL=gpt-4o-mini` (or a Haiku model) trims it further.
- **Ollama** — when you want **zero data leaving your machine** or **zero API cost**, or you're
  offline. Quality depends on the local model; a capable instruction-following model
  (`llama3.1`, `qwen2.5-coder`, or larger) parses prod's requests best. Smaller models may
  misclassify intent — if a plan looks wrong, try a bigger model before assuming prod is at fault.

## What success looks like

```
$ prod doctor
✅ LLM: OpenAI (gpt-4o) — reachable
✅ Docker: available
```

Then any deploy — `prod "deploy this to fly"` — uses that provider to build the plan.

## Common pitfalls

- **Your agent/editor can't see the key.** An MCP client launched from a GUI (Cursor, Claude Code
  from the Dock) inherits the editor's environment, not your shell's — so a key exported in
  `~/.zshrc` may be invisible and prod reports "no LLM configured." Launch the editor from a
  terminal that has the key exported, or set it where the editor reads its environment. `prod
  doctor` shows the environment prod actually sees. Full write-up:
  [agent-deploy.md](../agent-deploy.md#the-one-gotcha-that-bites-everyone).
- **Both cloud keys set, "wrong" one used.** OpenAI wins the tie. Unset `OPENAI_API_KEY` to use
  Anthropic.
- **Ollama fallback fails.** prod only falls back to Ollama when *no* cloud key is set — a stale
  `OPENAI_API_KEY` in your environment silently pre-empts it. Also confirm `ollama serve` is up and
  `PROD_LLM_MODEL` names a model you've pulled.
- **`PROD_LLM_MODEL` set to a model the provider doesn't have.** The call fails at the provider;
  the error surfaces from prod. Match the model to the selected provider.

## See also

- [Configuring your clouds](../clouds.md) — the platform credentials prod uses to actually deploy.
- [Environment variables & secrets](./environment-variables-and-secrets.md) — routing your *app's*
  provider keys (which are different from prod's own LLM key).
- [Your coding agent ships it](../agent-deploy.md) — running prod as an MCP server.
