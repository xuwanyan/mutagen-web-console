#!/bin/bash
# Mutagen Web Console - Linux Build Script
# Build output: build/
#   mutagen-web-server_linux     - Linux server
#   mutagen-web-agent.exe        - Windows agent
#   mutagen.exe                  - Mutagen CLI (Windows)
#   mutagen-agents.tar.gz        - Mutagen agent bundle
#   web/                         - Frontend static files
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
BUILD="$ROOT/build"
mkdir -p "$BUILD"

echo "=== Building Mutagen (agents + Linux CLI) ==="
cd "$ROOT/mutagen"
go run scripts/build.go
cp build/mutagen-agents.tar.gz "$BUILD/"

echo "=== Building Mutagen CLI (Windows) ==="
cd "$ROOT/mutagen/cmd/mutagen"
GOOS=windows GOARCH=amd64 go build -o "$BUILD/mutagen.exe" .

echo "=== Building mutagen-web-server_linux ==="
cd "$ROOT/server"
go build -o "$BUILD/mutagen-web-server_linux" .

echo "=== Building mutagen-web-agent.exe ==="
cd "$ROOT/agent"
GOOS=windows GOARCH=amd64 go build -o "$BUILD/mutagen-web-agent.exe" .

echo "=== Building frontend ==="
cd "$ROOT/web"
npm install && npm run build
mkdir -p "$BUILD/web"
cp -r "$ROOT/web/dist/"* "$BUILD/web/"

echo ""
echo "Build complete!"
ls -lh "$BUILD" | grep -v web