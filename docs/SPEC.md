# browserctl/svc — Product Specification

## Overview

**browserctl-svc** is a centralized browser automation platform for AI agents. It connects to a real Chrome browser running on the local machine, exposes a clean HTTP API for browser control, and records intercepted network events to disk for later retrieval.

The platform acts as the "body" for AI agents — providing browser control without the AI needing to know about CDP, WebSocket, or Chrome internals.

---

## Design Principles

1. **HTTP-only client interface** — Client sends HTTP requests, gets HTTP responses. No WebSocket required from client. Client manages its own polling cadence.

2. **State lives on svc** — Session, tabs, intercept rules, and buffered events are all stored server-side. Client is thin and stateless aside from holding a `session_id`.

3. **Network interception is passive** — We observe, never intervene. Requests pass through Chrome normally; we only record what matches the configured patterns.

4. **Per-user data directory** — All data stored under `~/.browserctl/`, never in system-level paths.

5. **Chrome as the user's Chrome** — We connect to the user's already-running Chrome (or launch one using their profile), preserving their signed-in sessions, cookies, and extensions.

---

## Product Vision

An AI agent should be able to sit down at the platform, see what the user's browser shows, click buttons, type text, scroll pages, execute JavaScript, and watch network traffic — without knowing what CDP is or how Chrome works internally.

The client is an AI agent SDK (in any language). The platform is browserctl-svc. The browser is Chrome running on the same machine as svc.

---

## Terminology

| Term | Definition |
|------|------------|
| **Session** | A persistent connection to one Chrome instance. Holds tab list, intercept rules, and event buffer. |
| **Tab** | One open tab within a Chrome session. |
| **Client** | HTTP client — CLI, AI agent SDK, or any HTTP consumer. Stateless except for `session_id`. |
| **Provider** | The browser engine type. Phase 1: Chrome only (CDP). |
| **Intercept** | Passive recording of network requests/responses that match URL patterns. |
| **Event** | A network request+response pair captured by intercept. Written to disk as one JSON object. |

---

## Connection Modes

### Launch Mode (default)

svc launches a new Chrome process using the user's default profile.

```
POST /sessions
{}                              → { id: "s_xxx", status: "active", tabs: [...] }
```

svc finds Chrome from the system PATH or known locations, starts it with `--remote-debugging-port=0` (dynamic port), and connects via the discovered CDP URL.

### Connect Mode

svc connects to an already-running Chrome via its remote debug port.

```
POST /sessions
{ "cdp_url": "http://localhost:9336" }
```

This preserves all of the user's signed-in sessions, cookies, and extensions. This is the primary mode for AI employee use cases.

---

## Session Model

### Session Lifecycle

```
Client                         svc                              Chrome
  │                             │                                 │
  │─── POST /sessions ─────────→ │─── connect ────────────────────→│
  │←─── { id: "s_xxx" } ─────── │                                 │
  │                             │ s_xxx.status = "active"          │
  │                             │ s_xxx.tabs = [t1]               │
  │                             │ s_xxx.intercept_patterns = []    │
  │                             │                                 │
  │─── GET /sessions/s_xxx ────→ │  Reuse — return current state    │
  │←─── { status, tabs } ────── │                                 │
  │                             │                                 │
  │─── POST .../navigate ──────→ │─── CDP ─────────────────────────→│
  │←─── HTTP 200 OK ──────────── │                                 │
  │                             │                                 │
  │─── POST .../intercept ──────→ │  Set patterns; activate monitor │
  │←─── HTTP 200 OK ──────────── │                                 │
  │                             │  Chrome makes matching request   │
  │                             │  svc records request+response     │
  │                             │  to ~/.browserctl/events/s_xxx/  │
  │                             │                                 │
  │─── GET .../requests ───────→ │  Read from disk                 │
  │←─── { requests: [...] } ─── │                                 │
  │                             │                                 │
  │─── DELETE /sessions/s_xxx ──→ │─── CDP close ──────────────────→│
  │←─── HTTP 200 OK ──────────── │ s_xxx.status = "closed"         │
```

### Session State

```go
type Session struct {
    ID         string    // "s_" + uuid
    Provider   string    // "chrome"
    Status     string    // "active" | "closed"
    ChromeURL  string    // ws://... (used for CDP connection)
    CreatedAt  time.Time
    UpdatedAt  time.Time

    Tabs           []TabInfo
    ActiveTabID    string

    // Intercept
    InterceptPatterns []string  // empty = interception disabled
    InterceptDir     string     // ~/.browserctl/events/{id}/intercepted/
}
```

---

## Tab Model

A Tab is a Chrome tab within a session. Each tab has an internal ID (`tab_<n>`), visible URL, and title.

```go
type TabInfo struct {
    ID    string  // internal ID, e.g. "tab_1"
    URL   string  // current URL
    Title string  // page title
}
```

svc maintains the tab list by listening to CDP `Target.targetCreated` and `Target.targetDestroyed` events on the browser's CDP WebSocket. The active tab is the one most recently targeted by client operations.

---

## Page Actions (Synchronous)

All page actions are synchronous HTTP requests:

1. Client sends HTTP request with action parameters
2. svc translates to CDP command, sends to Chrome
3. svc waits for CDP response or timeout
4. svc returns HTTP 200 with result or HTTP 4xx with error

No WebSocket from client. No streaming. No callbacks.

| Action | Semantics |
|--------|-----------|
| `navigate` | CDP `Page.navigate`. Optionally wait for `domcontentloaded`/`load`/`networkidle`. |
| `hover` | CDP `Runtime.evaluate` → `document.querySelector(selector).dispatchEvent(new MouseEvent('mouseenter'))`. |
| `click` | CDP `Runtime.evaluate` → `document.querySelector(selector).click()`. Waits for element to be actionable. |
| `type` | CDP `Runtime.evaluate` → focus + `insertText`. |
| `scroll` | CDP `Input.synthesizeScrollGesture` or `Runtime.evaluate` → `scrollBy(x, y)`. |
| `evaluate` | CDP `Runtime.evaluate`. Returns serialized JSON result. |
| `waitForSelector` | CDP `Runtime.waitForFunction` or `Page.waitForSelector`. Waits for element state. |

All actions return HTTP 200 on success. Errors are returned as JSON:

```json
{ "error": "click failed", "reason": "element .login not found after 10s" }
```

---

## Network Interception

### Semantics

Interception is **passive observation**. We never modify, block, or abort requests. Chrome handles them normally; we just record.

When `POST /sessions/:id/intercept` is called with patterns, svc activates Chrome's `Fetch.enable` CDP domain. Each time a request matches a pattern, svc buffers the request headers. When the response finishes, svc writes the merged request+response as one JSON object to the event file.

### Pattern Matching

Patterns are glob-style strings matched against the request URL:

```
*doubleclick*
*google-analytics*
*facebook.net/tr*
```

Uses `filepath.Match` semantics (`*` matches anything).

### Event Format

One JSON object per line, written after response is received:

```json
{
  "id":         "req_001",
  "tab_id":     "tab_1",
  "time":       "2026-05-28T10:00:01Z",
  "request": {
    "url":     "https://www.google-analytics.com/collect?v=1&tid=UA-...",
    "method":  "GET",
    "headers": {
      "User-Agent": "Mozilla/5.0...",
      "Referer": "https://example.com/"
    }
  },
  "response": {
    "status":        200,
    "status_text":   "OK",
    "headers": {
      "Content-Type": "image/gif",
      "Cache-Control": "no-cache"
    },
    "body_base64":  "R0lGODlhAQABAIAAAAAAAP..."
  }
}
```

- `id` is a locally-generated incrementing counter (`req_001`, `req_002`, ...), unique within the session.
- `time` is when the request was initiated.
- `body_base64` is the full response body, base64-encoded.
- Lines are written with `os.O_APPEND` — crash-safe, no locking needed for writes from a single goroutine.

---

## Error Handling

| Scenario | svc behavior |
|----------|--------------|
| Chrome connection lost | Session status → `"disconnected"`. Operations return HTTP 503. |
| CDP command timeout | HTTP 504 with `"timeout"` error. |
| Element not found | HTTP 400 with `"element not found"` error. |
| Invalid session ID | HTTP 404. |
| Session already closed | HTTP 410. |
| Intercept pattern invalid | HTTP 400 with validation error. |

---

## Security

- `BROWSERCTL_SECRET` env var enables HTTP Bearer auth on all endpoints.
- Chrome runs as the same user as svc — no privilege escalation.
- Data directory is under `~/.browserctl/` — user-owned, not system-wide.
- Intercept event files are append-only — no overwriting of historical data.

---

## Configuration

```bash
BROWSERCTL_SVC_PORT=9222            # HTTP API port (default: 9222)
BROWSERCTL_DATA_DIR=~/.browserctl   # data directory (default: ~/.browserctl)
BROWSERCTL_SECRET=                  # auth secret (optional)
```

Also supported via `.env` or `config.json` in the working directory.

---

## Architecture

```
svc/
├── cmd/svc/main.go              # entry point, flag parsing, server bootstrap
└── internal/
    ├── chrome/
    │   ├── launcher.go         # Chrome process launcher (launch mode)
    │   └── bridge.go           # Chrome CDP connection helpers
    ├── http/
    │   └── server.go           # HTTP router, middleware, handlers
    └── proxy/
        ├── backend.go          # BackendProvider interface
        ├── direct_cdp_backend.go  # Phase 1: DirectCDPBackend implementation
        ├── extension_backend.go   # Phase 4: ExtensionBackend
        ├── router.go           # Tab routing
        └── types.go            # Session, Tab, InterceptedRequest types
```

### BackendProvider interface

```go
type BackendProvider interface {
    // Connect to browser
    Connect(ctx context.Context, cdpUrl string) error
    Close(ctx context.Context) error

    // Session lifecycle
    NewSession(ctx context.Context) (*Session, error)
    GetSession(ctx context.Context, id string) (*Session, error)
    CloseSession(ctx context.Context, id string) error

    // Tab operations
    ListTabs(ctx context.Context, sessionId string) ([]Tab, error)
    NewTab(ctx context.Context, sessionId, url string) (string, error) // returns tabId

    // Page actions
    Navigate(ctx context.Context, tabId, url string, opts *NavigateOptions) error
    Click(ctx context.Context, tabId, selector string, timeoutms int) error
    Type(ctx context.Context, tabId, selector, text string) error
    Scroll(ctx context.Context, tabId string, x, y int) error
    Evaluate(ctx context.Context, tabId, script string) (interface{}, error)
    WaitForSelector(ctx context.Context, tabId, selector string, state string, timeoutms int) error
    Hover(ctx context.Context, tabId, selector string) error

    // Page state
    Screenshot(ctx context.Context, tabId string) ([]byte, error)
    GetDOM(ctx context.Context, tabId string) (string, error)

    // Network interception
    SetIntercept(ctx context.Context, sessionId string, patterns []string) error
    GetRequests(ctx context.Context, tabId string) ([]InterceptedRequest, error)
}
```

---

## Implementation Phases

| Phase | Scope | Status |
|-------|-------|--------|
| **Phase 1** | Session lifecycle, navigate, click, type, scroll, evaluate, wait, screenshot, dom | Current |
| **Phase 2** | Network interception (intercept + pull merged requests) | Planned |
| **Phase 3** | Session metadata persistence across svc restarts | Planned |
| **Phase 4** | ExtensionBackend (production Chrome connection via browserctl extension) | Planned |

---

## Related Documents

- [HTTP API Specification](api.md) — Full endpoint reference with request/response shapes
- [Data Storage Design](data-storage.md) — File layout, rotation, and cleanup
