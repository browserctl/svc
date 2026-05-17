#!/bin/bash
set -e

BINARY="/usr/local/bin/browserctl-svc"
PLIST="com.browserctl.svc.plist"
PLIST_PATH="$HOME/Library/LaunchAgents/$PLIST"

echo "Uninstalling browserctl-svc on macOS..."

# Unload launchd service
if command -v launchctl &>/dev/null; then
  echo "Unloading launchd service..."
  launchctl unload "$PLIST_PATH" 2>/dev/null || true
  rm -f "$PLIST_PATH"
fi

# Remove binary
sudo rm -f "$BINARY"

echo "Done."