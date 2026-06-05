# browserctl-svc

**Browser automation platform for AI agents.**

browserctl-svc connects to a Chrome browser on the local machine, exposes a clean HTTP API for browser control, and records intercepted network events to disk. The client is an AI agent — it talks HTTP, it gets HTTP back, it never touches CDP or WebSocket.

---

## What it is

AI agents need to control a browser — click buttons, type text, scroll pages, execute JavaScript, watch network traffic. Most browser automation tools are built for human use or web scraping. browserctl-svc is built for AI: stateless HTTP client interface, session state on the server, and passive network interception.

**Primary use case:** An AI agent sits down at the employee's Chrome (with their cookies, extensions, and logged-in sessions intact), controls it via HTTP, and observes network traffic without interrupting it.

---

## Quick start

```bash
# Start svc
./browserctl-svc

# Create a session (connect to running Chrome)
curl -X POST http://localhost:9222/sessions \
  -d '{"cdp_url": "http://localhost:9336"}'

# Navigate
curl -X POST http://localhost:9222/sessions/s_abc123/tabs/tab_1/navigate \
  -d '{"url": "https://example.com"}'

# Click
curl -X POST http://localhost:9222/sessions/s_abc123/tabs/tab_1/click \
  -d '{"selector": "button.submit"}'

# Intercept network traffic
curl -X POST http://localhost:9222/sessions/s_abc123/intercept \
  -d '{"patterns": ["*google-analytics*"]}'

# Read one event
curl http://localhost:9222/sessions/s_abc123/tabs/tab_1/intercepted

# Close
curl -X DELETE http://localhost:9222/sessions/s_abc123
```

---

## Core concepts

### Session

A Session is a persistent connection to one Chrome instance. It holds the tab list, intercept rules, and event buffer. Sessions live on svc until explicitly closed.

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/sessions` | Create session (launch or connect Chrome) |
| `GET` | `/sessions/:id` | Get current state |
| `DELETE` | `/sessions/:id` | Close session |

### Tab

A Tab is a Chrome tab within a session. Each tab has an internal ID (`tab_1`, `tab_2`...), a visible URL, and a title. svc tracks tabs via CDP `Target.targetCreated` / `Target.targetDestroyed` events.

### Intercept

Passive network monitoring. Requests matching URL patterns are recorded to disk — request and response merged into one JSON object. Chrome handles requests normally; svc only observes. **No modification, no blocking.**

---

## Configuration

```bash
BROWSERCTL_SVC_PORT=9222            # HTTP API port (default: 9222)
BROWSERCTL_DATA_DIR=~/.browserctl   # data directory
BROWSERCTL_SECRET=                  # auth secret (optional)
```

Or in `.env` / `config.json` in the working directory.

---

## Project structure

```
svc/
├── cmd/svc/main.go              # entry point, flag parsing, server bootstrap
└── internal/
    ├── chrome/                 # Chrome launcher + CDP connection helpers
    ├── http/                    # HTTP router, middleware, handlers
    └── connector/               # Connector interface + implementations
```

---

## See also

- [API Reference](docs/API.md) — Full HTTP endpoint reference
- [Design](docs/DESIGN.md) — Architecture, data models, semantics
- [Storage](docs/STORAGE.md) — Directory layout, file formats, rotation
- [browserctl/cli](https://github.com/browserctl/cli) — CLI client
- [sharingan](../sharingan) — Novel scraper provider built on browserctl-svc