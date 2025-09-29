#!/bin/bash

# Improved Prod CLI Installer
# This script provides a better installation experience with PATH setup

set -euo pipefail

# Configuration
SUPABASE_URL="${SUPABASE_URL:-https://PROJECT_REDACTED.supabase.co}"
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
    local manifest_url="${SUPABASE_URL}/storage/v1/object/public/${BUCKET_NAME}/releases/${VERSION}/manifest.json"
    local binary_name="prod_${VERSION}_${platform}"
    
    # Add file extension for Windows
    if [[ "$platform" == *"windows"* ]]; then
        binary_name="${binary_name}.exe"
    fi
    
    log_info "Downloading binary for platform: $platform"
    
    # Download manifest
    local manifest=$(curl -s "$manifest_url")
    if [[ $? -ne 0 ]]; then
        log_error "Failed to download manifest from $manifest_url"
        exit 1
    fi
    
    # Find the correct binary URL
    local binary_url=$(echo "$manifest" | jq -r ".[] | select(.name == \"${binary_name}.tar.gz\" or .name == \"${binary_name}.zip\") | .url")
    
    if [[ -z "$binary_url" || "$binary_url" == "null" ]]; then
        log_error "Binary not found for platform: $platform"
        log_error "Available platforms:"
        echo "$manifest" | jq -r '.[] | .name' | sed 's/^/  - /'
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
    
    # Find the binary
    local binary_file=$(find . -name "prod*" -type f -executable | head -1)
    if [[ -z "$binary_file" ]]; then
        log_error "Binary not found in archive"
        exit 1
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
    
    cat > "$uninstaller_file" << EOF
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
EOF
    
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
