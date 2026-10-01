#!/bin/bash
# Navigate to project root
cd "$(dirname "$0")" || exit

# Load environment variables
if [ -f .env ]; then
    set -a
    source .env
    set +a
fi

# Build Go project
echo "Building janus..."
go build -o janus ./cmd/janus

# Start janus server
echo "Starting janus..."
./janus