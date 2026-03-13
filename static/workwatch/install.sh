#!/bin/bash
# WorkWatch Installer
# Usage: curl -fsSL https://sagnikbhowmick.com/workwatch/install.sh | bash
#
# This script downloads and installs the WorkWatch binary for macOS.

set -euo pipefail

VERSION="${WORKWATCH_VERSION:-1.0.0}"
INSTALL_DIR="${WORKWATCH_INSTALL_DIR:-/usr/local/bin}"
BINARY_NAME="workwatch"
BASE_URL="https://sagnikbhowmick.com/workwatch/releases"
TEMP_DIR=$(mktemp -d)

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

cleanup() {
    rm -rf "$TEMP_DIR"
}
trap cleanup EXIT

info() { echo -e "${CYAN}ℹ${NC} $1"; }
success() { echo -e "${GREEN}✅${NC} $1"; }
warn() { echo -e "${YELLOW}⚠${NC} $1"; }
error() { echo -e "${RED}❌${NC} $1"; exit 1; }

# ── Fallback: install from source ─────────────────────────────────

install_from_source() {
    info "Installing from source using pip..."

    if ! command -v python3 &>/dev/null; then
        error "Python 3 is required for source install but was not found."
    fi

    PYTHON_VERSION=$(python3 -c 'import sys; print(f"{sys.version_info.major}.{sys.version_info.minor}")')
    info "Found Python ${PYTHON_VERSION}"

    # Clone or download source
    if command -v git &>/dev/null; then
        info "Cloning WorkWatch repository..."
        git clone --depth 1 https://github.com/sagnikb7/workwatch.git "${TEMP_DIR}/workwatch-src" 2>/dev/null || {
            error "Failed to clone repository."
        }
    else
        info "Downloading source archive..."
        if [[ "$DOWNLOADER" == "curl" ]]; then
            curl -fsSL -o "${TEMP_DIR}/source.tar.gz" "https://github.com/sagnikb7/workwatch/archive/refs/tags/v${VERSION}.tar.gz"
        else
            wget -q -O "${TEMP_DIR}/source.tar.gz" "https://github.com/sagnikb7/workwatch/archive/refs/tags/v${VERSION}.tar.gz"
        fi
        tar -xzf "${TEMP_DIR}/source.tar.gz" -C "${TEMP_DIR}"
        mv "${TEMP_DIR}/workwatch-${VERSION}" "${TEMP_DIR}/workwatch-src"
    fi

    cd "${TEMP_DIR}/workwatch-src"
    pip3 install . --quiet 2>/dev/null || pip3 install . --user --quiet
    success "WorkWatch installed from source via pip."
}

# ── Main installer ────────────────────────────────────────────────

echo ""
echo -e "${CYAN}╔══════════════════════════════════════════╗${NC}"
echo -e "${CYAN}║${NC}     ${BOLD}WorkWatch Installer v${VERSION}${NC}            ${CYAN}║${NC}"
echo -e "${CYAN}║${NC}     Auto-sleep after your work hours     ${CYAN}║${NC}"
echo -e "${CYAN}╚══════════════════════════════════════════╝${NC}"
echo ""

# ── Preflight checks ──────────────────────────────────────────────

# macOS only
if [[ "$(uname -s)" != "Darwin" ]]; then
    error "WorkWatch only runs on macOS (requires Mail.app and pmset)."
fi

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  ARCH_LABEL="x86_64" ;;
    arm64)   ARCH_LABEL="arm64"  ;;
    *)       error "Unsupported architecture: $ARCH" ;;
esac

info "Detected macOS ($ARCH_LABEL)"

# Check for curl or wget
if command -v curl &>/dev/null; then
    DOWNLOADER="curl"
elif command -v wget &>/dev/null; then
    DOWNLOADER="wget"
else
    error "Neither curl nor wget found. Please install one and retry."
fi

# ── Download ──────────────────────────────────────────────────────

TAR_NAME="workwatch-${VERSION}-darwin-${ARCH_LABEL}.tar.gz"
DOWNLOAD_URL="${BASE_URL}/${TAR_NAME}"
CHECKSUM_URL="${BASE_URL}/${TAR_NAME}.sha256"

info "Downloading WorkWatch v${VERSION}..."

if [[ "$DOWNLOADER" == "curl" ]]; then
    HTTP_CODE=$(curl -fsSL -w "%{http_code}" -o "${TEMP_DIR}/${TAR_NAME}" "$DOWNLOAD_URL" 2>/dev/null || true)
    if [[ "$HTTP_CODE" != "200" ]] && [[ ! -f "${TEMP_DIR}/${TAR_NAME}" ]]; then
        warn "Binary download failed (HTTP $HTTP_CODE). Falling back to source install..."
        install_from_source
        exit 0
    fi
else
    wget -q -O "${TEMP_DIR}/${TAR_NAME}" "$DOWNLOAD_URL" 2>/dev/null || {
        warn "Binary download failed. Falling back to source install..."
        install_from_source
        exit 0
    }
fi

# ── Verify checksum (if available) ───────────────────────────────

if [[ "$DOWNLOADER" == "curl" ]]; then
    curl -fsSL -o "${TEMP_DIR}/checksum.sha256" "$CHECKSUM_URL" 2>/dev/null || true
else
    wget -q -O "${TEMP_DIR}/checksum.sha256" "$CHECKSUM_URL" 2>/dev/null || true
fi

if [[ -f "${TEMP_DIR}/checksum.sha256" ]] && [[ -s "${TEMP_DIR}/checksum.sha256" ]]; then
    info "Verifying checksum..."
    EXPECTED=$(awk '{print $1}' "${TEMP_DIR}/checksum.sha256")
    ACTUAL=$(shasum -a 256 "${TEMP_DIR}/${TAR_NAME}" | awk '{print $1}')
    if [[ "$EXPECTED" != "$ACTUAL" ]]; then
        error "Checksum mismatch! Expected: $EXPECTED, Got: $ACTUAL"
    fi
    success "Checksum verified."
else
    warn "No checksum file found — skipping verification."
fi

# ── Extract & Install ─────────────────────────────────────────────

info "Extracting..."
tar -xzf "${TEMP_DIR}/${TAR_NAME}" -C "${TEMP_DIR}"

if [[ ! -f "${TEMP_DIR}/${BINARY_NAME}" ]]; then
    error "Binary not found in archive."
fi

chmod +x "${TEMP_DIR}/${BINARY_NAME}"

# Install to target directory
info "Installing to ${INSTALL_DIR}/${BINARY_NAME}..."

if [[ -w "$INSTALL_DIR" ]]; then
    mv "${TEMP_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
else
    warn "Need sudo to write to ${INSTALL_DIR}"
    sudo mv "${TEMP_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
fi

# ── Verify installation ──────────────────────────────────────────

if command -v workwatch &>/dev/null; then
    success "WorkWatch v${VERSION} installed successfully!"
else
    warn "Installed but '${INSTALL_DIR}' may not be in your PATH."
    echo -e "   Add this to your shell profile:"
    echo -e "   ${BOLD}export PATH=\"${INSTALL_DIR}:\$PATH\"${NC}"
fi

# ── Post-install setup ────────────────────────────────────────────

echo ""
echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BOLD}Getting Started:${NC}"
echo ""
echo -e "  1. Make sure Apple Mail is set up with your Gmail"
echo -e "  2. Run ${BOLD}workwatch${NC} to start the countdown timer"
echo -e "  3. Run ${BOLD}workwatch log${NC} to view your monthly attendance"
echo ""
echo -e "  Config: ${BOLD}~/.workwatch.json${NC} (auto-created on first run)"
echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""
