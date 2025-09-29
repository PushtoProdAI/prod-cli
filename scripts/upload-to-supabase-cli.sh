#!/bin/bash

# Upload CLI binaries to Supabase Storage using Supabase CLI
# This script uses the Supabase CLI for uploads

set -euo pipefail

# Configuration
BUCKET_NAME="cli-binaries"
VERSION="${GORELEASER_CURRENT_TAG:-latest}"
PROJECT_NAME="prod"

# Global variables for interactive mode
OVERWRITE_ALL=false
SKIP_ALL=false

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Load environment variables from .env file
load_env() {
    local env_file=""
    
    # Try to find .env file in current directory or parent
    if [[ -f ".env" ]]; then
        env_file=".env"
    elif [[ -f "../.env" ]]; then
        env_file="../.env"
    fi
    
    if [[ -n "$env_file" ]]; then
        log_info "Loading environment from $env_file"
        set -a  # automatically export all variables
        source "$env_file"
        set +a  # stop automatically exporting
    else
        log_warn "No .env file found, using existing environment variables"
    fi
}

# Check required environment variables
check_env() {
    local required_vars=("SUPABASE_URL" "SUPABASE_SERVICE_ROLE_KEY")
    local missing_vars=()
    
    for var in "${required_vars[@]}"; do
        if [[ -z "${!var:-}" ]]; then
            missing_vars+=("$var")
        fi
    done
    
    if [[ ${#missing_vars[@]} -gt 0 ]]; then
        log_error "Missing required environment variables: ${missing_vars[*]}"
        log_error "Please set these variables or create a .env file with:"
        for var in "${missing_vars[@]}"; do
            log_error "  $var=your_value_here"
        done
        exit 1
    fi
}

# Install Supabase CLI if not present
install_supabase_cli() {
    if ! command -v supabase &> /dev/null; then
        log_info "Installing Supabase CLI..."
        case "$(uname -s)" in
            Linux*)
                wget -O supabase.deb https://github.com/supabase/cli/releases/latest/download/supabase_linux_amd64.deb
                sudo dpkg -i supabase.deb
                rm supabase.deb
                ;;
            Darwin*)
                if command -v brew &> /dev/null; then
                    brew install supabase/tap/supabase
                else
                    log_error "Please install Supabase CLI manually: https://github.com/supabase/cli"
                    exit 1
                fi
                ;;
            *)
                log_error "Unsupported OS. Please install Supabase CLI manually: https://github.com/supabase/cli"
                exit 1
                ;;
        esac
    fi
}

# Create storage bucket if it doesn't exist
create_bucket() {
    log_info "Ensuring storage bucket '$BUCKET_NAME' exists..."
    
    # Check if bucket exists using Supabase CLI
    if supabase storage ls --linked --experimental 2>/dev/null | grep -q "$BUCKET_NAME"; then
        log_info "Bucket '$BUCKET_NAME' already exists"
    else
        log_info "Creating bucket '$BUCKET_NAME'..."
        # Try to create bucket using Supabase CLI
        if supabase storage create "$BUCKET_NAME" --linked --experimental 2>/dev/null; then
            log_info "Bucket '$BUCKET_NAME' created successfully"
        else
            log_warn "Could not create bucket via CLI. Please create bucket '$BUCKET_NAME' manually in Supabase dashboard."
            log_info "You can create it at: ${SUPABASE_URL}/storage"
        fi
    fi
}

# Check if file exists in storage
file_exists_in_storage() {
    local remote_path="$1"
    local file_name=$(basename "$remote_path")
    
    # Use timeout to prevent hanging
    timeout 10s supabase storage ls "ss:///${BUCKET_NAME}/${remote_path}" --linked --experimental 2>/dev/null | grep -q "$file_name" 2>/dev/null
}

# Ask user for overwrite decision
ask_overwrite() {
    local file_name="$1"
    local remote_path="$2"
    
    echo ""
    log_warn "File '$file_name' already exists in storage at $remote_path"
    echo -e "${YELLOW}What would you like to do?${NC}"
    echo "1) Overwrite the existing file"
    echo "2) Skip this file"
    echo "3) Overwrite all remaining files (no more prompts)"
    echo "4) Skip all remaining files (no more prompts)"
    echo ""
    read -p "Enter your choice (1-4): " choice
    
    case $choice in
        1)
            return 0  # Overwrite this file
            ;;
        2)
            return 1  # Skip this file
            ;;
        3)
            OVERWRITE_ALL=true
            return 0  # Overwrite this file and all remaining
            ;;
        4)
            SKIP_ALL=true
            return 1  # Skip this file and all remaining
            ;;
        *)
            log_error "Invalid choice. Please enter 1, 2, 3, or 4."
            ask_overwrite "$file_name" "$remote_path"
            ;;
    esac
}

# Upload a single file to Supabase storage
upload_file() {
    local file_path="$1"
    local file_name=$(basename "$file_path")
    local remote_path="releases/${VERSION}/${file_name}"
    
    log_info "Uploading $file_name to $remote_path..."
    
    # Try to upload first
    if supabase storage cp "$file_path" "ss:///${BUCKET_NAME}/${remote_path}" --linked --experimental 2>/dev/null; then
        log_info "Successfully uploaded $file_name"
        
        # Get the public URL
        local public_url="${SUPABASE_URL}/storage/v1/object/public/${BUCKET_NAME}/${remote_path}"
        echo "Public URL: $public_url"
        
        # Add to manifest
        echo "{\"name\":\"$file_name\",\"url\":\"$public_url\",\"size\":$(stat -f%z "$file_path" 2>/dev/null || stat -c%s "$file_path" 2>/dev/null || echo 0)}" >> "/tmp/${PROJECT_NAME}-${VERSION}-manifest.json"
    else
        # Upload failed, likely because file exists
        log_warn "File '$file_name' already exists in storage at $remote_path"
        
        if [[ "$OVERWRITE_ALL" == "true" ]]; then
            log_info "Overwriting existing file (overwrite all mode)"
            # Remove existing file and retry upload
            supabase storage rm "ss:///${BUCKET_NAME}/${remote_path}" --linked --experimental 2>/dev/null || true
            if supabase storage cp "$file_path" "ss:///${BUCKET_NAME}/${remote_path}" --linked --experimental; then
                log_info "Successfully uploaded $file_name"
                local public_url="${SUPABASE_URL}/storage/v1/object/public/${BUCKET_NAME}/${remote_path}"
                echo "Public URL: $public_url"
                echo "{\"name\":\"$file_name\",\"url\":\"$public_url\",\"size\":$(stat -f%z "$file_path" 2>/dev/null || stat -c%s "$file_path" 2>/dev/null || echo 0)}" >> "/tmp/${PROJECT_NAME}-${VERSION}-manifest.json"
            else
                log_error "Failed to upload $file_name after removing existing file"
                return 1
            fi
        elif [[ "$SKIP_ALL" == "true" ]]; then
            log_info "Skipping existing file (skip all mode)"
            return 0
        else
            if ! ask_overwrite "$file_name" "$remote_path"; then
                log_info "Skipping $file_name"
                return 0
            else
                # Remove existing file and retry upload
                log_info "Removing existing file..."
                supabase storage rm "ss:///${BUCKET_NAME}/${remote_path}" --linked --experimental 2>/dev/null || true
                if supabase storage cp "$file_path" "ss:///${BUCKET_NAME}/${remote_path}" --linked --experimental; then
                    log_info "Successfully uploaded $file_name"
                    local public_url="${SUPABASE_URL}/storage/v1/object/public/${BUCKET_NAME}/${remote_path}"
                    echo "Public URL: $public_url"
                    echo "{\"name\":\"$file_name\",\"url\":\"$public_url\",\"size\":$(stat -f%z "$file_path" 2>/dev/null || stat -c%s "$file_path" 2>/dev/null || echo 0)}" >> "/tmp/${PROJECT_NAME}-${VERSION}-manifest.json"
                else
                    log_error "Failed to upload $file_name after removing existing file"
                    return 1
                fi
            fi
        fi
    fi
}

# Create and upload release manifest
create_manifest() {
    local manifest_file="/tmp/${PROJECT_NAME}-${VERSION}-manifest.json"
    local manifest_path="releases/${VERSION}/manifest.json"
    
    log_info "Creating release manifest..."
    
    # Start JSON array
    echo "[" > "$manifest_file"
    
    # Process all files in the dist directory
    local first=true
    for file in dist/*; do
        if [[ -f "$file" ]]; then
            if [[ "$first" == "true" ]]; then
                first=false
            else
                echo "," >> "$manifest_file"
            fi
            
            local file_name=$(basename "$file")
            local remote_path="releases/${VERSION}/${file_name}"
            local public_url="${SUPABASE_URL}/storage/v1/object/public/${BUCKET_NAME}/${remote_path}"
            local file_size=$(stat -f%z "$file" 2>/dev/null || stat -c%s "$file" 2>/dev/null || echo 0)
            
            cat >> "$manifest_file" << EOF
  {
    "name": "$file_name",
    "url": "$public_url",
    "size": $file_size,
    "platform": "$(echo "$file_name" | sed 's/.*_\([^_]*\)_[^_]*$/\1/')",
    "arch": "$(echo "$file_name" | sed 's/.*_\([^_]*\)$/\1/')"
  }
EOF
        fi
    done
    
    # End JSON array
    echo "]" >> "$manifest_file"
    
    # Upload manifest
    log_info "Uploading release manifest..."
    
    # Try to upload first
    if supabase storage cp "$manifest_file" "ss:///${BUCKET_NAME}/${manifest_path}" --linked --experimental 2>/dev/null; then
        local manifest_url="${SUPABASE_URL}/storage/v1/object/public/${BUCKET_NAME}/${manifest_path}"
        log_info "Release manifest uploaded: $manifest_url"
        echo "MANIFEST_URL=$manifest_url" >> "${GITHUB_OUTPUT:-/dev/null}" 2>/dev/null || true
    else
        # Upload failed, likely because file exists
        log_warn "Manifest already exists in storage at $manifest_path"
        
        if [[ "$OVERWRITE_ALL" == "true" ]]; then
            log_info "Overwriting existing manifest (overwrite all mode)"
            supabase storage rm "ss:///${BUCKET_NAME}/${manifest_path}" --linked --experimental 2>/dev/null || true
            if supabase storage cp "$manifest_file" "ss:///${BUCKET_NAME}/${manifest_path}" --linked --experimental; then
                local manifest_url="${SUPABASE_URL}/storage/v1/object/public/${BUCKET_NAME}/${manifest_path}"
                log_info "Release manifest uploaded: $manifest_url"
                echo "MANIFEST_URL=$manifest_url" >> "${GITHUB_OUTPUT:-/dev/null}" 2>/dev/null || true
            else
                log_error "Failed to upload manifest after removing existing file"
                return 1
            fi
        elif [[ "$SKIP_ALL" == "true" ]]; then
            log_info "Skipping manifest (skip all mode)"
            return 0
        else
            if ! ask_overwrite "manifest.json" "$manifest_path"; then
                log_info "Skipping manifest"
                return 0
            else
                supabase storage rm "ss:///${BUCKET_NAME}/${manifest_path}" --linked --experimental 2>/dev/null || true
                if supabase storage cp "$manifest_file" "ss:///${BUCKET_NAME}/${manifest_path}" --linked --experimental; then
                    local manifest_url="${SUPABASE_URL}/storage/v1/object/public/${BUCKET_NAME}/${manifest_path}"
                    log_info "Release manifest uploaded: $manifest_url"
                    echo "MANIFEST_URL=$manifest_url" >> "${GITHUB_OUTPUT:-/dev/null}" 2>/dev/null || true
                else
                    log_error "Failed to upload manifest after removing existing file"
                    return 1
                fi
            fi
        fi
    fi
}

# Create installer script
create_installer() {
    local installer_file="/tmp/install.sh"
    local installer_path="install.sh"
    
    log_info "Creating installer script..."
    
    cat > "$installer_file" << 'EOF'
#!/bin/bash

# Improved Prod CLI Installer
# This script provides a better installation experience with PATH setup

set -euo pipefail

# Configuration
SUPABASE_URL="${SUPABASE_URL:-https://ciqiwllxffhbvayspynk.supabase.co}"
BUCKET_NAME="cli-binaries"
VERSION="${VERSION:-latest}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

# Detect platform and architecture
detect_platform() {
    local os arch
    
    case "$(uname -s)" in
        Linux*) os="linux" ;;
        Darwin*) os="darwin" ;;
        CYGWIN*|MINGW*|MSYS*) os="windows" ;;
        *) log_error "Unsupported operating system: $(uname -s)"; exit 1 ;;
    esac
    
    case "$(uname -m)" in
        x86_64|amd64) arch="amd64" ;;
        arm64|aarch64) arch="arm64" ;;
        *) log_error "Unsupported architecture: $(uname -m)"; exit 1 ;;
    esac
    
    echo "${os}_${arch}"
}

# Detect shell
detect_shell() {
    if [[ -n "${ZSH_VERSION:-}" ]]; then
        echo "zsh"
    elif [[ -n "${BASH_VERSION:-}" ]]; then
        echo "bash"
    elif [[ -n "${FISH_VERSION:-}" ]]; then
        echo "fish"
    else
        # Default fallback
        echo "bash"
    fi
}

# Add to PATH in shell profile
setup_path() {
    local install_dir="$1"
    local shell=$(detect_shell)
    local profile_file=""
    
    case "$shell" in
        zsh)
            profile_file="$HOME/.zshrc"
            ;;
        bash)
            profile_file="$HOME/.bashrc"
            ;;
        fish)
            profile_file="$HOME/.config/fish/config.fish"
            ;;
    esac
    
    if [[ -n "$profile_file" ]]; then
        # Check if PATH is already set up
        if ! grep -q "export PATH.*$install_dir" "$profile_file" 2>/dev/null; then
            log_info "Adding $install_dir to PATH in $profile_file"
            echo "" >> "$profile_file"
            echo "# Prod CLI" >> "$profile_file"
            echo "export PATH=\"$install_dir:\$PATH\"" >> "$profile_file"
            log_success "PATH updated in $profile_file"
            log_warn "Please restart your terminal or run: source $profile_file"
        else
            log_info "PATH already configured in $profile_file"
        fi
    else
        log_warn "Could not detect shell profile. Please add $install_dir to your PATH manually."
    fi
}

# Download and install binary
install_binary() {
    local platform="$1"
    # Use the actual version from the environment or default to latest
    local actual_version="${VERSION}"
    if [[ "$actual_version" == "latest" ]]; then
        # For now, use the test version since that's what we have
        actual_version="v0.0.0-test"
    fi
    local manifest_url="${SUPABASE_URL}/storage/v1/object/public/${BUCKET_NAME}/releases/${actual_version}/manifest.json"
    local binary_name="prod_${actual_version}_${platform}"
    
    # Add file extension for Windows
    if [[ "$platform" == *"windows"* ]]; then
        binary_name="${binary_name}.exe"
    fi
    
    log_info "Downloading binary for platform: $platform"
    
    # Download manifest
    log_info "Fetching manifest from: $manifest_url"
    local manifest=$(curl -s "$manifest_url")
    local curl_exit_code=$?
    
    if [[ $curl_exit_code -ne 0 ]]; then
        log_error "Failed to download manifest from $manifest_url (curl exit code: $curl_exit_code)"
        exit 1
    fi
    
    # Check if manifest is valid JSON
    if ! echo "$manifest" | jq . >/dev/null 2>&1; then
        log_error "Invalid JSON in manifest response"
        log_error "Response: $manifest"
        exit 1
    fi
    
    # Debug: Show manifest structure
    log_info "Manifest content: $manifest"
    
    # Find the correct binary URL - handle different manifest structures
    local binary_url=""
    
    # Try to find binary by name pattern
    if echo "$manifest" | jq -e '.[] | select(.name | contains("'$platform'"))' >/dev/null 2>&1; then
        binary_url=$(echo "$manifest" | jq -r ".[] | select(.name | contains(\"$platform\")) | .url")
    elif echo "$manifest" | jq -e '.[] | select(.name | contains("darwin"))' >/dev/null 2>&1; then
        # Fallback: try to find any darwin binary for macOS
        binary_url=$(echo "$manifest" | jq -r ".[] | select(.name | contains(\"darwin\")) | .url" | head -1)
    fi
    
    if [[ -z "$binary_url" || "$binary_url" == "null" ]]; then
        log_error "Binary not found for platform: $platform"
        log_error "Available platforms:"
        if echo "$manifest" | jq -e '.[] | .name' >/dev/null 2>&1; then
            echo "$manifest" | jq -r '.[] | .name' | sed 's/^/  - /'
        else
            log_error "Could not parse manifest structure"
            log_error "Manifest: $manifest"
        fi
        exit 1
    fi
    
    log_info "Downloading from: $binary_url"
    
    # Create temp directory
    local temp_dir=$(mktemp -d)
    local archive_file="$temp_dir/$(basename "$binary_url")"
    
    # Download binary
    if ! curl -L -o "$archive_file" "$binary_url"; then
        log_error "Failed to download binary"
        exit 1
    fi
    
    # Determine install directory
    local install_dir="/usr/local/bin"
    if [[ ! -w "$install_dir" ]]; then
        install_dir="$HOME/.local/bin"
        mkdir -p "$install_dir"
        log_info "Installing to $install_dir (user directory)"
    else
        log_info "Installing to $install_dir (system directory)"
    fi
    
    # Extract and install
    cd "$temp_dir"
    if [[ "$archive_file" == *.tar.gz ]]; then
        tar -xzf "$archive_file"
    elif [[ "$archive_file" == *.zip ]]; then
        unzip -q "$archive_file"
    fi
    
    # Find the binary (use -perm +x for macOS compatibility)
    local binary_file=$(find . -name "prod*" -type f -perm +x | head -1)
    if [[ -z "$binary_file" ]]; then
        # Fallback: look for any file named prod
        binary_file=$(find . -name "prod" -type f | head -1)
        if [[ -z "$binary_file" ]]; then
            log_error "Binary not found in archive"
            log_error "Files in archive:"
            find . -type f | sed 's/^/  - /'
            exit 1
        fi
    fi
    
    # Install binary
    chmod +x "$binary_file"
    cp "$binary_file" "$install_dir/prod"
    
    log_success "Installed prod CLI to $install_dir/prod"
    
    # Setup PATH if needed
    if [[ "$install_dir" == "$HOME/.local/bin" ]]; then
        setup_path "$install_dir"
    fi
    
    # Cleanup
    rm -rf "$temp_dir"
    
    return 0
}

# Create uninstall script
create_uninstaller() {
    local install_dir="$1"
    local uninstaller_file="$HOME/.prod-uninstall.sh"
    
    cat > "$uninstaller_file" << UNINSTALL_EOF
#!/bin/bash
# Prod CLI Uninstaller

echo "Uninstalling Prod CLI..."

# Remove binary
if [[ -f "$install_dir/prod" ]]; then
    rm -f "$install_dir/prod"
    echo "Removed $install_dir/prod"
fi

# Remove PATH from shell profiles
for profile in "\$HOME/.bashrc" "\$HOME/.zshrc" "\$HOME/.config/fish/config.fish"; do
    if [[ -f "\$profile" ]]; then
        sed -i.bak '/# Prod CLI/,/export PATH.*prod/d' "\$profile"
        echo "Removed PATH from \$profile"
    fi
done

# Remove uninstaller
rm -f "\$0"

echo "Prod CLI uninstalled successfully!"
UNINSTALL_EOF
    
    chmod +x "$uninstaller_file"
    log_info "Created uninstaller at $uninstaller_file"
}

# Main installation
main() {
    log_info "Installing Prod CLI..."
    
    # Check for required tools
    if ! command -v curl &> /dev/null; then
        log_error "curl is required but not installed"
        exit 1
    fi
    
    if ! command -v jq &> /dev/null; then
        log_error "jq is required but not installed"
        exit 1
    fi
    
    local platform=$(detect_platform)
    local install_dir="/usr/local/bin"
    
    # Try system install first
    if [[ ! -w "/usr/local/bin" ]]; then
        install_dir="$HOME/.local/bin"
        mkdir -p "$install_dir"
    fi
    
    install_binary "$platform"
    create_uninstaller "$install_dir"
    
    log_success "Installation completed!"
    log_info "Run 'prod --version' to verify installation"
    log_info "Run '$HOME/.prod-uninstall.sh' to uninstall"
}

main "$@"
EOF
    
    # Upload installer
    log_info "Uploading installer script..."
    
    # Try to upload first
    if supabase storage cp "$installer_file" "ss:///${BUCKET_NAME}/${installer_path}" --linked --experimental 2>/dev/null; then
        local installer_url="${SUPABASE_URL}/storage/v1/object/public/${BUCKET_NAME}/${installer_path}"
        log_info "Installer script uploaded: $installer_url"
        echo "INSTALLER_URL=$installer_url" >> "${GITHUB_OUTPUT:-/dev/null}" 2>/dev/null || true
    else
        # Upload failed, likely because file exists
        log_warn "Installer already exists in storage at $installer_path"
        
        if [[ "$OVERWRITE_ALL" == "true" ]]; then
            log_info "Overwriting existing installer (overwrite all mode)"
            supabase storage rm "ss:///${BUCKET_NAME}/${installer_path}" --linked --experimental 2>/dev/null || true
            if supabase storage cp "$installer_file" "ss:///${BUCKET_NAME}/${installer_path}" --linked --experimental; then
                local installer_url="${SUPABASE_URL}/storage/v1/object/public/${BUCKET_NAME}/${installer_path}"
                log_info "Installer script uploaded: $installer_url"
                echo "INSTALLER_URL=$installer_url" >> "${GITHUB_OUTPUT:-/dev/null}" 2>/dev/null || true
            else
                log_error "Failed to upload installer after removing existing file"
                return 1
            fi
        elif [[ "$SKIP_ALL" == "true" ]]; then
            log_info "Skipping installer (skip all mode)"
            return 0
        else
            if ! ask_overwrite "install.sh" "$installer_path"; then
                log_info "Skipping installer"
                return 0
            else
                supabase storage rm "ss:///${BUCKET_NAME}/${installer_path}" --linked --experimental 2>/dev/null || true
                if supabase storage cp "$installer_file" "ss:///${BUCKET_NAME}/${installer_path}" --linked --experimental; then
                    local installer_url="${SUPABASE_URL}/storage/v1/object/public/${BUCKET_NAME}/${installer_path}"
                    log_info "Installer script uploaded: $installer_url"
                    echo "INSTALLER_URL=$installer_url" >> "${GITHUB_OUTPUT:-/dev/null}" 2>/dev/null || true
                else
                    log_error "Failed to upload installer after removing existing file"
                    return 1
                fi
            fi
        fi
    fi
}

# Main function
main() {
    log_info "Starting Supabase storage upload for version $VERSION"
    log_info "This script will ask for confirmation if files already exist in storage"
    log_info "You can choose to overwrite, skip, or apply the decision to all remaining files"
    echo ""
    
    # Load environment variables
    load_env
    
    # Check environment
    check_env
    
    # Install Supabase CLI if needed
    install_supabase_cli
    
    # Create bucket
    create_bucket
    
    # Upload all files in dist directory
    log_info "Uploading binaries from dist/ directory..."
    for file in dist/*; do
        if [[ -f "$file" ]]; then
            # Skip metadata files that aren't needed for distribution
            if [[ "$file" == *"artifacts.json" ]] || [[ "$file" == *"config.yaml" ]] || [[ "$file" == *"metadata.json" ]]; then
                log_info "Skipping metadata file: $(basename "$file")"
                continue
            fi
            upload_file "$file"
        fi
    done
    
    # Create and upload manifest
    create_manifest
    
    # Create and upload installer
    create_installer
    
    log_info "Upload completed successfully!"
    log_info "Binaries are available at: ${SUPABASE_URL}/storage/v1/object/public/${BUCKET_NAME}/releases/${VERSION}/"
}

# Run main function
main "$@"
