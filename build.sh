#!/bin/bash
# Mutagen Web Console - Linux Build Script
# Build output: build/
#   mutagen-web-server_linux   - Linux server
#   mutagen-web-agent.exe      - Windows agent
#   web/                       - Frontend static files
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
BUILD="$ROOT/build"
mkdir $BUILD
echo "=== Building Mutagen (agents + Linux CLI) ==="
cp "$ROOT/scripts/Dockerfile" $BUILD
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
docker run --rm -v $(pwd):/app -w /app swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/library/node:20 bash -c "npm config set registry https://registry.npmmirror.com && npm install && npm run build"
mkdir -p "$BUILD/web"
cp -r "$ROOT/web/dist/"* "$BUILD/web/"

echo ""
echo "Build complete!"
ls -lh "$BUILD"