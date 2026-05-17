#!/bin/bash
set -e

BINARY="/usr/local/bin/browserctl-svc"
SERVICE_FILE="browserctl-svc.service"
SERVICE_PATH="/etc/systemd/system/$SERVICE_FILE"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "Installing browserctl-svc on Linux..."

# Build
echo "Building..."
cd "$SCRIPT_DIR"
make build

# Install binary
echo "Installing binary to $BINARY..."
sudo install -m 755 bin/browserctl-svc "$BINARY"

# Install systemd service
if command -v systemctl &>/dev/null; then
  echo "Installing systemd service..."
  CURRENT_USER=$(whoami)
  sed -e "s/^User=.*/User=$CURRENT_USER/" \
      -e "s/^Group=.*/Group=$CURRENT_USER/" \
      -e "s|/home/dave|/home/$CURRENT_USER|g" \
      "$SCRIPT_DIR/$SERVICE_FILE" | sudo tee "$SERVICE_PATH" > /dev/null
  sudo systemctl daemon-reload
  sudo systemctl enable browserctl-svc
  echo "Service installed and enabled. Run 'make start' to start it."
else
  echo "systemd not found, skipping service installation."
fi

echo "Done."