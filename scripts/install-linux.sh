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
  CURRENT_HOME=$(eval echo ~$CURRENT_USER)

  sudo tee "$SERVICE_PATH" > /dev/null << EOF
[Unit]
Description=browserctl-svc - Browser Control Service
After=network.target

[Service]
Type=simple
User=$CURRENT_USER
Group=$CURRENT_USER
WorkingDirectory=$SCRIPT_DIR

Environment=BROWSERCTL_SVC_PORT=9222
Environment=BROWSERCTL_HTTP_PORT=9223
Environment=BROWSERCTL_PROFILE_DIR=$CURRENT_HOME/.config/browserctl
Environment=BROWSERCTL_EXT_PATH=$SCRIPT_DIR/../ext/chromium

ExecStart=$BINARY

Restart=on-failure
RestartSec=5

MemoryMax=1G
MemoryHigh=800M

StandardOutput=journal
StandardError=journal
SyslogIdentifier=browserctl-svc

[Install]
WantedBy=multi-user.target
EOF

  sudo systemctl daemon-reload
  sudo systemctl enable browserctl-svc
  echo "Service installed and enabled. Run 'make start' to start it."
else
  echo "systemd not found, skipping service installation."
fi

echo "Done."