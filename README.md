# browserctl/svc

**Browser automation platform for AI agents.**

Connects to a real Chrome browser running on the local machine via Chrome DevTools Protocol, exposes a clean HTTP API for browser control, and records intercepted network events to disk for later retrieval.

---

## What it does

### Request handling path

All HTTP requests are handled by svc and dispatched through BackendProvider to the browser. The client never touches Chrome directly.

```
svc 进程
┌─────────────────────────────────────────────────────────────┐
│  HTTP Server                                                 │
│  ─────────────────────────────────────────────────────────  │
│  POST /sessions         →  SessionHandler.Create            │
│  GET  /sessions/:id     →  SessionHandler.Get              │
│  DELETE /sessions/:id   →  SessionHandler.Delete            │
│  GET  /sessions/:id/tabs →  TabHandler.List                 │
│  POST /sessions/:id/tabs →  TabHandler.Create               │
│  POST /sessions/:id/tabs/:tabId/navigate →  PageHandler     │
│  POST /sessions/:id/tabs/:tabId/hover    →  PageHandler     │
│  POST /sessions/:id/tabs/:tabId/click    →  PageHandler     │
│  POST /sessions/:id/tabs/:tabId/type     →  PageHandler     │
│  POST /sessions/:id/tabs/:tabId/scroll   →  PageHandler     │
│  POST /sessions/:id/tabs/:tabId/evaluate →  PageHandler     │
│  POST .../waitForSelector                →  PageHandler     │
│  GET  /sessions/:id/tabs/:tabId/screenshot → PageHandler   │
│  GET  /sessions/:id/tabs/:tabId/dom       → PageHandler   │
│  POST /sessions/:id/intercept  →  InterceptHandler          │
│  GET  /sessions/:id/tabs/:tabId/requests → InterceptHandler│
└───────────────────────────┬─────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│  BackendProvider (interface)                                 │
│  ─────────────────────────────────────────────────────────  │
│  Connect / Close / NewSession / CloseSession / ListTabs     │
│  Navigate / Hover / Click / Type / Scroll / Evaluate        │
│  WaitForSelector / Screenshot / GetDOM                      │
│  SetIntercept / GetRequests                                 │
└───────────────────────────┬─────────────────────────────────┘
                            │ implements
            ┌───────────────┴───────────────┐
            ▼                             ▼
┌───────────────────────┐     ┌───────────────────────┐
│  DirectCDPBackend     │     │  ExtensionBackend     │
│  Phase 1             │     │  Phase 4 (future)    │
│  HTTP /json → tab WS │     │                       │
│  CDP commands → WS   │     │                       │
└───────────┬──────────┘     └───────────────────────┘
            │ CDP WebSocket
            ▼
      ┌──────────┐
      │  Chrome  │
      └──────────┘
```

### Event listener path (async)

A separate goroutine in DirectCDPBackend listens for CDP events on each tab WebSocket. Matching events are written to disk. This is entirely independent of the request path.

```
┌─────────────────────────────────────────────────────────────┐
│  DirectCDPBackend.ReadLoop (goroutine, per tab)           │
│  ───────────────────────────────────────────────────────── │
│  tab WS ◄─────────────────────────────────────────────── │
│    │                                                       │
│    │ on Fetch.requestPaused                                │
│    │ on Network.requestWillBeSent                        │
│    │ on Network.responseReceived                          │
│    │ on Network.loadingFinished                           │
│    │ on Fetch.authRequired                                │
│    ▼                                                       │
│  PatternMatcher (matches against session.interceptPatterns)│
│    │                                                       │
│    │ matched                                              │
│    ▼                                                       │
│  EventWriter.append(line + "\n")                          │
│    ~/.browserctl/events/{session_id}/intercepted/         │
│                0000000000.jsonl                            │
└─────────────────────────────────────────────────────────────┘
```

**Client** talks to svc over plain HTTP — no WebSocket required. Client pulls intercepted events at its own pace.

---

## Key concepts

### Session

A Session is a connection to one Chrome instance. It holds the current tab list, intercept rules, and buffered events. Sessions persist on svc until explicitly closed.

```
POST   /sessions              → create new session (launch or connect Chrome)
GET    /sessions/:id          → reuse session (get current state)
DELETE /sessions/:id           → close session
```

### Connection modes

**Launch mode** (no params): svc starts a new Chrome process using the user's default profile.

```bash
POST /sessions
# {}
```

**Connect mode** (with `cdp_url`): svc connects to an already-running Chrome via its remote debug port.

```bash
POST /sessions
{ "cdp_url": "http://localhost:9336" }
```

This preserves the user's signed-in session, cookies, and extensions — no headless mode required.

### Network Interception (passive monitor)

When intercept patterns are set, matching network requests and their responses are recorded to disk. **No intervention**: requests pass through Chrome normally, svc only observes.

```
POST   /sessions/:id/intercept   → set URL patterns to monitor
GET    /sessions/:id/tabs/:tabId/requests   → pull intercepted requests
```

### Page Actions (synchronous)

All page operations (`navigate`, `click`, `type`, `scroll`, `evaluate`, `wait`) are synchronous — client sends a command, svc executes it in Chrome, returns the result. No WebSocket required from the client.

### Page State (independent primitives)

`screenshot` and `dom` are separate endpoints — client requests only what it needs.

---

## HTTP API

### Session

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/sessions` | Create session (launch or connect Chrome) |
| `GET` | `/sessions/:id` | Reuse session, get current state |
| `DELETE` | `/sessions/:id` | Close session |

**POST /sessions** — launch mode (no params):
```json
{}
```

**POST /sessions** — connect mode:
```json
{ "cdp_url": "http://localhost:9336" }
```

Response:
```json
{
  "id": "s_abc123",
  "provider": "chrome",
  "status": "active",
  "tabs": [
    { "id": "tab_1", "url": "https://example.com", "title": "Example" }
  ],
  "intercept_patterns": []
}
```

**GET /sessions/:id** response:
```json
{
  "id": "s_abc123",
  "provider": "chrome",
  "status": "active",
  "tabs": [
    { "id": "tab_1", "url": "https://example.com", "title": "Example" }
  ],
  "intercept_patterns": []
}
```

---

### Tab Operations

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/sessions/:id/tabs` | List all tabs |
| `POST` | `/sessions/:id/tabs` | Open new tab |

---

### Page Actions (synchronous)

|| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/sessions/:id/tabs/:tabId/navigate` | Navigate to URL |
| `POST` | `/sessions/:id/tabs/:tabId/hover` | Hover over element |
| `POST` | `/sessions/:id/tabs/:tabId/click` | Click element |
| `POST` | `/sessions/:id/tabs/:tabId/type` | Type text |
| `POST` | `/sessions/:id/tabs/:tabId/scroll` | Scroll page |
| `POST` | `/sessions/:id/tabs/:tabId/evaluate` | Execute JavaScript |
| `POST` | `/sessions/:id/tabs/:tabId/waitForSelector` | Wait for element |

**navigate** — `POST /sessions/:id/tabs/:tabId/navigate`
```json
{ "url": "https://example.com", "wait_until": "networkidle" }
```

**click** — `POST /sessions/:id/tabs/:tabId/click`
```json
{ "selector": "button.submit", "timeout": 10000 }
```

**type** — `POST /sessions/:id/tabs/:tabId/type`
```json
{ "selector": "input[name=email]", "value": "hello@example.com" }
```

**hover** — `POST /sessions/:id/tabs/:tabId/hover`
```json
{ "selector": "a.dropdown-toggle" }
```
Triggers `mouseenter` / `mouseover` events. Useful for revealing dropdowns, tooltips, or lazy-loaded content.

**scroll** — `POST /sessions/:id/tabs/:tabId/scroll`
```json
{ "y": 500, "x": 0 }
```
Scrolls the page by the given pixel offset. Use positive `y` to scroll down, negative to scroll up.

**evaluate** — `POST /sessions/:id/tabs/:tabId/evaluate`
```json
{ "script": "() => document.title" }
```
response: `{ "result": "Page Title" }`

**waitForSelector** — `POST /sessions/:id/tabs/:tabId/waitForSelector`
```json
{ "selector": ".chapter-detail-article p", "state": "visible", "timeout": 10000 }
```
Wait for a CSS selector to reach a desired state (`visible`, `hidden`, `attached`, `detached`). Returns `{ "found": true }` or HTTP 504 on timeout.

---

### Page State

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/sessions/:id/tabs/:tabId/screenshot` | Screenshot PNG |
| `GET` | `/sessions/:id/tabs/:tabId/dom` | Page HTML |

---

### Network Interception

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/sessions/:id/intercept` | Set URL monitoring patterns |
| `GET` | `/sessions/:id/tabs/:tabId/requests` | Pull intercepted requests |

**intercept** — `POST /sessions/:id/intercept`
```json
{ "patterns": ["*doubleclick*", "*google-analytics*", "*facebook.net*"] }
```

**GET /requests** response — request and response merged into one entry:
```json
{
  "requests": [
    {
      "id": "req_001",
      "tab_id": "tab_1",
      "time": "2026-05-28T10:00:01Z",
      "request": {
        "url": "https://www.google-analytics.com/collect?v=1&...",
        "method": "GET",
        "headers": { "User-Agent": "..." }
      },
      "response": {
        "status": 200,
        "headers": { "Content-Type": "image/gif" },
        "body_base64": "R0lGODlhAQABAIAAAAAAAP..."
      }
    }
  ]
}
```

Each line in the event log file is one JSON object containing both request and response, written after the response is received.

---

## Data Storage

```
~/.browserctl/
├── sessions/
│   └── {session_id}/
│       └── meta.json          ← session metadata (JSON)
└── events/
    └── {session_id}/
        └── intercepted/
            └── 0000000000.jsonl   ← intercepted requests (JSON Lines)
```

- Event files are append-only JSON Lines.
- Files rotate every 100,000 entries.
- No automatic cleanup — managed externally.

---

## Configuration

Environment variables:

```bash
BROWSERCTL_SVC_PORT=9222                # HTTP API port
BROWSERCTL_DATA_DIR=~/.browserctl       # data directory (default: ~/.browserctl)
BROWSERCTL_SECRET=                      # auth secret (optional)
```

Or in `.env` / `config.json` in the working directory.

---

## Architecture

```
svc/
├── cmd/svc/main.go                # entry point, flag parsing, server bootstrap
└── internal/
    ├── chrome/
    │   ├── launcher.go           # Chrome process launcher (launch mode)
    │   └── bridge.go             # Chrome CDP connection helpers
    ├── http/
    │   └── server.go             # HTTP server, routing, middleware
    └── connector/
        ├── connector.go          # Connector interface
        ├── chrome_connector.go    # ChromeConnector (Phase 1)
        ├── extension_connector.go # ExtensionConnector (Phase 4)
        ├── types.go               # Session, Tab, InterceptedRequest
        └── router.go              # Tab routing
```

### Connector interface

```go
type Connector interface {
    // Lifecycle
    Connect(ctx context.Context, cdpUrl string) error
    Close(ctx context.Context) error

    // Session lifecycle
    NewSession(ctx context.Context) (*Session, error)
    GetSession(ctx context.Context, id string) (*Session, error)
    CloseSession(ctx context.Context, id string) error

    // Tab operations
    ListTabs(ctx context.Context, sessionId string) ([]Tab, error)
    NewTab(ctx context.Context, sessionId, url string) (string, error)

    // Page actions
    Navigate(ctx context.Context, tabId, url string, opts *NavigateOptions) error
    Hover(ctx context.Context, tabId, selector string) error
    Click(ctx context.Context, tabId, selector string) error
    Type(ctx context.Context, tabId, selector, text string) error
    Scroll(ctx context.Context, tabId string, x, y int) error
    Evaluate(ctx context.Context, tabId, script string) (interface{}, error)
    WaitForSelector(ctx context.Context, tabId, selector string, state string, timeoutms int) error

    // Page state
    Screenshot(ctx context.Context, tabId string) ([]byte, error)
    GetDOM(ctx context.Context, tabId string) (string, error)

    // Network interception
    SetIntercept(ctx context.Context, sessionId string, patterns []string) error
    GetRequests(ctx context.Context, sessionId, tabId string) ([]InterceptedRequest, error)
}
```

---

## Implementation Phases

|| Phase | Scope |
|-------|-------|
| **Phase 1** | Session lifecycle, navigate, hover, click, type, scroll, evaluate, waitForSelector, screenshot, dom |
| **Phase 2** | Network interception (intercept, pull merged requests) |
| **Phase 3** | Session persistence across svc restarts |
| **Phase 4** | ExtensionBackend (production Chrome connection via browserctl extension) |

Phase 1 is the current target.

---

## Development

```bash
make lint    # Run linters
make test    # Run tests
make build   # Build binary
```

---

## See Also

- [browserctl/cli](https://github.com/browserctl/cli) — CLI client
- [sharingan](../sharingan) — Novel scraper provider built on top of browserctl-svc
