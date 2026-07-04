# Distributing `prod`

How we ship the `prod` single binary. We are **local-first**: there is no CI. Every
release is cut from a maintainer's machine with [GoReleaser](https://goreleaser.com).

- Module: `github.com/pushtoprodai/prod-cli`
- Binary: `prod`
- Go module root: `cli/` (entrypoint `cli/cmd/main.go`)
- Config: [`.goreleaser.yml`](../.goreleaser.yml) · Installer: [`scripts/install.sh`](../scripts/install.sh)

---

## The CGO cross-compile problem

`prod` depends on [BAML](https://github.com/boundaryml/baml)
(`github.com/boundaryml/baml`), which ships a **native library** and is used through
cgo. That forces `CGO_ENABLED=1` for every build. (SQLite via `modernc.org/sqlite` is
pure-Go and does *not* add a C requirement, so BAML is the only reason CGO is on.)

With cgo enabled, `go build` is no longer a self-contained Go toolchain operation: for
each `GOOS/GOARCH` it invokes a **C compiler and linker that target that exact OS and
architecture**, and links against that platform's system libraries. A plain
`GOOS=linux GOARCH=amd64 go build` from macOS therefore fails — the host `clang` builds
Mach-O objects for Darwin, not ELF for Linux, and there are no Linux headers/libs to
link against. This is why the historical `.goreleaser.yml` was darwin-only.

**What builds cleanly today:**

| Target             | From an Apple-Silicon mac                                              |
|--------------------|-----------------------------------------------------------------------|
| `darwin/arm64`     | ✅ native                                                              |
| `darwin/amd64`     | ✅ Apple `clang` cross-compiles across darwin arches (`-arch x86_64`)  |
| `linux/amd64`      | ❌ needs a linux-targeting C toolchain                                 |
| `linux/arm64`      | ❌ needs a linux-targeting C toolchain                                 |
| `windows/amd64`    | ❌ needs a mingw-w64 toolchain (+ CGO), must effectively build native  |

Both darwin archives come out of a single `goreleaser release` on a mac, so macOS users
are fully served today. Linux and Windows need one of the options below.

---

## Options for Linux / Windows

### 1. Native per-OS build hosts (most reliable)
Run the build on the OS you're targeting: a Linux box (or Linux Docker container) for the
linux archives, a Windows box for the `.exe`. cgo "just works" because the toolchain is
native. Downside: without CI this means keeping (or spinning up) machines per OS and
stitching their artifacts into one release. For linux specifically, option 3 gives you
this on your mac via Docker.

### 2. `zig cc` as a cgo cross-compiler (least infra)
Zig bundles a full cross-compilation toolchain (clang + per-target libc headers), so it
can be used as the C compiler for arbitrary targets from a single machine:

```bash
# from cli/
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
  CC="zig cc -target x86_64-linux-gnu" \
  CXX="zig c++ -target x86_64-linux-gnu" \
  go build -o ../bin/prod-linux-amd64 ./cmd/main.go
```

This can be wired into GoReleaser per-build with `env: [CC=zig cc -target ...]`. The
caveat: it only succeeds if **BAML's native library also cross-compiles/links under
zig** for the target. That must be proven per target before trusting it — if BAML ships
or builds a platform-specific static lib, zig may not resolve it. Verify a produced
linux binary actually runs (in a Linux container) before publishing.

### 3. The existing Docker cross toolchain for linux (`cli/Dockerfile.build`)
[`cli/Dockerfile.build`](../cli/Dockerfile.build) already builds `linux/amd64` and
`linux/arm64` inside a `golang` image with `gcc-x86-64-linux-gnu` /
`gcc-aarch64-linux-gnu` cross compilers and the right `CC`. `cd cli && make build-cli-linux`
(and `build-cli-linux-arm64`) drive it, and `make check-full` from the repo root builds
the image as a gate. This is the pragmatic linux path on a mac. Its `darwin-amd64` stage
is best-effort and unreliable (no macOS SDK in the image) — ignore it; build darwin on the
mac. It produces **raw binaries**, not GoReleaser archives/checksums, so today linux is a
manual/side-channel artifact rather than part of the goreleaser release.

---

## Recommendation

**Ship darwin via GoReleaser on a mac now** (this is what `.goreleaser.yml` does), and
treat **native/containerized linux builds as the path to first-class linux archives** —
use `cli/Dockerfile.build` (option 3) to produce linux binaries locally today, and adopt
a native Linux host when linux joins the goreleaser release. **Spike `zig cc`
(option 2)** in parallel because it would collapse linux (and maybe windows) into the
same one-command mac release — but only promote it once a zig-built binary is verified to
run with BAML on the target. Windows stays lowest priority: build it natively on Windows
when there's demand. Do **not** re-enable broken linux/windows targets in
`.goreleaser.yml` until one of these is proven, so `goreleaser release` keeps succeeding.

---

## Local release runbook

Prerequisites: `brew install goreleaser`, a clean working tree, and a `GITHUB_TOKEN` with
`repo` scope exported (GoReleaser uses it to create the release and push the Homebrew
formula to `pushtoprodai/homebrew-tap`).

1. **Validate the config** (no build):
   ```bash
   make release-check          # goreleaser check
   ```
2. **Dry-run locally** — builds the darwin archives + checksums into `dist/` without
   tagging or publishing:
   ```bash
   make release-snapshot       # goreleaser release --snapshot --clean
   ```
   Inspect `dist/` and smoke-test the binary: `tar -xzf dist/prod_*_darwin_arm64.tar.gz && ./prod --version`.
3. **Tag the release** (GoReleaser derives the version from the git tag):
   ```bash
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```
4. **Cut the release** (uploads archives + `checksums.txt` to GitHub Releases and updates
   the Homebrew tap):
   ```bash
   export GITHUB_TOKEN=...      # repo scope
   make release                 # goreleaser release --clean
   ```
5. **(Optional) linux binaries** until they're part of the goreleaser release:
   ```bash
   cd cli && make build-cli-linux build-cli-linux-arm64   # via Dockerfile.build → ../bin/
   ```
   Attach them to the GitHub release manually if you want to offer linux downloads.
6. **Verify install paths:**
   ```bash
   curl -sSL https://raw.githubusercontent.com/pushtoprodai/prod-cli/main/scripts/install.sh | sh
   brew install pushtoprodai/tap/prod
   ```

### What the release produces
- `prod_<version>_darwin_arm64.tar.gz`, `prod_<version>_darwin_amd64.tar.gz`
- `prod_<version>_checksums.txt` (sha256; `install.sh` verifies against it)
- A `prod` formula in `pushtoprodai/homebrew-tap` (macOS-only while builds are darwin-only)

The archive name shape `prod_<version>_<os>_<arch>.tar.gz` is a contract with
`scripts/install.sh` — change it in both places or installs break.
