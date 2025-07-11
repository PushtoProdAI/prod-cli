BINARY_NAME=prod
CMD_PATH=cli/cmd/main.go
BUILD_DIR=bin

GIT_TAG := $(shell git describe --tags --exact-match 2>/dev/null)
ifeq ($(GIT_TAG),)
    VERSION := $(shell git rev-parse --short HEAD)
else
    VERSION := $(GIT_TAG)
endif

.PHONY: build-cli
build-cli: build-cli-linux build-cli-darwin build-cli-darwin-arm64

.PHONY: build-cli-linux
build-cli-linux:
	@echo "Building for Linux..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-$(VERSION)-linux-amd64 $(CMD_PATH)

.PHONY: build-cli-darwin
build-cli-darwin:
	@echo "Building for macOS..."
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

# Supabase commands
.PHONY: supabase-start
supabase-start:
	@echo "Starting Supabase local development..."
	@supabase start

.PHONY: supabase-stop
supabase-stop:
	@echo "Stopping Supabase local development..."
	@supabase stop

.PHONY: supabase-status
supabase-status:
	@echo "Checking Supabase status..."
	@supabase status

.PHONY: supabase-reset
supabase-reset:
	@echo "Resetting Supabase local development..."
	@supabase db reset

.PHONY: supabase-migration-new
supabase-migration-new:
	@echo "Creating new migration..."
	@read -p "Enter migration name: " name; supabase migration new $$name

.PHONY: supabase-migration-up
supabase-migration-up:
	@echo "Applying migrations..."
	@supabase db push

.PHONY: supabase-seed
supabase-seed:
	@echo "Seeding database..."
	@supabase db seed

.PHONY: supabase-studio
supabase-studio:
	@echo "Opening Supabase Studio..."
	@open http://localhost:54323

.PHONY: supabase-init-force
supabase-init-force:
	@echo "Force initializing Supabase project..."
	@supabase init --force

.PHONY: supabase-setup
supabase-setup:
	@echo "Setting up Supabase for local development..."
	@if ! command -v supabase &> /dev/null; then \
		echo "Installing Supabase CLI..."; \
		brew install supabase/tap/supabase; \
	fi
	@echo "Initializing Supabase project..."
	@if [ -f "supabase/config.toml" ]; then \
		echo "Supabase config already exists, skipping init..."; \
	else \
		supabase init; \
	fi
	@echo "Starting Supabase..."
	@supabase start
	@echo "Supabase is ready! Studio available at: http://localhost:54323"

.PHONY: generate
generate:
	cd cli && go tool baml-cli generate
