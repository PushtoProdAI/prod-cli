# Secret sweep — git history

A full-history secret scan (gitleaks 8.30 over all 640 commits) run before making
the repo public. **This file contains no secret values — only redacted
fingerprints, locations, and remediation.**

**Repo is currently PRIVATE**, so nothing here is publicly exposed yet. That makes
this "rotate + (optionally) purge **before** going public" — not an active-breach
emergency. But rotation is mandatory before the repo is public, and history
rewrite is far cheaper now (few clones) than after launch.

---

## Findings — live vs. sample

### 🔴 Live secrets — ROTATE (owner)

| # | Type | Where (redacted) | Commits |
|---|------|------------------|---------|
| 1 | **Render API key** | `rnd_32ub…gVXa` in `cli/internal/agent/workflow.go` | `e1df3f1`, `0730783` ("WIP") |
| 2 | **Sentry DSN** (CLI) | `CLI_SENTRY_DSN=https://08…@…ingest.sentry.io/…` in `env.example` | multiple |
| 3 | **Sentry DSN** (backend) | `SB_SENTRY_DSN=https://2b…@…ingest.sentry.io/…` in `env.example` | multiple |

### 🟡 Low sensitivity — no rotation, purge with the rest

- **Real Supabase project URL** `ciqiwl…supabase.co` hardcoded as a config default
  in `cli/internal/config/config.go` history (also `manifest.json`,
  `artifacts.json`). A URL is not a credential, but it identifies the project.

### 🟢 Not secrets — no action

- **Supabase *demo* keys** (`iss=supabase-demo`, anon + service_role) in
  `env.example` and the deleted `supabase/functions/llm-test/index.ts`. These are
  Supabase's **public, well-known local-dev demo JWTs** — harmless.
- **`YOUR_…OKEN` placeholders** in `docs/deployment-logger-integration.md`.

### ✅ Confirmed NOT in git history

- **The real Supabase anon / service-role keys.** `.env` was **never committed**
  (verified) and `config.go` history has **zero** real JWT literals — the real
  keys lived only in `.env` (gitignored) and were injected at build time via
  `-ldflags`. (CLAUDE.md's note that they're "in old history" overstates it: only
  the Sentry DSNs and the Render key are.) Rotate the Supabase keys only if they
  were exposed through some other channel (a built binary, a screenshot, etc.).

---

## Rotation checklist (owner — mandatory before public)

- [ ] **Render API key** — revoke `rnd_32ub…` (Render dashboard → Account Settings
      → API Keys), issue a new one, update wherever it's used (`.env` / deploy env).
- [ ] **Sentry DSNs** (both `CLI_SENTRY_DSN` and `SB_SENTRY_DSN`) — in Sentry,
      Project → Settings → Client Keys (DSN), revoke the leaked keys and create new
      ones; update `.env` / deploy env.
- [ ] (Optional) **Supabase keys** — only if exposed outside git; not required by
      this sweep.

Rotation is the real fix. It makes the leaked values worthless even if a copy of
history survives somewhere (an old clone, a `make`-built binary that baked the
value via ldflags — those binaries can't be un-leaked).

---

## Purge history (owner — decision + destructive)

Because the repo is private and about to go public, **rewriting history now is
worthwhile** (defense-in-depth, cheap while clones are few) — unlike an
already-public repo with forks, where rotation alone is usually the call.

`git-filter-repo` is installed. The replacement file uses **regex patterns** (no
literal secrets), so it strips every Render key and Sentry DSN across all refs,
including deleted files and merge commits:

```bash
# replacements.txt (regex — matches the leaked shapes, not specific values):
#   regex:rnd_[A-Za-z0-9]{20,}==>rnd_REDACTED
#   regex:https://[a-f0-9]{16,}@[a-z0-9.]*ingest[a-z0-9.]*sentry\.io[/0-9]*==>SENTRY_DSN_REDACTED

# On a FRESH clone (filter-repo refuses to run on a repo with uncommitted work):
git clone git@github-work:PushtoProdAI/prod.git prod-purge && cd prod-purge
printf '%s\n%s\n' \
  'regex:rnd_[A-Za-z0-9]{20,}==>rnd_REDACTED' \
  'regex:https://[a-f0-9]{16,}@[a-z0-9.]*ingest[a-z0-9.]*sentry\.io[/0-9]*==>SENTRY_DSN_REDACTED' \
  > /tmp/replacements.txt
git filter-repo --replace-text /tmp/replacements.txt

# Verify the rewritten clone is clean, THEN force-push (coordinate with collaborators):
gitleaks git --no-banner          # expect: no leaks
# filter-repo drops the 'origin' remote by design — re-add it before pushing:
git remote add origin git@github-work:PushtoProdAI/prod.git
git push --force --all
git push --force --tags
```

> ⚠️ History rewrite changes every downstream commit SHA: collaborators must
> re-clone, open PRs re-base, and any SHA referenced elsewhere (release notes,
> external links) breaks. Do it in one coordinated pass while the team is small,
> and **after** rotation (so a stale clone still can't yield a working key).

---

## Prevention (shipped in this PR)

- `.githooks/pre-commit` runs `gitleaks git --staged` and **blocks a commit that
  would introduce a new secret** (staged-only, so it doesn't trip on the
  pre-existing history above). Active via `make install-hooks` (already sets
  `core.hooksPath .githooks`). Bypass a false positive with `git commit --no-verify`.
- Full-history scanning is intentionally **not** wired into the pre-push gate: it
  would fail on every push until the purge above runs. After the purge, run
  `gitleaks git` once to confirm clean.
- gitleaks is **best-effort**: its `generic-api-key` rule is entropy-dependent and
  there's no dedicated Render/Fly/Vercel rule, so a low-entropy or novel token can
  slip through. Don't treat a clean hook as proof there's no secret — it's a
  backstop, not a guarantee. (It also allowlists well-known docs examples like the
  canonical AWS example key, so use obviously-fake values when testing the hook.)
