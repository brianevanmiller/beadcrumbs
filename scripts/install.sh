#!/usr/bin/env bash
#
# beadcrumbs (bdc) installation script
# Usage: curl -fsSL https://raw.githubusercontent.com/beadcrumbs/beadcrumbs/main/scripts/install.sh | bash
#

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${BLUE}==>${NC} $1"
}

log_success() {
    echo -e "${GREEN}==>${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}==>${NC} $1"
}

log_error() {
    echo -e "${RED}Error:${NC} $1" >&2
}

# Detect OS and architecture
detect_platform() {
    local os arch

    case "$(uname -s)" in
        Darwin)
            os="darwin"
            ;;
        Linux)
            os="linux"
            ;;
        FreeBSD)
            os="freebsd"
            ;;
        *)
            log_error "Unsupported operating system: $(uname -s)"
            exit 1
            ;;
    esac

    case "$(uname -m)" in
        x86_64|amd64)
            arch="amd64"
            ;;
        aarch64|arm64)
            arch="arm64"
            ;;
        armv7*|armv6*|armhf|arm)
            arch="arm"
            ;;
        *)
            log_error "Unsupported architecture: $(uname -m)"
            exit 1
            ;;
    esac

    echo "${os}_${arch}"
}

# Re-sign binary for macOS to avoid slow Gatekeeper checks
resign_for_macos() {
    local binary_path=$1

    if [[ "$(uname -s)" != "Darwin" ]]; then
        return 0
    fi

    if ! command -v codesign &> /dev/null; then
        return 0
    fi

    log_info "Re-signing binary for macOS..."
    codesign --remove-signature "$binary_path" 2>/dev/null || true
    if codesign --force --sign - "$binary_path"; then
        log_success "Binary re-signed for this machine"
    fi
}

# Check if Go is installed
check_go() {
    if command -v go &> /dev/null; then
        local go_version=$(go version | awk '{print $3}' | sed 's/go//')
        log_info "Go detected: $(go version)"
        return 0
    else
        return 1
    fi
}

# Install using go install
install_with_go() {
    log_info "Installing bdc using 'go install'..."

    if go install github.com/brianevanmiller/beadcrumbs/cmd/bdc@latest; then
        log_success "bdc installed successfully via go install"

        local gobin
        gobin=$(go env GOBIN 2>/dev/null || true)
        if [ -n "$gobin" ]; then
            bin_dir="$gobin"
        else
            bin_dir="$(go env GOPATH)/bin"
        fi

        resign_for_macos "$bin_dir/bdc"

        if [[ ":$PATH:" != *":$bin_dir:"* ]]; then
            log_warning "$bin_dir is not in your PATH"
            echo ""
            echo "Add this to your shell profile (~/.bashrc, ~/.zshrc, etc.):"
            echo "  export PATH=\"\$PATH:$bin_dir\""
            echo ""
        fi

        return 0
    else
        log_error "go install failed"
        return 1
    fi
}

# Build from source
build_from_source() {
    log_info "Building bdc from source..."

    local tmp_dir
    tmp_dir=$(mktemp -d)

    cd "$tmp_dir"
    log_info "Cloning repository..."

    if git clone --depth 1 https://github.com/brianevanmiller/beadcrumbs.git; then
        cd beadcrumbs
        log_info "Building binary..."

        if go build -o bdc ./cmd/bdc; then
            local install_dir
            if [[ -w /usr/local/bin ]]; then
                install_dir="/usr/local/bin"
            else
                install_dir="$HOME/.local/bin"
                mkdir -p "$install_dir"
            fi

            log_info "Installing to $install_dir..."
            if [[ -w "$install_dir" ]]; then
                mv bdc "$install_dir/"
            else
                sudo mv bdc "$install_dir/"
            fi

            resign_for_macos "$install_dir/bdc"

            log_success "bdc installed to $install_dir/bdc"

            if [[ ":$PATH:" != *":$install_dir:"* ]]; then
                log_warning "$install_dir is not in your PATH"
                echo ""
                echo "Add this to your shell profile (~/.bashrc, ~/.zshrc, etc.):"
                echo "  export PATH=\"\$PATH:$install_dir\""
                echo ""
            fi

            cd - > /dev/null || cd "$HOME"
            rm -rf "$tmp_dir"
            return 0
        else
            log_error "Build failed"
            cd - > /dev/null || cd "$HOME"
            rm -rf "$tmp_dir"
            return 1
        fi
    else
        log_error "Failed to clone repository"
        rm -rf "$tmp_dir"
        return 1
    fi
}

# Verify installation
verify_installation() {
    if command -v bdc &> /dev/null; then
        log_success "bdc is installed and ready!"
        echo ""
        bdc --help | head -5
        echo ""
        echo "Get started:"
        echo "  cd your-project"
        echo "  bdc init"
        echo "  bdc capture \"Your first insight\" --hypothesis"
        echo ""
        return 0
    else
        log_error "bdc was installed but is not in PATH"
        return 1
    fi
}

# Main installation flow
main() {
    echo ""
    echo "beadcrumbs (bdc) Installer"
    echo ""

    log_info "Detecting platform..."
    local platform
    platform=$(detect_platform)
    log_info "Platform: $platform"

    # Try go install first (most reliable for Go projects)
    if check_go; then
        if install_with_go; then
            verify_installation
            exit 0
        fi
    fi

    # Try building from source
    log_warning "Falling back to building from source..."

    if ! check_go; then
        log_warning "Go is not installed"
        echo ""
        echo "bdc requires Go to install. You can:"
        echo "  1. Install Go from https://go.dev/dl/"
        echo "  2. Use your package manager:"
        echo "     - macOS: brew install go"
        echo "     - Ubuntu/Debian: sudo apt install golang"
        echo ""
        echo "After installing Go, run this script again."
        exit 1
    fi

    if build_from_source; then
        verify_installation
        exit 0
    fi

    # All methods failed
    log_error "Installation failed"
    echo ""
    echo "Manual installation:"
    echo "  1. Install Go from https://go.dev/dl/"
    echo "  2. Run: go install github.com/brianevanmiller/beadcrumbs/cmd/bdc@latest"
    echo ""
    echo "Or build from source:"
    echo "  git clone https://github.com/brianevanmiller/beadcrumbs.git"
    echo "  cd beadcrumbs"
    echo "  go build -o bdc ./cmd/bdc/"
    echo "  sudo mv bdc /usr/local/bin/"
    echo ""
    exit 1
}

main "$@"
