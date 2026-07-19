#!/bin/bash
# Mutagen Web Console - Linux Build Script
# Build output: build/
#   mutagen-web-server_linux   - Linux server
#   mutagen-web-agent.exe      - Windows agent
#   web/                       - Frontend static files
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
BUILD="$ROOT/build"

echo "=== Building mutagen-web-server_linux ==="
cd "$ROOT/server"
go build -o "$BUILD/mutagen-web-server_linux" .

echo "=== Building mutagen-web-agent.exe ==="
cd "$ROOT/agent"
GOOS=windows GOARCH=amd64 go build -o "$BUILD/mutagen-web-agent.exe" .

echo "=== Building frontend ==="
cd "$ROOT/web"
npm install && npm run build
cp -r "$ROOT/web/dist/"* "$BUILD/web/"

echo ""
echo "Build complete!"
ls -lh "$BUILD" | grep -v web
