# Data Storage Design

---

## Base Directory

All data lives under a per-user directory. Default: `~/.browserctl/`

```
~/.browserctl/
├── sessions/
│   └── {session_id}/
│       └── meta.json              ← session metadata (JSON)
└── events/
    └── {session_id}/
        └── intercepted/
            └── 0000000000.jsonl   ← intercepted request+response pairs
```

The directory is created on first use. If `$BROWSERCTL_DATA_DIR` is set, it overrides the default.

---

## Session Metadata

Location: `~/.browserctl/sessions/{session_id}/meta.json`

Written on every state change (session create, close, tab add/remove, intercept activate/deactivate). Uses `os.Rename` (write-then-rename) for atomic updates.

```json
{
  "id":                "s_abc123def",
  "provider":          "chrome",
  "status":            "active",
  "chrome_url":        "ws://localhost:9336/devtools/page/...",
  "created_at":        "2026-05-28T10:00:00Z",
  "updated_at":        "2026-05-28T10:05:00Z",
  "tabs": [
    {
      "id":    "tab_1",
      "url":   "https://example.com",
      "title": "Example"
    }
  ],
  "active_tab": "tab_1",
  "intercept_patterns": ["*doubleclick*"],
  "intercept_seq": 42
}
```

`intercept_seq` tracks the highest sequence number written to the event file. It is updated after each successful write to disk.

---

## Intercepted Events

Location: `~/.browserctl/events/{session_id}/intercepted/`

File format: **JSON Lines** (newline-delimited JSON objects), append-only.

### File Rotation

Files rotate every **100,000 entries**. A new file is started with the next sequence number in the filename:

```
intercepted/
  0000000000.jsonl   ← seq 0    to seq 99999
  0000010000.jsonl   ← seq 100000 to seq 199999
  0000020000.jsonl   ← seq 200000 to seq 299999
  ...
```

Sequence number is zero-padded to 10 digits in the filename. New entries are always appended to the file with the highest sequence number (the "current" file). No in-memory index needed — reads scan from the last known position.

### Event Schema

One JSON object per line, written after the response is received:

```json
{"id":"req_001","tab_id":"tab_1","time":"2026-05-28T10:00:01Z","request":{"url":"https://...","method":"GET","headers":{}},"response":{"status":200,"status_text":"OK","headers":{},"body_base64":""}}
```

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Local incrementing counter (`req_001`, `req_002`, ...), unique per session |
| `tab_id` | string | Internal tab ID (e.g. `tab_1`) |
| `time` | string | RFC 3339 timestamp when the request was initiated |
| `request` | object | Request metadata |
| `request.url` | string | Full URL |
| `request.method` | string | HTTP method (GET, POST, etc.) |
| `request.headers` | object | HTTP request headers |
| `response` | object | Response metadata |
| `response.status` | int | HTTP status code |
| `response.status_text` | string | HTTP status text |
| `response.headers` | object | HTTP response headers |
| `response.body_base64` | string | Full response body, base64-encoded (may be empty) |

### Writing Protocol

```
1. Intercepted request arrives via Chrome CDP Fetch event
2. Buffer request headers in memory (keyed by requestId)
3. When response arrives via Fetch.requestPaused (or the final response event):
   a. Merge with buffered request
   b. Generate "req_NNN" id
   c. appendWrite(line + "\n") to current event file
   d. Update intercept_seq in meta.json
```

`appendWrite` uses `os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)`. The append is atomic per-line because the OS guarantees atomic writes of up to PIPE_BUF bytes (~4KB on Linux). For responses with large bodies (>4KB body), the write is still atomic for the JSON wrapper; the base64 body may span multiple `Write()` calls but the JSON structure itself is built in memory first and written as one complete line.

### Reading Protocol

```
GET /sessions/:id/tabs/:tabId/requests
1. Open the event directory
2. Read all .jsonl files in order
3. Filter lines by tab_id matching the requested tab
4. Return matching entries in chronological order
```

The response is constructed by reading from disk on every request. No in-memory caching of intercepted events — disk is the source of truth.

### No Cleanup

Files are never automatically deleted or rotated out. This is intentional:

- Append-only writes are fast and crash-safe
- No cleanup logic means no risk of data loss
- Retention is managed externally (by the operator or a separate cron job)

A production operator can clean up old session directories once the session is closed and the data is no longer needed.

---

## Directory Layout Summary

```
~/.browserctl/
├── sessions/
│   └── s_abc123def/
│       └── meta.json
└── events/
    └── s_abc123def/
        └── intercepted/
            ├── 0000000000.jsonl     ← seq 0 – 99999
            └── 0000010000.jsonl     ← seq 100000 – 199999
```

---

## Concurrency

- **Single writer**: Only the CDP read loop goroutine writes events. No concurrent writes to the same file.
- **Multiple readers**: The HTTP handler reads event files on demand. Reads use `os.Open` (shared read lock on most filesystems). `O_APPEND` opens are safe for concurrent reads.
- **meta.json updates**: Written via `os.Rename` (write to temp file, then rename over original) to avoid corruption if svc crashes mid-write.
