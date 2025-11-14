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
build-cli: 
	$(MAKE) -C cli build 

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

.PHONY: supabase-test-connection
supabase-test-connection:
	@echo "Testing Supabase connection..."
	@if [ -f ".env" ]; then \
		echo "Loading environment variables..."; \
		export $$(grep -v '^#' .env | grep -v '^$$' | xargs); \
		echo "SUPABASE_URL: $$SUPABASE_URL"; \
		echo "SUPABASE_ANON_KEY: $$(echo $$SUPABASE_ANON_KEY | cut -c1-20)..."; \
		echo "Testing connection with authentication..."; \
		curl -s -o /dev/null -w "HTTP Status: %{http_code}\n" \
			-H "apikey: $$SUPABASE_ANON_KEY" \
			-H "Authorization: Bearer $$SUPABASE_ANON_KEY" \
			"$$SUPABASE_URL/rest/v1/" || echo "Connection failed"; \
	else \
		echo "No .env file found. Please copy env.example to .env and configure your Supabase credentials."; \
	fi

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

# Infrastructure and Lambda configuration
INFRA_DIR=infra
LAMBDA_DIR=lambda
LAMBDA_BUCKET?=prod-aws-deploy
S3_STACK_NAME?=prod-s3-infrastructure
AWS_REGION?=us-east-1

.PHONY: lambda-build
lambda-build: lambda-build-database-url-constructor

.PHONY: lambda-build-database-url-constructor
lambda-build-database-url-constructor:
	@echo "Building database-url-constructor Lambda function..."
	@cd $(LAMBDA_DIR)/database-url-constructor && \
		npm install --production && \
		./build.sh
	@echo "✓ Lambda function built: $(LAMBDA_DIR)/database-url-constructor/function.zip"
	@echo ""
	@echo "To upload to S3, run:"
	@echo "  aws s3 cp $(LAMBDA_DIR)/database-url-constructor/function.zip \\"
	@echo "    s3://$(LAMBDA_BUCKET)/lambda-functions/database-url-constructor/function.zip \\"
	@echo "    --metadata version=\$$(cd $(LAMBDA_DIR)/database-url-constructor && node -p \"require('./package.json').version\")"

.PHONY: lambda-clean
lambda-clean:
	@echo "Cleaning Lambda function build artifacts..."
	@rm -rf $(LAMBDA_DIR)/database-url-constructor/node_modules
	@rm -f $(LAMBDA_DIR)/database-url-constructor/function.zip
	@echo "✓ Lambda function artifacts cleaned"

.PHONY: lambda-version
lambda-version:
	@echo "Lambda function versions:"
	@cd $(LAMBDA_DIR)/database-url-constructor && \
		echo "  database-url-constructor: v$$(node -p "require('./package.json').version")"

