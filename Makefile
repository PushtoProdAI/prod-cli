BINARY_NAME=prod
CMD_PATH=cli/cmd/main.go
BUILD_DIR=bin

GIT_TAG := $(shell git describe --tags --exact-match 2>/dev/null)
ifeq ($(GIT_TAG),)
    VERSION := $(shell git rev-parse --short HEAD)
else
    VERSION := $(GIT_TAG)
endif

# ---------------------------------------------------------------------------
# Build (the single binary)
# ---------------------------------------------------------------------------
.PHONY: build
build:
	$(MAKE) -C cli build

.PHONY: build-cli-linux
build-cli-linux:
	@echo "Building for Linux..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-$(VERSION)-linux-amd64 $(CMD_PATH)

.PHONY: build-cli-darwin
build-cli-darwin:
	@echo "Building for macOS (Intel)..."
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-$(VERSION)-darwin-amd64 $(CMD_PATH)

.PHONY: build-cli-darwin-arm64
build-cli-darwin-arm64:
	@echo "Building for macOS (Apple Silicon)..."
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-$(VERSION)-darwin-arm64 $(CMD_PATH)

.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)

# ---------------------------------------------------------------------------
# Local verification — we run everything locally, not in CI.
#   make check         fast, hermetic gate (also runs on every push via the hook)
#   make check-full    + cross-compile the CGO targets via cli/Dockerfile.build
#   make install-hooks wire the pre-push hook (git config core.hooksPath .githooks)
#   make smoke         clean-room deploy test (needs a Fly token + network; not hermetic)
# ---------------------------------------------------------------------------
.PHONY: check
check:
	@./scripts/check.sh

.PHONY: check-full
check-full: check
	@echo "Cross-compiling CGO targets via cli/Dockerfile.build..."
	@cd cli && docker build -f Dockerfile.build -t prod-cli-build .

.PHONY: install-hooks
install-hooks:
	@git config core.hooksPath .githooks
	@chmod +x .githooks/* scripts/check.sh
	@echo "✓ pre-push gate installed (core.hooksPath -> .githooks)"

# Phase-0 definition of done: deploys with no backend and no account.
# Expected to FAIL until the backend is severed from the deploy path (ROADMAP Phase 0).
.PHONY: smoke
smoke:
	@echo "Clean-room smoke test: no backend, no account, deploy to Fly.io."
	@echo "Requires FLY_API_TOKEN + network. Not hermetic. Expected to fail pre-Phase-0."
	@env -u SUPABASE_URL -u SUPABASE_ANON_KEY go run ./cli/cmd "deploy this to fly"
