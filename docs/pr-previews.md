# PR preview deploys

Deploy an isolated preview of every pull request, comment its URL, update it on each push, and
tear it down when the PR closes — driven by prod's headless CI flags.

## Quick start

1. Copy [`docs/examples/pr-preview.yml`](./examples/pr-preview.yml) to
   `.github/workflows/pr-preview.yml` in your repo.
2. Edit the two marked lines (`APP`, `PLATFORM`).
3. Add repo secrets (Settings → Secrets and variables → Actions):
   - your platform token (e.g. `FLY_API_TOKEN`, `RENDER_API_KEY`),
   - an LLM key (`ANTHROPIC_API_KEY` or `OPENAI_API_KEY`),
   - any secrets the app itself needs.
4. Open a PR. You'll get a `🚀 Preview deployed: <url>` comment.

## How it works

The workflow uses the `prod-deploy` composite action, which installs prod and runs it headless:

```bash
PROD_JSON_MODE=true prod run --yes \
  --name myapp-pr-7 \
  --env OPENAI_API_KEY=… \
  -- "deploy this to fly"
```

- **`--name myapp-pr-<number>`** gives each PR its own app. Re-running updates that same app
  (idempotent); it never spawns duplicates. If the name collides with another org's app on the
  platform, the deploy fails loudly rather than silently renaming into an orphan.
- **`--yes` + `PROD_JSON_MODE`** run with no prompts and emit a machine-readable
  `deployment_complete` event; the action reads the live URL from it.
- **`--env KEY=VALUE`** supplies app config headlessly. A value on a variable prod can't confirm
  as non-secret routes to the platform's **secret store**, never plaintext config.
- **On close**, the action runs `prod run --yes --name myapp-pr-<number> "destroy this on …"` —
  teardown is best-effort, so an already-gone app won't fail the job.

## Platform notes

- **Fly / Render / container clouds** — prod creates a distinct `myapp-pr-<n>` app per PR (shown
  above). These bill per running app, so **N open PRs = N live apps in your own account** — close
  PRs (or cap concurrency) to control cost.
- **Vercel / Netlify** — these already produce a unique preview URL per deploy natively, so the
  `-pr-<n>` naming matters less (but `name` is still required — pass `${{ env.APP }}-pr-${{ github.event.number }}` anyway; it's harmless). Read `outputs.url`. Teardown is best-effort and
  effectively a no-op — the platform expires previews on its own — so you can drop the `teardown`
  job for these platforms if you prefer.

## Forks & cancellation

- **Fork PRs don't get previews.** On `pull_request`, GitHub withholds secrets from forks (a
  safety default), so the example guards the deploy job with `head.repo.fork == false`. Without the
  guard, every fork PR would show a failed check. Previews run for same-repo (branch) PRs.
- **A new push cancels an in-flight deploy** (`cancel-in-progress`). If a deploy is interrupted,
  the next push reconciles it — prod detects the existing app by name and updates in place, so a
  half-finished deploy self-heals rather than duplicating.

## Using the action directly

```yaml
- id: deploy
  uses: PushtoProdAI/prod-cli/.github/actions/prod-deploy@main   # pin to a tag for stability
  with:
    platform: fly
    name: myapp-pr-${{ github.event.number }}
    env: |
      OPENAI_API_KEY=${{ secrets.OPENAI_API_KEY }}
  env:
    FLY_API_TOKEN: ${{ secrets.FLY_API_TOKEN }}
    ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
# steps.deploy.outputs.url / .id / .status
```

For a reproducible pipeline, pin **both**: the action ref to a release tag or commit SHA
(`@v0.2.13`) **and** the `version:` input to that same release tag — otherwise the action always
installs the latest prod (and its bundled `install.sh` from `main`), so a green pipeline could
change under you.
