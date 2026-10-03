#!/usr/bin/env bash
# build.sh — Janus Linux / WSL2 build script
# Downloads pre-built llama.cpp Vulkan .so files from GitHub Releases,
# then builds the janus binary.
#
# Usage:
#   ./build.sh                      # use the pinned llama.cpp release (see below)
#   ./build.sh --version b5400      # use another llama.cpp release tag
#   ./build.sh --skip-download      # use .so files already in lib/linux/
#
# The struct layouts in internal/bridge/llama_dl.go match llama.cpp b11146
# (llama.h of that tag); other tags may need those buffers adjusted.
# Requires Go >= 1.25 (purego v0.11); with GOTOOLCHAIN=auto an older go
# command downloads the right toolchain by itself.
#
# WSL2 note: Vulkan is bridged from Windows via the GPU driver.
#   NVIDIA RTX on WSL2 — Vulkan works out of the box with recent drivers.
#   Run 'vulkaninfo' inside WSL2 to confirm your GPU is visible.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_DIR="$ROOT/lib/linux"
DIST_DIR="$ROOT/dist"
BIN_NAME="janus"
LLAMA_VERSION="b11146"
SKIP_DOWNLOAD=false

# ---------------------------------------------------------------------------
# Parse arguments
# ---------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
    case "$1" in
        --version)
            [[ $# -ge 2 && -n "$2" ]] || { echo "--version requires a llama.cpp tag (e.g. b11146)"; exit 1; }
            LLAMA_VERSION="$2"; shift 2 ;;
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
# 1. llama.cpp release version (pinned; override with --version)
# ---------------------------------------------------------------------------
cd "$ROOT"
if [ "$SKIP_DOWNLOAD" = false ]; then
    echo "Using llama.cpp $LLAMA_VERSION"
fi

# ---------------------------------------------------------------------------
# 2. Download and extract Vulkan .so files
# ---------------------------------------------------------------------------
if [ "$SKIP_DOWNLOAD" = false ]; then
    TGZ_NAME="llama-${LLAMA_VERSION}-bin-ubuntu-vulkan-x64.tar.gz"
    TGZ_URL="https://github.com/ggml-org/llama.cpp/releases/download/${LLAMA_VERSION}/${TGZ_NAME}"

    WORK_DIR="$(mktemp -d)"
    trap 'rm -rf "$WORK_DIR"' EXIT

    echo "Downloading ${TGZ_NAME}..."
    curl -fL --progress-bar -o "$WORK_DIR/$TGZ_NAME" "$TGZ_URL"
    tar -xzf "$WORK_DIR/$TGZ_NAME" -C "$WORK_DIR"

    # Replace any previous libs. Keep symlinks (libggml.so.0 -> ...): libllama.so
    # is linked against the versioned sonames.
    mapfile -t NEW_LIBS < <(find "$WORK_DIR" \( -name "libllama.so*" -o -name "libggml*.so*" \))
    if [ "${#NEW_LIBS[@]}" -eq 0 ]; then
        echo "ERROR: no libllama/libggml .so files found in ${TGZ_NAME}."
        exit 1
    fi
    mkdir -p "$LIB_DIR"
    rm -f "$LIB_DIR"/lib*.so*
    cp -P "${NEW_LIBS[@]}" "$LIB_DIR/"

    echo "Shared libraries extracted to $LIB_DIR:"
    ls -lh "$LIB_DIR"/*.so 2>/dev/null || echo "  (none found — check archive contents)"
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
mkdir -p "$DIST_DIR"

echo "Building janus..."
go build -ldflags "-s -w" -o "$DIST_DIR/$BIN_NAME" ./cmd/janus

# ---------------------------------------------------------------------------
# 4. Assemble dist/ — copy .so files alongside the binary
# ---------------------------------------------------------------------------
echo "Assembling dist/ ..."
cp -P "$LIB_DIR"/lib*.so* "$DIST_DIR/" 2>/dev/null || true

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
