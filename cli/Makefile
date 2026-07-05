# Cross-platform CLI build system
# Supports building for Linux (AMD64/ARM64) and macOS (AMD64/ARM64)
# Linux builds use Docker for cross-compilation with CGO support
# macOS builds require running on macOS or proper cross-compilation setup

BINARY_NAME=prod
CMD_PATH=cmd/main.go
BUILD_DIR=../bin

# VERSION can be set via environment variable, otherwise fall back to git
ifndef VERSION
    GIT_TAG := $(shell git describe --tags --exact-match 2>/dev/null)
    ifeq ($(GIT_TAG),)
        VERSION := $(shell git rev-parse --short HEAD)
    else
        VERSION := $(GIT_TAG)
    endif
endif

# Load .env file if it exists (check parent directory first, then current directory)
# This is the default for build targets
ifneq (,$(wildcard ../.env))
    include ../.env
    export
else ifneq (,$(wildcard .env))
    include .env
    export
endif

# Release builds stamp ONLY the version — no secrets. Unlike LDFLAGS below (which
# injects Supabase/Sentry values from .env into local dev builds), release
# artifacts must never embed secrets. Used by dist-linux; goreleaser stamps the
# same for darwin.
RELEASE_LDFLAGS := -s -w -X 'github.com/pushtoprodai/prod-cli/internal/config.Version=$(VERSION)'

# Build flags for setting variables at build time
LDFLAGS := -X 'github.com/pushtoprodai/prod-cli/internal/config.SupabaseURL=$(SUPABASE_URL)' \
           -X 'github.com/pushtoprodai/prod-cli/internal/config.SupabaseAnonKey=$(SUPABASE_ANON_KEY)' \
           -X 'github.com/pushtoprodai/prod-cli/internal/config.ProdDebug=$(PROD_DEBUG)' \
					 -X 'github.com/pushtoprodai/prod-cli/internal/config.Environment=$(ENVIRONMENT)' \
					 -X 'github.com/pushtoprodai/prod-cli/internal/config.Version=$(VERSION)'

.PHONY: build
build: build-cli-darwin-arm64

.PHONY: build-cli-linux-arm64
build-cli-linux-arm64:
	@echo "Building for Linux ARM64..."
	@mkdir -p $(BUILD_DIR)
	@if command -v docker >/dev/null 2>&1; then \
		echo "Using Docker for cross-compilation..."; \
		docker build --target linux-arm64 --build-arg LDFLAGS="$(LDFLAGS)" -f Dockerfile.build -t prod-linux-arm64-build .; \
		docker run --rm -v "$(shell cd .. && pwd)/bin:/output" prod-linux-arm64-build sh -c "cp /build/prod-linux-arm64 /output/$(BINARY_NAME)-$(VERSION)-linux-arm64"; \
	else \
		echo "Docker not available, trying native cross-compilation..."; \
		CGO_ENABLED=1 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-$(VERSION)-linux-arm64 $(CMD_PATH); \
	fi

.PHONY: build-all
build-all: build-cli-linux build-cli-linux-arm64 build-cli-darwin-arm64
	@echo "Building macOS AMD64 (may fail on non-macOS platforms)..."
	@$(MAKE) build-cli-darwin || echo "Warning: macOS AMD64 build failed (normal on non-macOS platforms)"

.PHONY: build-linux-all
build-linux-all: build-cli-linux build-cli-linux-arm64

.PHONY: build-darwin-all
build-darwin-all: build-cli-darwin build-cli-darwin-arm64

# dist-linux builds release linux binaries (no secrets) via Docker and packages
# them into cli/dist/ as tar.gz archives + a checksums file, matching the
# name shape install.sh expects. GoReleaser OSS can't cross-compile CGO from a
# mac or ingest prebuilt binaries (that's Pro), so linux ships via these archives
# attached to the release manually — see docs/DISTRIBUTION.md.
#
# Pass VERSION=v0.1.0 (the release tag) so the stamped version matches darwin;
# otherwise VERSION falls back to the short commit hash.
.PHONY: dist-linux
dist-linux:
	@command -v docker >/dev/null 2>&1 || { echo "Docker is required for linux release builds"; exit 1; }
	@rm -rf dist-prebuilt && mkdir -p dist-prebuilt/linux_amd64 dist-prebuilt/linux_arm64 dist
	@echo "Building linux/amd64 (release, no secrets)..."
	docker build --target linux-amd64 --build-arg LDFLAGS="$(RELEASE_LDFLAGS)" -f Dockerfile.build -t prod-linux-amd64-dist .
	docker run --rm -v "$(CURDIR)/dist-prebuilt/linux_amd64:/out" prod-linux-amd64-dist sh -c "cp /build/prod-linux-amd64 /out/prod"
	@echo "Building linux/arm64 (release, no secrets)..."
	docker build --target linux-arm64 --build-arg LDFLAGS="$(RELEASE_LDFLAGS)" -f Dockerfile.build -t prod-linux-arm64-dist .
	docker run --rm -v "$(CURDIR)/dist-prebuilt/linux_arm64:/out" prod-linux-arm64-dist sh -c "cp /build/prod-linux-arm64 /out/prod"
	@echo "Packaging linux archives → cli/dist/ ..."
	@for arch in amd64 arm64; do \
		cp ../README.md ../LICENSE ../NOTICE dist-prebuilt/linux_$$arch/; \
		tar -czf dist/$(BINARY_NAME)_$(VERSION)_linux_$$arch.tar.gz -C dist-prebuilt/linux_$$arch prod README.md LICENSE NOTICE; \
	done
	@cd dist && shasum -a 256 $(BINARY_NAME)_$(VERSION)_linux_*.tar.gz > $(BINARY_NAME)_$(VERSION)_linux_checksums.txt
	@echo "→ cli/dist/ (attach these to the GitHub release; see the runbook):"
	@ls dist/$(BINARY_NAME)_$(VERSION)_linux_*

.PHONY: build-cli-linux
build-cli-linux:
	@echo "Building for Linux..."
	@mkdir -p $(BUILD_DIR)
	@if command -v docker >/dev/null 2>&1; then \
		echo "Using Docker for cross-compilation..."; \
		docker build --target linux-amd64 --build-arg LDFLAGS="$(LDFLAGS)" -f Dockerfile.build -t prod-linux-build .; \
		docker run --rm -v "$(shell cd .. && pwd)/bin:/output" prod-linux-build sh -c "cp /build/prod-linux-amd64 /output/$(BINARY_NAME)-$(VERSION)-linux-amd64"; \
	else \
		echo "Docker not available, trying native cross-compilation..."; \
		CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-$(VERSION)-linux-amd64 $(CMD_PATH); \
	fi

.PHONY: build-cli-darwin
build-cli-darwin:
	@echo "Building for macOS..."
	@mkdir -p $(BUILD_DIR)
	@echo "Note: Cross-compiling macOS binaries from non-macOS requires macOS SDK"
	@echo "Building natively on the current platform..."
	@if [ "$(shell uname)" = "Darwin" ]; then \
		CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-$(VERSION)-darwin-amd64 $(CMD_PATH); \
	else \
		echo "Warning: Cannot cross-compile macOS binary from non-macOS platform"; \
		echo "Please run this target on macOS or use a macOS CI runner"; \
		exit 1; \
	fi

.PHONY: build-cli-darwin-arm64
build-cli-darwin-arm64:
	@echo "Building for macOS (Apple Silicon)..."
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-$(VERSION)-darwin-arm64 $(CMD_PATH)

.PHONY: build-cli-windows
build-cli-windows:
	@echo "Building for Windows AMD64..."
	@mkdir -p $(BUILD_DIR)
	@echo "Note: This must be built natively on Windows"
	@if [ "$(shell go env GOOS)" != "windows" ]; then \
		echo "Error: Windows builds must be built natively on Windows"; \
		exit 1; \
	fi
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-$(VERSION)-windows-amd64.exe $(CMD_PATH)

.PHONY: build-windows-all
build-windows-all: build-cli-windows

.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)

.PHONY: clean-docker
clean-docker:
	@echo "Cleaning Docker build images..."
	@docker rmi prod-linux-build prod-linux-arm64-build 2>/dev/null || true

.PHONY: generate
generate:
	go tool baml-cli generate

.PHONY: dev
dev:
	@echo "Running in development mode (local environment)..."
	@if [ -f ../.env.local ]; then \
		echo "Loading ../.env.local..."; \
		set -a; . ../.env.local; set +a; \
	fi
	ENVIRONMENT=local PROD_DEBUG=true go run -ldflags "$(LDFLAGS)" $(CMD_PATH)

.PHONY: dev-staging
dev-staging:
	@echo "Running in development mode (staging environment)..."
	@if [ -f ../.env ]; then \
		echo "Loading ../.env..."; \
		set -a; . ../.env; set +a; \
	fi
	ENVIRONMENT=staging PROD_DEBUG=true go run -ldflags "$(LDFLAGS)" $(CMD_PATH)

.PHONY: dev-production
dev-production:
	@echo "Running in development mode (production environment)..."
	@if [ -f ../.env ]; then \
		echo "Loading ../.env..."; \
		set -a; . ../.env; set +a; \
	fi
	ENVIRONMENT=production PROD_DEBUG=true go run -ldflags "$(LDFLAGS)" $(CMD_PATH)

