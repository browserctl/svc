# HTTP API Specification

All endpoints return `Content-Type: application/json` except `/screenshot` which returns `image/png`. Errors use the shape:

```json
{ "error": "human-readable message", "reason": "optional detail" }
```

Base URL: `http://localhost:9222`

---

## Session

### `POST /sessions` — Create session

Creates a new session. In **launch mode** (default), svc launches a new Chrome process with the user's default profile. In **connect mode**, svc connects to an already-running Chrome via its remote debug port.

**Launch mode** (no body required):

```bash
curl -X POST http://localhost:9222/sessions
```

```json
// request (empty body or omit fields)
{}
```

```json
// response 200
{
  "id": "s_abc123def",
  "provider": "chrome",
  "status": "active",
  "chrome_url": "ws://localhost:9336/devtools/page/...",
  "tabs": [
    { "id": "tab_1", "url": "about:blank", "title": "" }
  ],
  "intercept_patterns": []
}
```

**Connect mode** (provide `cdp_url`):

```bash
curl -X POST http://localhost:9222/sessions \
  -H "Content-Type: application/json" \
  -d '{"cdp_url": "http://localhost:9336"}'
```

```json
// request
{ "cdp_url": "http://localhost:9336" }
```

```json
// response 200 — same shape as launch mode
{ "id": "s_abc123def", "provider": "chrome", ... }
```

| Field | Type | Description |
|-------|------|-------------|
| `cdp_url` | string | HTTP URL of Chrome's remote debug port (e.g. `http://localhost:9336`). Omit to launch a new Chrome process. |

**Errors:**

| Status | Condition |
|--------|-----------|
| 400 | `cdp_url` is not reachable |
| 503 | Chrome launch failed or Chrome process exited unexpectedly |

---

### `GET /sessions/:id` — Reuse session

Returns the current state of an existing session. Used by client to confirm session is still alive and to retrieve the current tab list before issuing commands.

```bash
curl http://localhost:9222/sessions/s_abc123def
```

```json
// response 200
{
  "id": "s_abc123def",
  "provider": "chrome",
  "status": "active",
  "tabs": [
    { "id": "tab_1", "url": "https://www.biquge555.com/xx/12345/", "title": "笔趣阁" }
  ],
  "intercept_patterns": ["*doubleclick*"]
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| 404 | Session not found |

---

### `DELETE /sessions/:id` — Close session

Closes all tabs in the session, disconnects from Chrome (svc does **not** kill browser processes it connects to — Chrome is left running with its user's profile, cookies, and extensions intact). Marks the session as closed.

```bash
curl -X DELETE http://localhost:9222/sessions/s_abc123def
```

```json
// response 200
{ "id": "s_abc123def", "status": "closed" }
```

**Errors:**

| Status | Condition |
|--------|-----------|
| 404 | Session not found |
| 409 | Session already closed |

---

## Tabs

### `GET /sessions/:id/tabs` — List tabs

```bash
curl http://localhost:9222/sessions/s_abc123def/tabs
```

```json
// response 200
{
  "tabs": [
    { "id": "tab_1", "url": "https://example.com", "title": "Example" },
    { "id": "tab_2", "url": "about:blank", "title": "" }
  ],
  "active_tab": "tab_1"
}
```

---

### `POST /sessions/:id/tabs` — Open new tab

```bash
curl -X POST http://localhost:9222/sessions/s_abc123def/tabs \
  -H "Content-Type: application/json" \
  -d '{"url": "https://example.com"}'
```

```json
// request (all fields optional)
{ "url": "https://example.com" }
```

```json
// response 200
{
  "id": "tab_2",
  "url": "about:blank",
  "title": ""
}
```

If `url` is omitted, opens `about:blank`.

---

## Page Actions

All page action endpoints return HTTP 200 on success with a result body (if any). On failure, return HTTP 4xx/5xx with an error body.

---

### `POST /sessions/:id/tabs/:tabId/navigate`

Navigates the tab to a URL.

```bash
curl -X POST http://localhost:9222/sessions/s_abc123def/tabs/tab_1/navigate \
  -H "Content-Type: application/json" \
  -d '{"url": "https://example.com", "wait_until": "networkidle"}'
```

```json
// request
{
  "url": "https://example.com",
  "wait_until": "networkidle"   // optional: "load" | "domcontentloaded" | "networkidle" (default)
}
```

```json
// response 200
{
  "url": "https://example.com",
  "title": "Example Domain"
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `url` | string | required | Target URL |
| `wait_until` | string | `"domcontentloaded"` | Wait until which load event before returning |

**Errors:**

| Status | Condition |
|--------|-----------|
| 400 | Invalid URL |
| 404 | Session or tab not found |
| 504 | `wait_until` condition not met within Chrome's default timeout |

---

### `POST /sessions/:id/tabs/:tabId/click`

Clicks an element identified by a CSS selector.

```bash
curl -X POST http://localhost:9222/sessions/s_abc123def/tabs/tab_1/click \
  -H "Content-Type: application/json" \
  -d '{"selector": "button.submit", "timeout": 10000}'
```

```json
// request
{
  "selector": "button.submit",
  "timeout": 10000    // optional: milliseconds to wait for element (default: 10000)
}
```

```json
// response 200
{}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| 400 | Element not found or not actionable |
| 404 | Session or tab not found |

---

### `POST /sessions/:id/tabs/:tabId/type`

Types text into an element identified by a CSS selector. Clears the element first if it has editable content.

```bash
curl -X POST http://localhost:9222/sessions/s_abc123def/tabs/tab_1/type \
  -H "Content-Type: application/json" \
  -d '{"selector": "input[name=email]", "value": "hello@example.com"}'
```

```json
// request
{
  "selector": "input[name=email]",
  "value": "hello@example.com"
}
```

```json
// response 200
{}
```

---

### `POST /sessions/:id/tabs/:tabId/hover`

Moves the mouse cursor over an element identified by a CSS selector. Triggers `mouseenter` / `mouseover` events — useful for revealing dropdowns, tooltips, or lazy-loaded content.

```bash
curl -X POST http://localhost:9222/sessions/s_abc123def/tabs/tab_1/hover \
  -H "Content-Type: application/json" \
  -d '{"selector": "a.dropdown-toggle"}'
```

```json
// request
{ "selector": "a.dropdown-toggle" }
```

```json
// response 200
{}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| 400 | Element not found |
| 404 | Session or tab not found |

---

### `POST /sessions/:id/tabs/:tabId/scroll`

Scrolls the page by a pixel offset. Positive `y` scrolls down; negative `y` scrolls up.

```bash
curl -X POST http://localhost:9222/sessions/s_abc123def/tabs/tab_1/scroll \
  -H "Content-Type: application/json" \
  -d '{"y": 500, "x": 0}'
```

```json
// request
{
  "y": 500,    // vertical scroll offset in pixels
  "x": 0       // horizontal scroll offset in pixels (default: 0)
}
```

```json
// response 200
{}
```

---

### `POST /sessions/:id/tabs/:tabId/evaluate`

Executes a JavaScript expression in the page context. The expression must be a function (optionally an arrow function with no parameters) — it is called with no arguments.

```bash
curl -X POST http://localhost:9222/sessions/s_abc123def/tabs/tab_1/evaluate \
  -H "Content-Type: application/json" \
  -d '{"script": "() => document.title"}'
```

```json
// request
{ "script": "() => document.title" }
```

```json
// response 200
{ "result": "Example Domain" }
```

Scripts that return DOM nodes return `"[object HTMLDivElement]"` etc. Use `outerHTML` or `innerText` explicitly:

```json
{ "script": "() => document.querySelector('.content').innerText" }
```

```json
// response 200
{ "result": "第一章 逆天改命\n\n天地玄黄，宇宙洪荒..." }
```

**Errors:**

| Status | Condition |
|--------|-----------|
| 400 | JavaScript execution error or syntax error |
| 404 | Session or tab not found |

---

### `POST /sessions/:id/tabs/:tabId/waitForSelector`

Waits for a CSS selector to match a desired state before returning.

```bash
curl -X POST http://localhost:9222/sessions/s_abc123def/tabs/tab_1/waitForSelector \
  -H "Content-Type: application/json" \
  -d '{"selector": ".chapter-detail-article p", "timeout": 10000}'
```

```json
// request
{
  "selector": ".chapter-detail-article p",
  "state":    "visible",   // optional: "visible" (default) | "hidden" | "attached" | "detached"
  "timeout":  10000       // optional: milliseconds (default: 10000)
}
```

```json
// response 200
{ "found": true }
```

**State semantics:**

| State | Description |
|-------|-------------|
| `visible` | Element in DOM and visible (default) |
| `hidden` | Element absent or not visible (`display:none`, `visibility:hidden`, hidden `<input>`) |
| `attached` | Element present in DOM, regardless of visibility |
| `detached` | Element removed from DOM |

**Errors:**

| Status | Condition |
|--------|-----------|
| 400 | Invalid selector syntax |
| 404 | Session or tab not found |
| 504 | Selector did not match the desired state within `timeout` |

---

## Page State

### `GET /sessions/:id/tabs/:tabId/screenshot`

Returns a full-page PNG screenshot of the current tab.

```bash
curl http://localhost:9222/sessions/s_abc123def/tabs/tab_1/screenshot -o screenshot.png
```

```
Response: image/png binary
Content-Type: image/png
```

---

### `GET /sessions/:id/tabs/:tabId/dom`

Returns the full HTML of the current tab.

```bash
curl http://localhost:9222/sessions/s_abc123def/tabs/tab_1/dom
```

```json
// response 200
{
  "html": "<html><head>...</head><body>...</body></html>"
}
```

---

## Network Interception

### `POST /sessions/:id/intercept` — Set intercept patterns

Activates passive network monitoring. Requests matching any of the provided URL patterns will have their request and response recorded to disk.

Calling this again replaces the previous patterns.

```bash
curl -X POST http://localhost:9222/sessions/s_abc123def/intercept \
  -H "Content-Type: application/json" \
  -d '{"patterns": ["*google-analytics*", "*doubleclick*", "*facebook.net*"]}'
```

```json
// request
{
  "patterns": ["*pattern1*", "*pattern2*"]
}
```

```json
// response 200
{
  "patterns": ["*google-analytics*", "*doubleclick*", "*facebook.net*"],
  "active": true
}
```

- Patterns use `filepath.Match` semantics: `*` matches any sequence of characters.
- Empty `patterns` array `[]` **disables** interception.
- Calling without a body returns the current patterns without changes.

**Errors:**

| Status | Condition |
|--------|-----------|
| 400 | Invalid pattern (empty string, or not a valid glob pattern) |

---

### `GET /sessions/:id/tabs/:tabId/requests` — Pull intercepted requests

Returns all intercepted request+response pairs for this tab since the session started or since intercept was last activated. Results are read from the event log file on disk and returned in chronological order.

```bash
curl http://localhost:9222/sessions/s_abc123def/tabs/tab_1/requests
```

```json
// response 200
{
  "requests": [
    {
      "id": "req_001",
      "tab_id": "tab_1",
      "time": "2026-05-28T10:00:01Z",
      "request": {
        "url": "https://www.google-analytics.com/collect?v=1&tid=UA-...",
        "method": "GET",
        "headers": {
          "User-Agent": "Mozilla/5.0 ...",
          "Referer": "https://example.com/"
        }
      },
      "response": {
        "status": 200,
        "status_text": "OK",
        "headers": {
          "Content-Type": "image/gif",
          "Cache-Control": "no-cache"
        },
        "body_base64": "R0lGODlhAQABAIAAAAAAAP..."
      }
    },
    {
      "id": "req_002",
      "tab_id": "tab_1",
      "time": "2026-05-28T10:00:02Z",
      "request": { "url": "...", "method": "POST", "headers": {...} },
      "response": { "status": 204, "status_text": "No Content", "headers": {...}, "body_base64": "" }
    }
  ]
}
```

- `body_base64` is the **full** response body, base64-encoded. Empty string if no body.
- Each call returns **all** intercepted requests since session start — client is responsible for deduplication if needed.
- If interception is not active (`patterns` is empty), `requests` is an empty array.

---

## Global Error Responses

All endpoints may return these error shapes:

```json
// 404 Not Found
{ "error": "session not found" }

// 400 Bad Request
{ "error": "invalid request", "reason": "selector is required" }

// 410 Gone
{ "error": "session already closed" }

// 503 Service Unavailable
{ "error": "chrome disconnected" }

// 504 Gateway Timeout
{ "error": "timeout", "reason": "wait_until condition not met" }
```
