# Distributing `prod`

How we ship the `prod` single binary. Releases are **automated**: push a `vX.Y.Z` tag and
[`.github/workflows/release.yml`](../.github/workflows/release.yml) builds darwin natively on a
mac and linux via `cli/Dockerfile.build` on an Ubuntu runner, publishes the GitHub Release, and
updates the Homebrew tap.

- Module: `github.com/pushtoprodai/prod-cli`
- Binary: `prod`
- Go module root: `cli/` (entrypoint `cli/cmd/main.go`)
- Release: [`.github/workflows/release.yml`](../.github/workflows/release.yml) · Installer:
  [`scripts/install.sh`](../scripts/install.sh) · Brew template:
  [`.github/homebrew-formula.rb.template`](../.github/homebrew-formula.rb.template)

> The legacy manual GoReleaser flow (`.goreleaser.yml`, `make release`) is superseded by the
> workflow below and kept only as a local fallback. Don't run it *and* push a tag — you'd
> double-release.

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

## Cutting a release (the automated path)

Because BAML's native lib is fetched at runtime (above), the CGO surface is just a `dlopen`
shim, so linux cross-compiles cleanly inside the pinned build image — no per-arch native
runner needed there. `release.yml` runs two build jobs:

1. **Bump the version** and update [`CHANGELOG.md`](../CHANGELOG.md).
2. **Tag and push:** `git tag -a v0.1.0 -m "v0.1.0" && git push origin v0.1.0`.
3. The workflow then, automatically:
   - builds **darwin/amd64 + arm64** natively on a macOS runner (`CGO_ENABLED=1`);
   - builds **linux/amd64 + arm64** on an Ubuntu runner via `make -C cli dist-linux`, which
     cross-compiles both arches inside [`cli/Dockerfile.build`](../cli/Dockerfile.build) — plain
     Docker, no buildx/QEMU needed;
   - packages `prod_<version>_<os>_<arch>.tar.gz` (binary + README + LICENSE, + NOTICE for linux);
   - generates a combined `prod_<version>_checksums.txt` (`sha256␣␣filename`, what `install.sh`
     verifies) covering all four archives;
   - publishes the GitHub Release with all four archives + the checksums;
   - **updates the Homebrew tap** (see below).
4. **Verify** on a clean mac and a glibc Linux box:
   ```bash
   curl -sSL https://raw.githubusercontent.com/pushtoprodai/prod-cli/main/scripts/install.sh | sh
   brew install pushtoprodai/tap/prod
   ```

`install.sh` allows linux (musl/Alpine rejected with a clear message) and points Windows
users at WSL2. The archive name shape `prod_<version>_<os>_<arch>.tar.gz` is a contract with
`install.sh` and the brew template — change it in all three or installs break.

## Homebrew tap — one-time setup

The `homebrew` job in `release.yml` fills
[`.github/homebrew-formula.rb.template`](../.github/homebrew-formula.rb.template) with the
release's version + per-arch sha256s and commits `Formula/prod.rb` to the tap.

The tap repo already exists and is prepared:
[`github.com/pushtoprodai/homebrew-tap`](https://github.com/pushtoprodai/homebrew-tap) (public;
the old SaaS-era formula that pointed at Supabase storage has been removed — the formula is now
published by this workflow, not by hand). **The one remaining step to enable brew installs:**

- Create a token that can push to the tap — a fine-grained PAT with **Contents: read/write** on
  `homebrew-tap` (or a classic PAT with `repo`) — and add it to `prod-cli` as the repo
  **secret `HOMEBREW_TAP_TOKEN`**.

Until the secret exists, the job **skips cleanly** (it logs a notice; the release still
succeeds). Once set, every tagged release updates the formula and
`brew install pushtoprodai/tap/prod` works on macOS and (glibc) Linux.

### What the release produces
- `prod_<version>_{darwin_arm64,darwin_amd64,linux_amd64,linux_arm64}.tar.gz`
- `prod_<version>_checksums.txt`
- `Formula/prod.rb` in the `homebrew-tap` repo (when `HOMEBREW_TAP_TOKEN` is set)
