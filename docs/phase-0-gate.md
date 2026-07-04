# Phase 0 gate — closing the launch blockers

The Phase 0 gate (ROADMAP) blocks the public launch. Verifying the *actual* code state (not the
checkboxes) shows the gate is closer than it reads — LICENSE/SECURITY/CONTRIBUTING exist, the
console-mode panic is already fixed, and the Linux build infrastructure is already written. This
plan closes the four remaining items.

> **This plan was revised after a staff review** that overturned WS1's original premise: BAML does
> **not** static-link a native library at build time. Its CGO is a `dlopen` shim (`-ldl`); the real
> engine `libbaml_cffi-<triple>.{so,dylib,dll}` is **downloaded at runtime** from BAML's GitHub
> releases into `~/.cache/baml/libs/<version>/` and `dlopen`ed, and package `init()` **panics** if
> it can't be found/fetched (`BAML_LIBRARY_PATH` / `BAML_LIBRARY_DISABLE_DOWNLOAD` override it).
> So cross-compilation is trivial; the **runtime-lib provisioning + offline story** is the real
> work. WS1 is rewritten accordingly.

Legend: **[me]** I can complete · **[owner]** needs a human/GitHub-side action I'll prepare but
can't execute.

---

## Verified starting state

- ✅ `LICENSE` (MIT), `SECURITY.md`, `CONTRIBUTING.md`, `scripts/install.sh` present.
- ✅ **Console panic already fixed**: every `out.(TUIWriter)` is the checked `, ok` form; the three
  `ConsoleWriter` methods are real implementations, not no-ops.
- ⚠️ **Linux build infra exists but is unverified/unwired**: `cli/Dockerfile.build` has
  `linux-amd64`, `linux-arm64`, `darwin-amd64` CGO stages + a `binaries` scratch stage;
  `cli/Makefile` has `build-cli-linux-{amd64,arm64}` Docker targets. `.goreleaser.yml` +
  `install.sh` still publish/allow darwin only.
- ❌ `go-workflows` still `replace → github.com/lyuboxa/go-workflows` (untagged commit).
- ❌ Supabase keys + Sentry DSN in git history; **`make build` bakes them into binaries via ldflags**
  (`Makefile:31-35` from `.env`); `NOTICE` / third-party-licenses missing.

---

## Workstream 1 — Linux (and arm64) release binaries · **the main effort** · [me] + [owner] release

**Goal:** a non-macOS user runs `curl … | sh` and gets a working `prod`. Phase 0 exit criterion.

**Corrected risk model.** Cross-compiling the Go+CGO binary is a non-event — the only C is a
`dlopen` shim any `x86_64-linux-gnu-gcc` / `aarch64-linux-gnu-gcc` handles (exactly what
`Dockerfile.build` already does). The **real** risks are:
1. **Runtime first-run fetch** of a third-party native lib from GitHub — an offline/air-gapped-UX
   and supply-chain concern, and ironic for a "local-first" tool. `init()` **panics** on failure.
2. **glibc floor of the *downloaded* lib**, not just of `prod`. The floor is `max(prod, libbaml_cffi)`.
3. **musl is unsupported**: `isMusl()` is hardcoded `false` in v0.212.0, so on Alpine BAML fetches
   the `-linux-gnu` variant and fails to load. Alpine/musl is a **non-target**, not a fallback.

**Approach:**
1. **Prove the binary RUNS — with the lib available, not offline.** Build via
   `make build-cli-linux-amd64`, then run it in a clean **glibc** container
   (`debian:bookworm-slim`) *with network* (so `init()` can fetch `libbaml_cffi`) **or** with a
   pre-seeded lib (`BAML_LIBRARY_PATH=… BAML_LIBRARY_DISABLE_DOWNLOAD=true`). Assert `prod --version`
   **and** a BAML-loading command (`prod --help`) succeed. Repeat for `arm64` under
   `--platform linux/arm64` (qemu). *An offline run without a pre-seeded lib will panic on the
   network fetch — that is expected and must not be mistaken for a link failure.*
2. **Design the runtime-lib / offline story (the largest real task, invisible in the first draft).**
   Document `BAML_LIBRARY_PATH`, `BAML_CACHE_DIR`, `BAML_LIBRARY_DISABLE_DOWNLOAD` in
   `docs/DISTRIBUTION.md`; state clearly that first run needs network unless the lib is pre-seeded.
   Recommended for "local-first": pre-download the target libs and pre-seed/ship them so first run
   works air-gapped (decide ship-in-archive vs install.sh-fetches-both). At minimum, make the
   behavior explicit and non-surprising.
3. **Wire release + install.** Keep `Dockerfile.build` for linux (reproducible, already written).
   Use goreleaser's **OSS `builder: prebuilt`** to ingest the Docker-built linux binaries alongside
   the natively-built darwin ones, producing **one** `checksums.txt` + `brews` block that
   `install.sh` already assumes. Flip the `os != darwin` guard in `install.sh`; add linux archives
   to `brews`. (Do **not** make goreleaser cross-build linux from the mac — no benefit.)
4. **Ship attribution.** Generate `NOTICE` / `THIRD-PARTY-LICENSES` (several deps are Apache-2.0 —
   OpenTelemetry, aws-sdk-go-v2, grpc — whose terms require preserving attribution in binary
   redistribution) and include it in the release archives.

**Acceptance criteria:**
- linux/amd64 **and** linux/arm64 binaries print their version **and** run `prod --help`
  (BAML-loading) in a clean glibc container with the lib available.
- `install.sh` installs the linux archive end-to-end in a container; `prod --version` works.
- `docs/DISTRIBUTION.md` documents: supported OS/arch, **min glibc of both `prod` and libbaml_cffi**,
  Alpine/musl unsupported, and the runtime-lib / offline behavior + env overrides.
- Release archives carry `NOTICE`/third-party-licenses.

**Edge cases & mitigation:**
- **Offline first run panics** — the headline UX risk; mitigate via pre-seed/ship + docs (above).
- **glibc too new** — bookworm builds against 2.36; if older distros matter, build `prod` on an
  older base, but `libbaml_cffi`'s own floor still applies and we don't control it — document it.
- **musl/Alpine** — explicitly unsupported; `install.sh` should detect musl and fail clearly rather
  than install a binary that panics at first run.
- **Windows** — needs the `.dll` variant + mingw; low value, **out of scope** this pass; install.sh
  keeps failing clearly, documented.
- **macOS notarization** — `curl | sh` dodges Gatekeeper (no quarantine xattr on curl-fetched
  files), so not a hard gate blocker, but Homebrew-tap + manual downloads hit "developer cannot be
  verified." Flag as a known UX cliff; signing/notarization tracked separately.
- **Release execution is [owner]** — creating the tag and running `goreleaser release` with a GitHub
  token is an owner action; I prepare the config + a dry-run (`goreleaser release --snapshot`).

**PR:** `oss/linux-release-binaries` — verification notes, goreleaser `prebuilt` wiring, install.sh
+ DISTRIBUTION.md + NOTICE. (Tag + publish = **[owner]**.)

---

## Workstream 2 — Move `go-workflows` off the personal fork · [me prep + owner]

**Goal:** the durable-workflow engine comes from a source we control (or upstream), at a **tagged**
release — not a personal account at an untagged commit.

**Source preference (explicit ordering):**
**upstream tag  >  vendored patch  >  our-org fork tag  >  personal fork (current).**
Pinning our own org fork makes us its maintainer in perpetuity — only do it if the delta is real and
not upstreamable; if the delta is tiny, `go mod vendor` + a thin patch beats a standing fork.

**Approach:**
1. Diff `lyuboxa/go-workflows@3f2a5a7` against the nearest `cschleiden/go-workflows` upstream tag —
   establish *what the fork changes and why*.
2. Pick the highest-preference viable option above.
3. If a fork must be kept → **[owner]** creates `github.com/pushtoprodai/go-workflows`; I prepare the
   branch + `v0.x.0` tag and update the `replace`.

**Acceptance criteria:**
- `cli/go.mod` `replace` points to a **tagged** version (or is gone for an upstream tag);
  `go build ./...` + `go test ./...` green; `go mod verify` clean.
- **A targeted regression test** for whatever behavior the fork changes (replay determinism / event
  ordering / retry / timer) — a plain green `go test` won't catch durable-execution drift, so if the
  fork alters engine behavior, add a test that exercises *that* behavior before changing the pin.
- The fork delta vs upstream is documented (what/why).

**PR:** `oss/rehome-go-workflows`. (Repo creation = **[owner]** if a fork is kept.)

---

## Workstream 3 — Secret history sweep · [me scan/prep + owner executes] · **start rotation day 1**

**Goal:** no live secret is usable, and none is extractable once the repo is public.

**Resolve first:** *is `PushtoProdAI/prod` already public?* If yes, the keys are **already exposed** →
**rotation is urgent and starts now, in parallel with everything else.** History rewrite is secondary.

**Key finding:** `make build` **bakes** `SupabaseURL` / `SupabaseAnonKey` / `CLI_SENTRY_DSN` into the
binary via ldflags (`Makefile:31-35`) from `.env`. So (a) any locally-built binary already embeds
them, (b) already-distributed `make`-built binaries **cannot be un-leaked** by a history rewrite, and
(c) therefore **rotation is the only real fix**. (Goreleaser's release build stamps only `Version` —
worth noting the inconsistency; the released archives don't bake secrets.)

**Approach:**
1. **Scan** full history: `gitleaks` + `trufflehog` **and** targeted patterns (`eyJ…` JWTs,
   `…@…sentry.io`, `SUPABASE_SERVICE_ROLE`, `.env`). Report each secret, its commits/paths, live-vs-sample.
2. **Prepare the purge**: a reviewed `git-filter-repo --replace-text` invocation (covers blobs across
   all refs incl. merge commits / deleted files); re-scan the rewritten clone before any force-push.
3. **Rotation checklist** (owner): Supabase anon + service-role, Sentry DSN, anything found.
4. **Prevention:** add a **pre-commit `gitleaks` hook** (extend the existing pre-push `make check`
   hook) so secrets can't recur.

**Acceptance criteria (my part):**
- Scan report (secret → location, live vs sample), scrubbed of the actual values.
- A ready-to-run, reviewed `git-filter-repo` command.
- A rotation checklist.
- A committed pre-commit gitleaks hook.

**Owner-only + decision:** rotate the keys (mandatory; the real fix). History rewrite on a 219-PR
public repo is **explicitly discouraged as the primary move** — force-push, re-clones, changed SHAs —
present it as optional defense-in-depth, not a gate blocker.

**Deliverable:** `docs/security-sweep.md` (report + purge command + rotation checklist, no secret
values) + the pre-commit hook. No `main` code PR beyond the hook.

---

## Workstream 4 — Console-writer parity golden test · **small, closes the "panic" item** · [me]

**Goal:** lock in the already-fixed writer parity so a future event can't silently drift.

**Approach:** a table-driven test driving `ConsoleWriter`, `JSONWriter`, and `TeaWriter` through one
canonical sequence of every `StatusWriter` event, asserting each handles all of them **without panic**
and with stable output. **`JSONWriter` output is the golden anchor** (it's the MCP substrate and must
be byte-stable); Console/Tea are no-panic-plus-smoke, asserted at the `StatusWriter` interface
boundary — **not** by golden-comparing Bubble Tea's rendered frames (brittle).

**Acceptance criteria:**
- Passes for all writers; a new event one writer doesn't handle (or a re-introduced unchecked
  assertion) fails it; runs hermetically in `make check`.

**PR:** `oss/writer-parity-golden-test`.

---

## Ordering & sequencing

- **WS3 rotation runs in parallel from day 1** (owner-side, cheap, highest severity if the repo is
  already public) — don't sequence it last. My WS3 code (scan report + pre-commit hook) lands whenever.
- **WS4** first as a cheap, self-contained warm-up (closes the console item).
- **WS1** is the real gate item and the bulk of the work.
- **WS2** is independent and can run alongside WS1.
- `NOTICE` / third-party-licenses folds into the **WS1** release PR (don't let it fall off).

Each code change is its own PR, gated by `make check` and an adversarial review before merge — same
discipline as PR 3. WS1 release execution, WS2 repo creation (if a fork is kept), and WS3 rotation +
history decision are **[owner]** steps I prepare fully and hand off.

**Definition of done for the gate:** a stranger on a mainstream glibc Linux installs the binary and
it runs (with the runtime-lib behavior documented and non-surprising); the build depends only on
sources we control at tagged versions; and the public repo carries no live secret (keys rotated,
prevention in place). Windows, Alpine/musl, macOS notarization, and history-rewrite are explicitly
tracked as separate decisions, **not** gate blockers.
