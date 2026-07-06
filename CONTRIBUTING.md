# Contributing to prod

Thanks for helping build `prod` — a single self-contained Go binary that deploys apps and
agents from natural language, with no backend and no account. This guide covers building,
the local-first quality gate, and the conventions worth knowing before you open a PR.

Read [CLAUDE.md](./CLAUDE.md) first — it's the authoritative architecture map. [ROADMAP.md](./ROADMAP.md)
has the plan and the open-core boundary.

- **Module path:** `github.com/pushtoprodai/prod-cli`
- **License:** MIT (by contributing, you agree your work is licensed under it)
- **Go:** 1.25+, with **CGO enabled** (see the caveat below)

---

## Build & run

All build/dev targets live in the `cli/` directory.

```bash
cd cli
make build       # build the prod binary for your host arch
make dev         # go run for local iteration
make generate    # regenerate baml_client/ from baml_src/ after editing any *.baml
go test ./...    # unit tests
```

`make build` also works from the repo root (it delegates into `cli/`).

### The CGO caveat (read before you build on Linux/Windows)

`prod` links a **native dependency** (BAML), so `CGO_ENABLED=1` and a working C toolchain
are required. Because of this, **cross-compiling to Linux and Windows is a known issue** —
the build is currently host-native (macOS/arm64 by default). On Linux, `make build-cli-linux`
uses a Docker cross-compile path; Windows must be built natively on Windows. Fixing clean
per-OS builds is tracked in the ROADMAP. If `make build` fails with a linker/CGO error,
this is almost certainly why.

---

## The local-first quality gate

The primary gate is **local**: `make check` runs the same build/vet/test/format checks on
your machine before you push. **CI (GitHub Actions) also runs** — `.github/workflows/ci.yml`
builds, vets, tests, and format-checks on both Linux and macOS for every push to `main` and
every PR (with an advisory lint + govulncheck job). Keep `make check` green locally and CI
will be green too.

```bash
make check          # the gate: format → vet → lint → govulncheck → build → test
make install-hooks  # wire the pre-push hook (git config core.hooksPath .githooks)
```

Run `make install-hooks` once after cloning. From then on, `make check` runs before every
push and blocks it on failure. In a genuine emergency you can bypass with
`git push --no-verify`, but don't make a habit of it.

`make check` is fast and hermetic — no network, no cloud creds, no backend. Cross-compilation
(`make check-full`) and a real deploy smoke test (`make smoke`, needs a Fly token) are
separate and not part of the push gate.

**Open a PR only after `make check` is green locally.**

---

## Conventions worth knowing

### Output: emit through the StatusWriter — never `fmt.Println`

Everything user-visible goes through a `StatusWriter` (`internal/output/`). Its
implementations render the same events into console, TUI, and JSON (`PROD_JSON_MODE=true`)
modes — and the JSON stream is the substrate for the agent/MCP surface.

**Do not call `fmt.Println` / `fmt.Fprintf(out, ...)` in agent or deployment code.** Emit
through the writer so your output renders correctly in every mode and stays structured.

> This replaces the old README "Output Pattern Guide," which documented a raw
> `fmt.Fprintf(out, ...)` pattern. That guidance is obsolete — follow CLAUDE.md §6.

When you add a new event, implement it in **all** writers and drive them from one canonical
event object; adding it to a single writer causes drift (and a console-mode panic — see
CLAUDE.md §6).

### The word "agent" means two things

Don't conflate them:

- **prod's orchestrator** (`internal/agent/`) — the internal state machine that drives a
  deploy. Most "agent" references in the code mean this.
- **AI agents as a deploy target** — deploying autonomous LLM apps (web-shaped agents, MCP
  servers).

See CLAUDE.md for the full distinction; be explicit about which you mean in code and PRs.

### Adding a platform adapter

Implement the adapter interfaces from `internal/deployment/deployment.go`. The full
walkthrough — which enum to extend, where to wire dispatch, and the rollback requirement —
is in **CLAUDE.md §6 ("Add a deployment platform")**. `internal/deployment/flyio/` is the
cleanest reference to copy. Deploys always use the **user's own credentials** — there is no
central account.

### Other house rules (see CLAUDE.md §9)

- Dependency injection via constructors, not globals; `cmd/main.go` is the composition root.
- Never hand-edit generated `baml_client/` code — edit `baml_src/*.baml` and `make generate`.
- Wrap errors with stack traces; no naked `panic`.
- Simplicity is the design value — justify any new dependency or moving piece.

---

## Commit & PR flow

1. Branch off `main`.
2. Make your change; keep commits focused.
3. Run `make check` — it must be **green locally**.
4. Open a PR describing what changed and why.

That's it. Keep the local gate green; CI runs the same checks on Linux + macOS for your PR.
