# browserctl/svc

WebSocket service that bridges browserctl CLI to Chrome extension for browser automation.

## Architecture

```
browserctl CLI → browserctl/svc → Chrome extension → Chrome browser
```

The service acts as a transparent CDP proxy:
- Accepts WebSocket connections from CLI clients
- Connects to Chrome extension via WebSocket
- Forwards CDP commands to extension
- Routes tabs and windows intelligently

## Features

- **Transparent CDP Proxy** - Standard Chrome DevTools Protocol compatibility
- **Tab/Window Routing** - Routes commands to correct tab based on domain
- **Multi-window Support** - Manages multiple Chrome windows
- **Extension Bridge** - Connects CLI to Chrome extension
- **HTTP API** - Health check and status endpoints

## Ports

| Port | Protocol | Description |
|------|----------|-------------|
| 9222 | WebSocket | CDP commands from CLI |
| 9223 | HTTP | Health check, status |

## Configuration

Environment variables or config file:

```bash
BROWSERCTL_SECRET=your-secret          # Auth secret
BROWSERCTL_SVC_PORT=9222             # WebSocket port
BROWSERCTL_HTTP_PORT=9223             # HTTP API port
BROWSERCTL_PROFILE_DIR=~/.config/google-chrome  # Chrome profile
```

Or in `.env` / `config.json`:

```
BROWSERCTL_SECRET=your-secret
BROWSERCTL_SVC_PORT=9222
BROWSERCTL_HTTP_PORT=9223
BROWSERCTL_PROFILE_DIR=~/.config/google-chrome
```

## HTTP API

### Health Check

```bash
curl http://localhost:9223/health
```

### Status

```bash
curl http://localhost:9223/status
```

## Installation

```bash
make build
sudo make install
```

## Service Management

**Linux:**
```bash
sudo systemctl start browserctl-svc
sudo systemctl stop browserctl-svc
sudo systemctl restart browserctl-svc
sudo journalctl -u browserctl-svc -f
```

**macOS:**
```bash
launchctl load ~/Library/LaunchAgents/com.browserctl.svc.plist
launchctl unload ~/Library/LaunchAgents/com.browserctl.svc.plist
```

## Development

```bash
make lint    # Run linters
make test    # Run tests
make build   # Build binary
```

## See Also

- [browserctl/cli](https://github.com/browserctl/cli) - CLI documentation
- [browserctl/ext](https://github.com/browserctl/ext) - Chrome extension documentation