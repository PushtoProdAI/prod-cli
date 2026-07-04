#!/usr/bin/env bash
#
# Local verification gate for prod-cli.
#
# Fast, hermetic checks only — no network, no cloud creds, no backend. This is
# the thing we run instead of GitHub Actions: `make check`, and automatically on
# every `git push` via .githooks/pre-push. Cross-compilation (CGO) lives in
# `make check-full`; a real deploy smoke test lives in `make smoke`.
#
# Bypass in a genuine emergency with `git push --no-verify`.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI="$ROOT/cli"
BASE_REF="${PROD_CHECK_BASE:-main}"   # lint only code changed since this ref

cd "$CLI"

step() { printf '\n\033[1;36m▶ %s\033[0m\n' "$1"; }
ok()   { printf '\033[1;32m✓ %s\033[0m\n' "$1"; }
warn() { printf '\033[1;33m! %s\033[0m\n' "$1"; }

# 1. Formatting (exclude generated BAML client)
step "format check"
fmt_tool="gofmt"; command -v gofumpt >/dev/null && fmt_tool="gofumpt"
unformatted="$("$fmt_tool" -l . | grep -v '^baml_client/' || true)"
if [ -n "$unformatted" ]; then
  warn "these files need formatting (run: $fmt_tool -w cli):"
  echo "$unformatted"
  exit 1
fi
ok "formatting clean ($fmt_tool)"

# 2. go vet (blocking)
step "go vet"
go vet ./...
ok "vet clean"

# 3. lint — scoped to code changed vs BASE_REF so the legacy tree doesn't block.
#    Advisory in v1; flip to blocking once the new-code baseline is clean.
step "golangci-lint (new code vs $BASE_REF)"
if command -v golangci-lint >/dev/null; then
  if git rev-parse --verify -q "$BASE_REF" >/dev/null 2>&1; then
    golangci-lint run --new-from-rev="$BASE_REF" ./... || warn "lint findings (advisory — not blocking yet)"
  else
    warn "base ref '$BASE_REF' not found; skipping scoped lint"
  fi
else
  warn "golangci-lint not installed; skipping (brew install golangci-lint)"
fi

# 4. Known vulnerabilities — advisory in v1. Findings today are dominated by Go
#    stdlib issues fixed only by a toolchain upgrade (go1.25.7+); blocking on those
#    would keep the gate permanently red. Flip to blocking once the baseline is clean.
step "govulncheck (advisory)"
if command -v govulncheck >/dev/null; then
  govulncheck ./... || warn "vulnerabilities found (advisory — upgrade Go to 1.25.7+ and keep deps current)"
else
  warn "govulncheck not installed; skipping (go install golang.org/x/vuln/cmd/govulncheck@latest)"
fi

# 5. Build (host arch; CGO on — matches `make build`)
step "go build"
go build ./...
ok "build ok"

# 6. Tests
step "go test"
go test ./...
ok "tests pass"

printf '\n\033[1;32m✓ all local checks passed\033[0m\n'
