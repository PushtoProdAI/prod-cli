# Distributing `prod`

How we ship the `prod` single binary. We are **local-first**: there is no CI. Every
release is cut from a maintainer's machine with [GoReleaser](https://goreleaser.com).

- Module: `github.com/pushtoprodai/prod-cli`
- Binary: `prod`
- Go module root: `cli/` (entrypoint `cli/cmd/main.go`)
- Config: [`.goreleaser.yml`](../.goreleaser.yml) · Installer: [`scripts/install.sh`](../scripts/install.sh)

---

## How BAML's CGO actually works (this drove the wrong assumptions before)

`prod` depends on [BAML](https://github.com/boundaryml/baml), used through cgo, so
`CGO_ENABLED=1`. It was long assumed this forced a build-time link against a
platform-specific **native library**, making cross-compilation from a mac impractical.
**That assumption is wrong.** Reading the binding
(`baml_go/lib_unix.go`, `lib_common.go`):

- The cgo surface is a **`dlopen` shim**: `#cgo LDFLAGS: -ldl` + `#include <dlfcn.h>` and a
  small wrapper. **Nothing platform-specific is linked at build time.** Any linux-targeting
  C compiler (`x86_64-linux-gnu-gcc`, `aarch64-linux-gnu-gcc`) handles it — cross-compiling
  the Go binary is a non-event.
- The real engine — `libbaml_cffi-<target-triple>.{so,dylib,dll}` (~56 MB) — is
  **downloaded at runtime** from BAML's GitHub releases into `~/.cache/baml/libs/<version>/`
  on first use and `dlopen`ed. The target is chosen from the **runtime** `GOOS/GOARCH`.
- This happens in package `init()`, which **panics** if the library can't be found or
  fetched. Because `main → baml_client → … → baml_go`, *every* invocation (even
  `prod --version`) triggers it at process start.

So the build story is easy; the **runtime library provisioning** is the thing to get right
(see "Runtime library" below).

**Build matrix (verified):**

| Target          | From an Apple-Silicon mac                                                    |
|-----------------|-----------------------------------------------------------------------------|
| `darwin/arm64`  | ✅ native (`make build-cli-darwin-arm64`)                                   |
| `darwin/amd64`  | ✅ Apple `clang` cross-compiles across darwin arches                        |
| `linux/amd64`   | ✅ **via Docker** (`make build-cli-linux`) — cross-gcc; verified to run     |
| `linux/arm64`   | ✅ **via Docker** (`make build-cli-linux-arm64`) — cross-gcc                |
| `linux/musl` (Alpine) | ❌ unsupported — BAML v0.212.0 hardcodes `isMusl()=false`, fetches the `-gnu` lib, fails to load |
| `windows/amd64` | ❌ needs a mingw-w64 toolchain — deferred (low demand)                      |

---

## The linux build (`cli/Dockerfile.build`)

[`cli/Dockerfile.build`](../cli/Dockerfile.build) builds `linux/amd64` and `linux/arm64`
inside a `golang` image with `gcc-x86-64-linux-gnu` / `gcc-aarch64-linux-gnu` cross
compilers. `cd cli && make build-cli-linux` (and `build-cli-linux-arm64`) drive it.

> The Docker base image must match `cli/go.mod`'s `go` directive. It was pinned to
> `golang:1.24.4` while go.mod required 1.25.0, so `go mod download` failed with
> `go.mod requires go >= 1.25.0` and every linux build "broke." Keep the `FROM` line in
> lockstep with `go.mod`.

A produced `linux/amd64` binary has been verified to **run** in a clean
`debian:bookworm-slim` container (see the runtime requirements below), so this is a
first-class linux path — not a side channel.

> `zig cc` is **not** needed here (an earlier plan suggested it). Zig only helps when
> there's real C to cross-compile; the BAML shim is just `dlopen`, which the cross-gcc
> already handles. Don't reach for it.

---

## Runtime library — the real UX detail

On first run, BAML downloads `libbaml_cffi` and caches it. Consequences to document for
users and to build into any container image:

- **Network is required on first run** to reach `github.com/boundaryml/baml` releases
  (~56 MB). Subsequent runs use the cache (`~/.cache/baml/libs/<version>/`, or
  `$BAML_CACHE_DIR`).
- **CA certificates are required.** Minimal images (`debian:*-slim`, `alpine`) ship
  **without** `ca-certificates`, so the HTTPS fetch fails with
  `x509: certificate signed by unknown authority` and `init()` panics. Install
  `ca-certificates` in any minimal runtime image.
- **Offline / air-gapped:** pre-place the correct `libbaml_cffi-<triple>.so` and set
  `BAML_LIBRARY_PATH=/path/to/lib` (and optionally `BAML_LIBRARY_DISABLE_DOWNLOAD=true`).
- **glibc floor:** the `prod` binary itself links very low (`GNU/Linux 3.2.0`); the
  downloaded `libbaml_cffi` sets its own floor, which we don't control. Mainstream glibc
  distros (Debian/Ubuntu/Fedora, current releases) work; **musl/Alpine does not** (above).
- **Supply-chain note:** BAML currently publishes no `.sha256` for the lib, so the download
  proceeds **unverified** (the client logs a warning). Track upstream; consider pinning /
  vendoring the lib for releases where integrity matters.

---

## Shipping linux (GoReleaser OSS can't do it directly)

GoReleaser **OSS** builds darwin natively on the mac, but it can neither cross-compile CGO
from a mac nor ingest prebuilt binaries — **`builder: prebuilt` is GoReleaser Pro-only**. So
linux ships as **separate archives** attached to the same release:

1. `make -C cli dist-linux VERSION=<tag>` — Docker-builds linux/amd64 + arm64 and packages
   `cli/dist/prod_<tag>_linux_{amd64,arm64}.tar.gz` (+ `…_linux_checksums.txt`), matching the
   name shape `install.sh` expects. **Pass `VERSION=<tag>`** so the stamped version matches
   darwin (otherwise it's the short commit hash).
2. GoReleaser cuts the darwin release (darwin archives + `prod_<tag>_checksums.txt` + the
   Homebrew tap).
3. `gh release upload <tag> cli/dist/prod_<tag>_linux_*.tar.gz` — attach the linux archives.
   Optionally append the linux sha256 lines to the release's `checksums.txt` so `install.sh`
   verifies them; if you don't, `install.sh` installs linux with a "checksum not found"
   warning (it degrades gracefully — it does not fail).
4. `install.sh` already allows linux (musl rejected with a clear message).

> **Alternative:** with **GoReleaser Pro**, a `builder: prebuilt` build ingests the
> `cli/dist-prebuilt/linux_<arch>/prod` binaries directly into a unified archive + checksum
> matrix. We stay on OSS, so linux is the manual attach above.

---

## Local release runbook

Prerequisites: `brew install goreleaser`, Docker running (for linux), a clean working tree,
a `GITHUB_TOKEN` with `repo` scope, and the [`gh`](https://cli.github.com) CLI.

1. **Tag** (so darwin and linux stamp the same version): `git tag -a v0.1.0 -m "v0.1.0"`.
2. **Build + package linux** (Docker): `make -C cli dist-linux VERSION=v0.1.0`. Smoke-test a
   linux archive **in a glibc container with `ca-certificates`** — the darwin host can't
   exercise the linux runtime path.
3. **Validate darwin config:** `make release-check` (`goreleaser check`).
4. **Dry-run darwin:** `make release-snapshot` (`goreleaser release --snapshot --clean`);
   smoke-test `tar -xzf dist/prod_*_darwin_arm64.tar.gz && ./prod --version`.
5. **Push the tag + cut darwin:** `git push origin v0.1.0` then
   `export GITHUB_TOKEN=… && make release` (`goreleaser release --clean`).
6. **Attach linux:** `gh release upload v0.1.0 cli/dist/prod_v0.1.0_linux_*.tar.gz`.
7. **Verify installs** on both a mac and a glibc Linux box:
   ```bash
   curl -sSL https://raw.githubusercontent.com/pushtoprodai/prod-cli/main/scripts/install.sh | sh
   brew install pushtoprodai/tap/prod
   ```

### What the release produces
- darwin (GoReleaser): `prod_<version>_{darwin_arm64,darwin_amd64}.tar.gz` +
  `prod_<version>_checksums.txt` + a `prod` Homebrew formula.
- linux (manual attach): `prod_<version>_{linux_amd64,linux_arm64}.tar.gz`.

The archive name shape `prod_<version>_<os>_<arch>.tar.gz` is a contract with
`scripts/install.sh` — change it in both places or installs break.
