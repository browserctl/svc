# Design

---

## Design Principles

1. **HTTP-only client interface** — Client sends HTTP requests, gets HTTP responses. No WebSocket required from client. Client manages its own polling cadence.

2. **State lives on svc** — Session, tabs, intercept rules, and buffered events are all stored server-side. Client is thin and stateless aside from holding a `session_id`.

3. **Network interception is passive** — We observe, never intervene. Requests pass through Chrome normally; we only record what matches the configured patterns.

4. **Per-user data directory** — All data stored under `~/.browserctl/`, never in system-level paths.

5. **Chrome as the user's Chrome** — We connect to the user's already-running Chrome (or launch one using their profile), preserving their signed-in sessions, cookies, and extensions.

---

## Session Model

### Lifecycle

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
  │─── GET .../intercepted ──────→ │  Read one from disk             │
  │←─── { request: {...} } ────── │  advance read position         │
  │                             │                                 │
  │─── DELETE /sessions/s_xxx ──→ │─── disconnect CDP ────────────→│
  │←─── HTTP 200 OK ──────────── │ s_xxx.status = "closed"         │
```

### Connection Modes

**Launch mode** (default): svc launches a new Chrome process using the user's default profile.

**Connect mode** (with `cdp_url`): svc connects to an already-running Chrome via its remote debug port. Preserves the user's signed-in sessions, cookies, and extensions.

### Session State

```go
type Session struct {
    ID         string    // "s_" + uuid
    Provider   string    // "chrome"
    Status     string    // "active" | "closed" | "disconnected"
    ChromeURL  string    // ws://... (CDP connection URL)

    Tabs        []TabInfo
    ActiveTabID string

    InterceptPatterns []string  // empty = interception disabled
}
```

`s_created_at` and `updated_at` are stored in meta.json but not exposed in the HTTP API response.

---

## Tab Model

A Tab is a Chrome tab within a session. Each tab has an internal ID (`tab_1`, `tab_2`...), a visible URL, and a title.

```go
type TabInfo struct {
    ID    string  // internal ID: "tab_1", "tab_2", ...
    URL   string  // current URL
    Title string  // page title
}
```

svc maintains the tab list by listening to CDP `Target.targetCreated` and `Target.targetDestroyed` events. The active tab is the one most recently targeted by client operations.

---

## Page Actions

All page actions are synchronous: client sends HTTP, svc executes the CDP command, waits for result, returns HTTP.

| Action | Semantics |
|--------|-----------|
| `navigate` | CDP `Page.navigate`. Optionally wait for `domcontentloaded`/`load`/`networkidle`. |
| `hover` | `Runtime.evaluate` → `document.querySelector(selector).dispatchEvent(new MouseEvent('mouseenter'))`. |
| `click` | `Runtime.evaluate` → `document.querySelector(selector).click()`. Waits for element to be actionable. |
| `type` | `Runtime.evaluate` → focus + `insertText`. Clears existing content first. |
| `scroll` | `Runtime.evaluate` → `scrollBy(x, y)`. Positive `y` scrolls down. |
| `evaluate` | `Runtime.evaluate`. Returns serialized JSON result. |
| `waitForSelector` | `Runtime.waitForFunction` / `Page.waitForSelector`. Waits for element state. |

All actions return HTTP 200 on success. Errors are JSON:

```json
{ "error": "click failed", "reason": "element .login not found after 10s" }
```

---

## Network Interception

**Semantics:** Passive observation. Requests pass through Chrome normally; svc only records those matching the configured patterns. **No modification, no blocking, no continuation.**

### Pattern Matching

Patterns are glob-style strings matched against the request URL via `filepath.Match`:

```
*doubleclick*
*google-analytics*
*facebook.net/tr*
```

### Event Format

One JSON object per line, written after the response is received:

```json
{
  "id":         "req_001",
  "tab_id":     "tab_1",
  "time":       "2026-05-28T10:00:01Z",
  "request": {
    "url":     "https://www.google-analytics.com/collect?v=1&tid=UA-...",
    "method":  "GET",
    "headers": { "User-Agent": "Mozilla/5.0...", "Referer": "https://example.com/" }
  },
  "response": {
    "status":      200,
    "status_text": "OK",
    "headers":     { "Content-Type": "image/gif", "Cache-Control": "no-cache" },
    "body_base64": "R0lGODlhAQABAIAAAAAAAP..."
  }
}
```

- `id` is a locally-generated counter (`req_001`, `req_002`, ...), unique per session.
- `time` is when the request was initiated (RFC 3339).
- `body_base64` is the full response body, base64-encoded (may be empty).
- Lines are written with `os.O_APPEND` — crash-safe.

### Reading

`GET /sessions/:id/tabs/:tabId/intercepted` reads **one** event from disk, advances the read position by one, and persists the new position. Returns `{ "request": null }` when the queue is empty.

Read position is tracked per tab in `meta.json` under `intercept_read_seq`. Each successful read increments it atomically (write-then-rename). Concurrent reads on the same tab are serialized via `sync.RWMutex`.

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

## Connector Interface

The `Connector` interface abstracts all browser connection logic. svc does not know or care which implementation is used.

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
    GetRequests(ctx context.Context, sessionId, tabId string) (*InterceptedRequest, error)
}
```

Implementations:

| Implementation | Phase | Description |
|----------------|-------|-------------|
| `ChromeConnector` | Phase 1 | Direct CDP WebSocket to Chrome |
| `ExtensionConnector` | Phase 4 | Via browserctl Chrome extension |

---

## Architecture

### Request handling path

All HTTP requests are handled by svc and dispatched through `Connector` to the browser. The client never touches Chrome directly.

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
│  GET  /sessions/:id/tabs/:tabId/intercepted → InterceptHandler│
└───────────────────────────┬─────────────────────────────────┘
                            │ calls
                            ▼
┌─────────────────────────────────────────────────────────────┐
│  Connector (interface)                                       │
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
│  ChromeConnector      │     │  ExtensionConnector  │
│  Phase 1             │     │  Phase 4             │
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

A separate goroutine in `ChromeConnector` listens for CDP events on each tab WebSocket. Matching events are written to disk. Independent of the request path.

```
┌─────────────────────────────────────────────────────────────┐
│  ChromeConnector.ReadLoop (goroutine, per tab)             │
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

---

## Implementation Phases

| Phase | Scope | Status |
|-------|-------|--------|
| **Phase 1** | Session lifecycle, navigate, hover, click, type, scroll, evaluate, waitForSelector, screenshot, dom | Current |
| **Phase 2** | Network interception (intercept + read one event at a time) | Planned |
| **Phase 3** | Session metadata persistence across svc restarts | Planned |
| **Phase 4** | ExtensionConnector (production Chrome connection via browserctl extension) | Planned |