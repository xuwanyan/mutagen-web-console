#!/bin/bash
# ============================================
# Mutagen Web Server - Linux Deployment Script
# ============================================
# Usage: bash deploy-linux.sh <server_ip> [port]
# Example: bash deploy-linux.sh 192.168.238.23 18080
# ============================================
set -euo pipefail

SERVER_IP="${1:-0.0.0.0}"
PORT="${2:-18080}"
PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUILD_DIR="$PROJECT_ROOT/build"
DEPLOY_DIR="/opt/mutagen-web"

echo "============================================"
echo " Mutagen Web Server - Linux Deployment"
echo "============================================"
echo "Server IP: $SERVER_IP"
echo "Port:      $PORT"
echo ""

# Step 1: Cross-compile from Windows (or build directly on Linux)
echo "[1/4] Building server for Linux..."
cd "$PROJECT_ROOT/server"
go build -o "$BUILD_DIR/server_linux" .
echo "  -> server_linux built"

# Step 2: Prepare frontend
echo "[2/4] Building frontend..."
cd "$PROJECT_ROOT/web"
npm run build
echo "  -> frontend built"

# Step 3: Create deployment directory
echo "[3/4] Creating deployment directory..."
sudo mkdir -p "$DEPLOY_DIR/web"
sudo cp "$BUILD_DIR/server_linux" "$DEPLOY_DIR/server"
sudo cp -r "$BUILD_DIR/web/"* "$DEPLOY_DIR/web/"
echo "  -> files copied to $DEPLOY_DIR"

# Step 4: Install systemd service
echo "[4/4] Installing systemd service..."
sudo tee /etc/systemd/system/mutagen-web.service > /dev/null <<EOF
[Unit]
Description=Mutagen Web Server - Remote file sync management
After=network.target

[Service]
Type=simple
ExecStart=$DEPLOY_DIR/server -addr $SERVER_IP:$PORT
WorkingDirectory=$DEPLOY_DIR
Restart=always
RestartSec=5
User=root

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable mutagen-web
sudo systemctl restart mutagen-web
echo "  -> service installed and started"

echo ""
echo "============================================"
echo " Deployment complete!"
echo ""
echo " Access: http://$SERVER_IP:$PORT"
echo ""
echo " Manage:"
echo "   sudo systemctl status mutagen-web"
echo "   sudo systemctl restart mutagen-web"
echo "   sudo journalctl -u mutagen-web -f"
echo ""
echo " Database: $DEPLOY_DIR/data.json"
echo "============================================"