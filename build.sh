#!/usr/bin/env bash
# build.sh — Janus Linux / WSL2 build script
# Downloads pre-built llama.cpp Vulkan .so files from GitHub Releases,
# then builds the janus binary.
#
# Usage:
#   ./build.sh                      # auto-detect latest llama.cpp release
#   ./build.sh --version b5000      # pin a specific release tag
#   ./build.sh --skip-download      # use .so files already in lib/linux/
#
# WSL2 note: Vulkan is bridged from Windows via the GPU driver.
#   NVIDIA RTX on WSL2 — Vulkan works out of the box with recent drivers.
#   Run 'vulkaninfo' inside WSL2 to confirm your GPU is visible.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_DIR="$ROOT/lib/linux"
DIST_DIR="$ROOT/dist"
BIN_NAME="janus"
LLAMA_VERSION=""
SKIP_DOWNLOAD=false

# ---------------------------------------------------------------------------
# Parse arguments
# ---------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
    case "$1" in
        --version) LLAMA_VERSION="$2"; shift 2 ;;
        --skip-download) SKIP_DOWNLOAD=true; shift ;;
        *) echo "Unknown argument: $1"; exit 1 ;;
    esac
done

# ---------------------------------------------------------------------------
# Detect WSL2 vs native Linux
# ---------------------------------------------------------------------------
IS_WSL2=false
if grep -qi "microsoft" /proc/version 2>/dev/null; then
    IS_WSL2=true
    echo "Detected WSL2 environment."
fi

# ---------------------------------------------------------------------------
# 1. Resolve llama.cpp release version
# ---------------------------------------------------------------------------
get_latest_release() {
    curl -fsSL \
        -H "User-Agent: janus-build-script" \
        "https://api.github.com/repos/ggerganov/llama.cpp/releases/latest" \
    | grep '"tag_name"' \
    | sed 's/.*"tag_name": "\(.*\)".*/\1/'
}

if [ "$SKIP_DOWNLOAD" = false ]; then
    if [ -z "$LLAMA_VERSION" ]; then
        echo "Fetching latest llama.cpp release tag..."
        LLAMA_VERSION="$(get_latest_release || echo 'b5000')"
    fi
    echo "Using llama.cpp $LLAMA_VERSION"
fi

# ---------------------------------------------------------------------------
# 2. Download and extract Vulkan .so files
# ---------------------------------------------------------------------------
if [ "$SKIP_DOWNLOAD" = false ]; then
    ZIP_NAME="llama-${LLAMA_VERSION}-bin-ubuntu-vulkan-x64.zip"
    ZIP_URL="https://github.com/ggerganov/llama.cpp/releases/download/${LLAMA_VERSION}/${ZIP_NAME}"
    ZIP_PATH="/tmp/${ZIP_NAME}"

    echo "Downloading ${ZIP_NAME}..."
    curl -fL --progress-bar -o "$ZIP_PATH" "$ZIP_URL"

    mkdir -p "$LIB_DIR"

    EXTRACT_DIR="/tmp/llama-extract-$$"
    mkdir -p "$EXTRACT_DIR"
    unzip -q "$ZIP_PATH" -d "$EXTRACT_DIR"

    # Copy all .so files regardless of sub-folder structure in the zip
    find "$EXTRACT_DIR" -name "*.so" -exec cp {} "$LIB_DIR/" \;

    rm -rf "$EXTRACT_DIR" "$ZIP_PATH"

    echo "Shared libraries extracted to $LIB_DIR:"
    ls -lh "$LIB_DIR"/*.so 2>/dev/null || echo "  (none found — check zip contents)"
else
    echo "Skipping download (--skip-download)."
fi

# Verify required library is present
if [ ! -f "$LIB_DIR/libllama.so" ]; then
    echo "ERROR: $LIB_DIR/libllama.so not found."
    echo "Run without --skip-download to fetch it."
    exit 1
fi

# ---------------------------------------------------------------------------
# 3. Build the Go binary
# ---------------------------------------------------------------------------
echo "Running go mod tidy..."
go mod tidy

mkdir -p "$DIST_DIR"

echo "Building janus..."
go build -ldflags "-s -w" -o "$DIST_DIR/$BIN_NAME" ./cmd/janus

# ---------------------------------------------------------------------------
# 4. Assemble dist/ — copy .so files alongside the binary
# ---------------------------------------------------------------------------
echo "Assembling dist/ ..."
cp "$LIB_DIR"/*.so "$DIST_DIR/" 2>/dev/null || true

mkdir -p "$DIST_DIR/models"

# Set RPATH so the binary finds the .so in the same directory
if command -v patchelf >/dev/null 2>&1; then
    patchelf --set-rpath '$ORIGIN' "$DIST_DIR/$BIN_NAME"
    echo "RPATH set to \$ORIGIN via patchelf."
else
    echo "Note: patchelf not found. Set LD_LIBRARY_PATH=\$PWD when running:"
    echo "  LD_LIBRARY_PATH=. ./janus"
fi

echo ""
echo "Build complete:"
ls -lh "$DIST_DIR/"
echo ""
echo "Usage:"
echo "  Copy dist/ anywhere. Place your .gguf model in dist/models/"
echo "  Set JANUS_MODEL_PATH=./models/yourmodel.gguf in .env"
if command -v patchelf >/dev/null 2>&1; then
    echo "  Run: ./dist/janus"
else
    echo "  Run: LD_LIBRARY_PATH=./dist ./dist/janus"
fi
