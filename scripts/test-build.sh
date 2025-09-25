#!/bin/bash

# Test script for local GoReleaser build
# This script tests the build process locally without creating a release

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if GoReleaser is installed
check_goreleaser() {
    if ! command -v goreleaser &> /dev/null; then
        log_info "Installing GoReleaser..."
        case "$(uname -s)" in
            Linux*)
                wget -O goreleaser.deb https://github.com/goreleaser/goreleaser/releases/latest/download/goreleaser_linux_amd64.deb
                sudo dpkg -i goreleaser.deb
                rm goreleaser.deb
                ;;
            Darwin*)
                if command -v brew &> /dev/null; then
                    brew install goreleaser/tap/goreleaser
                else
                    log_error "Please install GoReleaser manually: https://goreleaser.com/install/"
                    exit 1
                fi
                ;;
            *)
                log_error "Unsupported OS. Please install GoReleaser manually: https://goreleaser.com/install/"
                exit 1
                ;;
        esac
    fi
}

# Load environment variables from .env file
load_env() {
    if [[ -f ".env" ]]; then
        log_info "Loading environment variables from .env file..."
        set -a  # automatically export all variables
        source .env
        set +a
    elif [[ -f "../.env" ]]; then
        log_info "Loading environment variables from ../.env file..."
        set -a
        source ../.env
        set +a
    else
        log_warn "No .env file found. Using default values for testing."
    fi
}

# Test local build
test_local_build() {
    log_info "Testing local GoReleaser build..."
    
    # Load environment variables
    load_env
    
    # Create a test version
    export GORELEASER_CURRENT_TAG="v0.0.0-test"
    
    # Set default environment variables for testing if not already set
    export SUPABASE_URL="${SUPABASE_URL:-https://test.supabase.co}"
    export SUPABASE_ANON_KEY="${SUPABASE_ANON_KEY:-test-anon-key}"
    export PROD_DEBUG="${PROD_DEBUG:-false}"
    
    log_info "Using SUPABASE_URL: ${SUPABASE_URL}"
    
    # Run GoReleaser in snapshot mode
    goreleaser release --snapshot --clean
    
    log_info "Build completed! Check the dist/ directory for artifacts."
    
    # List built artifacts
    if [[ -d "dist" ]]; then
        log_info "Built artifacts:"
        ls -la dist/
    else
        log_error "No dist/ directory found. Build may have failed."
        exit 1
    fi
}

# Test Supabase upload (dry run)
test_supabase_upload() {
    # Load environment variables again for upload test
    load_env
    
    if [[ -z "${SUPABASE_URL:-}" || -z "${SUPABASE_SERVICE_ROLE_KEY:-}" ]]; then
        log_warn "Supabase credentials not set. Skipping upload test."
        log_warn "Set SUPABASE_URL and SUPABASE_SERVICE_ROLE_KEY to test upload."
        return 0
    fi
    
    log_info "Testing Supabase upload (dry run)..."
    log_info "Using SUPABASE_URL: ${SUPABASE_URL}"
    
    # Make upload script executable
    chmod +x scripts/upload-to-supabase-api.sh
    
    # Set test version
    export VERSION="v0.0.0-test"
    
    # Test the upload script
    if ./scripts/upload-to-supabase-api.sh; then
        log_info "Supabase upload test completed successfully!"
    else
        log_error "Supabase upload test failed!"
        exit 1
    fi
}

# Clean up test artifacts
cleanup() {
    log_info "Cleaning up test artifacts..."
    rm -rf dist/
    rm -rf /tmp/prod-*
}

# Main function
main() {
    log_info "Starting GoReleaser build test..."
    
    # Check prerequisites
    check_goreleaser
    
    # Test local build
    test_local_build
    
    # Test Supabase upload if credentials are available
    test_supabase_upload
    
    log_info "All tests completed successfully!"
    
    # Ask if user wants to clean up
    read -p "Do you want to clean up test artifacts? (y/N): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        cleanup
    fi
}

# Run main function
main "$@"
