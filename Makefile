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
# Deploys the project in SMOKE_DIR (default: current dir). Needs a Fly token +
# network + Docker, and provisions REAL infrastructure — not hermetic.
#   make smoke SMOKE_DIR=test-projects/node-app
SMOKE_DIR ?= .
.PHONY: smoke
smoke:
	@echo "Clean-room smoke: no backend, no account, deploy '$(SMOKE_DIR)' to Fly.io."
	@echo "Requires a Fly token + network + Docker. Provisions real infrastructure."
	@cd cli && go build -o $(CURDIR)/$(BUILD_DIR)/prod-smoke ./cmd
	@cd $(SMOKE_DIR) && env -u SUPABASE_URL -u SUPABASE_ANON_KEY $(CURDIR)/$(BUILD_DIR)/prod-smoke "deploy this to fly"
