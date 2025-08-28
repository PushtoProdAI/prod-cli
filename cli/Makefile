BINARY_NAME=prod
CMD_PATH=cmd/main.go
BUILD_DIR=../bin

GIT_TAG := $(shell git describe --tags --exact-match 2>/dev/null)
ifeq ($(GIT_TAG),)
    VERSION := $(shell git rev-parse --short HEAD)
else
    VERSION := $(GIT_TAG)
endif

# Build flags for setting variables at build time
LDFLAGS := -X 'github.com/meroxa/prod/cli/internal/config.SupabaseURL=$(SUPABASE_URL)' \
           -X 'github.com/meroxa/prod/cli/internal/config.SupabaseAnonKey=$(SUPABASE_ANON_KEY)' \
           -X 'github.com/meroxa/prod/cli/internal/config.ProdDebug=$(PROD_DEBUG)'

.PHONY: build
build: build-cli-darwin-arm64

.PHONY: build-cli-linux
build-cli-linux:
	@echo "Building for Linux..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-$(VERSION)-linux-amd64 $(CMD_PATH)

.PHONY: build-cli-darwin
build-cli-darwin:
	@echo "Building for macOS..."
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-$(VERSION)-darwin-amd64 $(CMD_PATH)

.PHONY: build-cli-darwin-arm64
build-cli-darwin-arm64:
	@echo "Building for macOS (Apple Silicon)..."
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-$(VERSION)-darwin-arm64 $(CMD_PATH)

.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)

.PHONY: generate
generate:
	go tool baml-cli generate

.PHONY: dev
dev:
	@echo "Running in development mode..."
	PROD_DEBUG=true go run -ldflags "$(LDFLAGS)" $(CMD_PATH)

