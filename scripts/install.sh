#!/bin/bash
#
# prod CLI installer.
#
# Downloads a release archive from GitHub Releases, verifies it against the
# published checksums.txt, and installs the `prod` binary onto your PATH.
#
#   curl -sSL https://raw.githubusercontent.com/pushtoprodai/prod-cli/main/scripts/install.sh | sh
#
# Environment overrides:
#   VERSION      release tag to install (default: latest, e.g. v0.1.0)
#   INSTALL_DIR  target directory (default: /usr/local/bin, else ~/.local/bin)
#   REPO         owner/name (default: pushtoprodai/prod-cli)

set -euo pipefail

# Configuration
REPO="${REPO:-pushtoprodai/prod-cli}"
VERSION="${VERSION:-latest}"
BINARY_NAME="prod"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info()    { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn()    { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $1" >&2; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }

# Detect platform and architecture -> "os_arch" matching goreleaser archive names.
detect_platform() {
    local os arch

    case "$(uname -s)" in
        Linux*)  os="linux" ;;
        Darwin*) os="darwin" ;;
        CYGWIN*|MINGW*|MSYS*) os="windows" ;;
        *) log_error "Unsupported operating system: $(uname -s)"; exit 1 ;;
    esac

    case "$(uname -m)" in
        x86_64|amd64) arch="amd64" ;;
        arm64|aarch64) arch="arm64" ;;
        *) log_error "Unsupported architecture: $(uname -m)"; exit 1 ;;
    esac

    # Only darwin archives are published today (BAML CGO cross-compile blocks
    # linux/windows — see docs/DISTRIBUTION.md). Fail early with a clear message.
    if [[ "$os" != "darwin" ]]; then
        log_error "No published binary for ${os}/${arch} yet."
        log_error "Only macOS archives are released today (BAML's native CGO dep"
        log_error "blocks linux/windows cross-compile). Build from source instead:"
        log_error "  git clone https://github.com/${REPO} && cd prod-cli/cli && make build"
        exit 1
    fi

    echo "${os}_${arch}"
}

# Detect shell profile for PATH setup.
detect_profile() {
    if [[ -n "${ZSH_VERSION:-}" ]] || [[ "${SHELL:-}" == */zsh ]]; then
        echo "$HOME/.zshrc"
    elif [[ -n "${BASH_VERSION:-}" ]] || [[ "${SHELL:-}" == */bash ]]; then
        echo "$HOME/.bashrc"
    else
        echo "$HOME/.profile"
    fi
}

# Add install dir to PATH in the shell profile if it isn't already there.
setup_path() {
    local install_dir="$1"
    local profile_file
    profile_file="$(detect_profile)"

    if grep -qs "$install_dir" "$profile_file" 2>/dev/null; then
        log_info "PATH already configured in $profile_file"
        return
    fi

    log_info "Adding $install_dir to PATH in $profile_file"
    {
        echo ""
        echo "# prod CLI"
        echo "export PATH=\"$install_dir:\$PATH\""
    } >> "$profile_file"
    log_warn "Restart your terminal or run: source $profile_file"
}

# Resolve the release tag (turn "latest" into a concrete tag via the GitHub API).
resolve_version() {
    if [[ "$VERSION" != "latest" ]]; then
        echo "$VERSION"
        return
    fi

    local api_url="https://api.github.com/repos/${REPO}/releases/latest"
    local tag
    # Parse the tag_name without requiring jq.
    tag="$(curl -sSL "$api_url" | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"

    if [[ -z "$tag" ]]; then
        log_error "Could not determine the latest release tag from $api_url"
        log_error "Set VERSION explicitly, e.g. VERSION=v0.1.0"
        exit 1
    fi
    echo "$tag"
}

# Download, verify, extract, install.
install_binary() {
    local platform="$1"
    local tag="$2"

    # goreleaser strips a leading "v" from {{ .Version }} in archive names.
    local version="${tag#v}"
    local archive="${BINARY_NAME}_${version}_${platform}.tar.gz"
    local checksums="${BINARY_NAME}_${version}_checksums.txt"
    local base_url="https://github.com/${REPO}/releases/download/${tag}"

    local temp_dir
    temp_dir="$(mktemp -d)"
    trap 'rm -rf "$temp_dir"' EXIT

    log_info "Downloading $archive ($tag)"
    if ! curl -fSL -o "$temp_dir/$archive" "${base_url}/${archive}"; then
        log_error "Failed to download ${base_url}/${archive}"
        log_error "Check that release $tag has an asset for platform $platform."
        exit 1
    fi

    # Verify against checksums.txt when available (best-effort but on by default).
    if curl -fSL -o "$temp_dir/$checksums" "${base_url}/${checksums}" 2>/dev/null; then
        log_info "Verifying checksum"
        local expected actual
        expected="$(grep " ${archive}\$" "$temp_dir/$checksums" | awk '{print $1}')"
        if [[ -z "$expected" ]]; then
            log_warn "Checksum for $archive not found in $checksums; skipping verification"
        else
            if command -v sha256sum >/dev/null 2>&1; then
                actual="$(sha256sum "$temp_dir/$archive" | awk '{print $1}')"
            else
                actual="$(shasum -a 256 "$temp_dir/$archive" | awk '{print $1}')"
            fi
            if [[ "$expected" != "$actual" ]]; then
                log_error "Checksum mismatch for $archive"
                log_error "  expected: $expected"
                log_error "  actual:   $actual"
                exit 1
            fi
            log_success "Checksum verified"
        fi
    else
        log_warn "Could not fetch $checksums; skipping checksum verification"
    fi

    log_info "Extracting"
    tar -xzf "$temp_dir/$archive" -C "$temp_dir"

    if [[ ! -f "$temp_dir/$BINARY_NAME" ]]; then
        log_error "Binary '$BINARY_NAME' not found in archive"
        exit 1
    fi

    # Pick install dir: /usr/local/bin if writable, else ~/.local/bin.
    local install_dir="${INSTALL_DIR:-}"
    if [[ -z "$install_dir" ]]; then
        if [[ -w "/usr/local/bin" ]]; then
            install_dir="/usr/local/bin"
        else
            install_dir="$HOME/.local/bin"
        fi
    fi
    mkdir -p "$install_dir"

    chmod +x "$temp_dir/$BINARY_NAME"
    if ! mv "$temp_dir/$BINARY_NAME" "$install_dir/$BINARY_NAME" 2>/dev/null; then
        log_info "Elevating with sudo to write to $install_dir"
        sudo mv "$temp_dir/$BINARY_NAME" "$install_dir/$BINARY_NAME"
    fi

    log_success "Installed $BINARY_NAME to $install_dir/$BINARY_NAME"

    # Ensure it's on PATH.
    case ":$PATH:" in
        *":$install_dir:"*) ;;
        *) setup_path "$install_dir" ;;
    esac
}

main() {
    log_info "Installing prod CLI from github.com/${REPO}"

    if ! command -v curl >/dev/null 2>&1; then
        log_error "curl is required but not installed"
        exit 1
    fi
    if ! command -v tar >/dev/null 2>&1; then
        log_error "tar is required but not installed"
        exit 1
    fi

    local platform tag
    platform="$(detect_platform)"
    tag="$(resolve_version)"

    install_binary "$platform" "$tag"

    log_success "Done. Run 'prod --version' to verify."
}

main "$@"
