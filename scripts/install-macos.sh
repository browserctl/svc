#!/bin/bash
set -e

BINARY="/usr/local/bin/browserctl-svc"
PLIST="com.browserctl.svc.plist"
PLIST_PATH="$HOME/Library/LaunchAgents/$PLIST"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "Installing browserctl-svc on macOS..."

# Build
echo "Building..."
cd "$SCRIPT_DIR"
make build

# Install binary
echo "Installing binary to $BINARY..."
sudo install -m 755 bin/browserctl-svc "$BINARY"

# Install launchd service
if command -v launchctl &>/dev/null; then
  echo "Installing launchd service..."
  mkdir -p "$HOME/Library/LaunchAgents"

  cat > "$PLIST_PATH" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.browserctl.svc</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/browserctl-svc</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/tmp/browserctl-svc.log</string>
  <key>StandardErrorPath</key>
  <string>/tmp/browserctl-svc.err</string>
</dict>
</plist>
EOF

  launchctl load "$PLIST_PATH"
  echo "Service installed and loaded. Run 'make start' to start it."
else
  echo "launchctl not found, skipping service installation."
fi

echo "Done."