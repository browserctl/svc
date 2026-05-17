#!/bin/bash
set -e

BINARY="/usr/local/bin/browserctl-svc"
SERVICE_PATH="/etc/systemd/system/browserctl-svc.service"

echo "Uninstalling browserctl-svc on Linux..."

# Stop and disable service
if command -v systemctl &>/dev/null; then
  echo "Stopping and disabling service..."
  sudo systemctl stop browserctl-svc 2>/dev/null || true
  sudo systemctl disable browserctl-svc 2>/dev/null || true
  sudo rm -f "$SERVICE_PATH"
  sudo systemctl daemon-reload
fi

# Remove binary
sudo rm -f "$BINARY"

echo "Done."