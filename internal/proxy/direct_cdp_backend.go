package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// DirectCDPBackend connects directly to Chrome's Raw CDP WebSocket.
// This is used for testing mode, where we bypass the extension and talk
// directly to Chrome's DevTools Protocol endpoint.
//
// Transport discovery (Chrome 148+):
//   - Root URL: http://localhost:9336  (CDP debugging port)
//   - Tab list:  GET  {root}/json      → [{id, webSocketDebuggerUrl, ...}]
//   - Tab WS:    WS   {webSocketDebuggerUrl}
//
// Note: Chrome 148 headless removed /devtools/browser — tabs must be discovered
// via /json and connected individually via their /devtools/page/<id> path.
//
// Architecture:
//   - Single readLoop goroutine is the ONLY reader of the WebSocket
//   - SendCommand() only WRITES; never reads
//   - readLoop dispatches responses to pending callbacks by id
//   - This matches the chrome-use SDK pattern and avoids dual-read competition
type DirectCDPBackend struct {
	logger *slog.Logger
	rootUrl string // e.g. "http://localhost:9336" (CDP debugging port)

	conn    *websocket.Conn
	readLoopCtx context.Context
	readLoopCancel context.CancelFunc

	pending map[int64]*directPendingCallback
	mu      sync.Mutex
	_nextId int64

	readLoopDone chan struct{}
}

type directPendingCallback struct {
	onResult func(result interface{}, errMsg string)
}

const (
	directWriteWait = 10 * time.Second
	directPongWait  = 60 * time.Second
	directMaxSize   = 512 * 1024
	defaultTimeout  = 30 * time.Second
)

// NewDirectCDPBackend creates a new DirectCDPBackend.
// cdpUrl is the Chrome CDP debugging root URL, e.g. "http://localhost:9336".
// The backend discovers tabs via GET {cdpUrl}/json on first Tabs() call.
func NewDirectCDPBackend(logger *slog.Logger, cdpUrl string) *DirectCDPBackend {
	return &DirectCDPBackend{
		logger:  logger,
		rootUrl: cdpUrl,
		pending: make(map[int64]*directPendingCallback),
	}
}

// Tabs returns the list of available page tabs.
// Each Tab.ID is a sequential routing ID starting from 1, not the Chrome tab UUID.
// These routing IDs are used by ws_server.go to route commands to the correct tab.
// In direct mode there is only one WS connection, so commands always go to routing ID 1.
func (b *DirectCDPBackend) Tabs() []Tab {
	tabs, err := b.discoverTabs()
	if err != nil {
		b.logger.Debug("Tabs: discoverTabs failed", "err", err)
		return nil
	}

	result := make([]Tab, 0, len(tabs))
	routingId := 1
	for _, t := range tabs {
		if t.Type != "page" {
			continue
		}
		result = append(result, Tab{
			ID:    routingId,
			Title: t.Title,
			URL:   t.URL,
		})
		routingId++
	}
	return result
}

// SendCommand sends a CDP command to Chrome via the raw CDP WebSocket.
// It is synchronous — blocks until the response arrives or timeout.
// tabId is used to establish/verify the CDP session for the given tab.
// method is the CDP method name (e.g. "Runtime.evaluate").
// params are the CDP parameters (may be nil).
func (b *DirectCDPBackend) SendCommand(tabId int, method string, params map[string]interface{}) (interface{}, error) {
	if err := b.ensureConnected(); err != nil {
		return nil, fmt.Errorf("connection: %w", err)
	}

	// Strip domain prefix: "Runtime.evaluate" → "evaluate"
	methodName := method
	if idx := lastIndexByte(method, '.'); idx >= 0 {
		methodName = method[idx+1:]
	}

	reqId := b.nextId()
	resultCh := make(chan json.RawMessage, 1)
	errCh := make(chan error, 1)

	// Register pending callback BEFORE sending
	b.mu.Lock()
	b.pending[reqId] = &directPendingCallback{
		onResult: func(result interface{}, errMsg string) {
			if errMsg != "" {
				select { case errCh <- fmt.Errorf("%s", errMsg): default: }
				return
			}
			if raw, ok := result.(json.RawMessage); ok {
				select { case resultCh <- raw: default: }
			} else {
				select { case errCh <- fmt.Errorf("unexpected result type %T", result): default: }
			}
		},
	}
	b.mu.Unlock()

	// Build command payload.
	// In direct mode we are connected to the tab's own WS endpoint, so the
	// connection IS the routing — no sessionId injection needed.
	payload := map[string]interface{}{
		"id":     reqId,
		"method": methodName,
		"params": params,
	}

	if err := b.writeJSON(payload); err != nil {
		b.mu.Lock()
		delete(b.pending, reqId)
		b.mu.Unlock()
		return nil, fmt.Errorf("write: %w", err)
	}

	// Wait for response or timeout
	select {
	case <-b.readLoopCtx.Done():
		return nil, fmt.Errorf("backend closed")
	case <-time.After(defaultTimeout):
		b.mu.Lock()
		delete(b.pending, reqId)
		b.mu.Unlock()
		return nil, fmt.Errorf("timeout after %v", defaultTimeout)
	case raw := <-resultCh:
		var result map[string]interface{}
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("unmarshal result: %w", err)
		}
		return result, nil
	case err := <-errCh:
		return nil, err
	}
}

// Close shuts down the backend.
func (b *DirectCDPBackend) Close() error {
	if b.readLoopCancel != nil {
		b.readLoopCancel()
	}
	if b.conn != nil {
		return b.conn.Close()
	}
	return nil
}

// ─── Internal ────────────────────────────────────────────────────────────────

// discoverTabs queries Chrome /json endpoint and returns the tab list.
func (b *DirectCDPBackend) discoverTabs() ([]chromeTabInfo, error) {
	reqUrl := strings.TrimSuffix(b.rootUrl, "/") + "/json"
	resp, err := http.Get(reqUrl)
	if err != nil {
		return nil, fmt.Errorf("http get %s: %w", reqUrl, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			b.logger.Debug("resp.Body.Close", "err", cerr)
		}
	}()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("json endpoint: status %d", resp.StatusCode)
	}
	var tabs []chromeTabInfo
	if err := json.NewDecoder(resp.Body).Decode(&tabs); err != nil {
		return nil, fmt.Errorf("decode tabs: %w", err)
	}
	return tabs, nil
}

// ensureConnected discovers the first "page" tab via /json and connects to its WS.
func (b *DirectCDPBackend) ensureConnected() error {
	b.mu.Lock()
	connected := b.conn != nil
	b.mu.Unlock()
	if connected {
		return nil
	}

	tabs, err := b.discoverTabs()
	if err != nil {
		return fmt.Errorf("discover tabs: %w", err)
	}

	var wsUrl string
	for _, t := range tabs {
		if t.Type == "page" {
			wsUrl = t.WebSocketDebuggerURL
			break
		}
	}
	if wsUrl == "" {
		return fmt.Errorf("no page tab found in /json")
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsUrl, nil)
	if err != nil {
		return fmt.Errorf("dial %s: %w", wsUrl, err)
	}

	b.mu.Lock()
	b.conn = conn
	b.mu.Unlock()

	b.readLoopCtx, b.readLoopCancel = context.WithCancel(context.Background())
	b.readLoopDone = make(chan struct{})

	go b.readLoop()

	b.logger.Info("DirectCDPBackend connected", "wsUrl", wsUrl)
	return nil
}

// chromeTabInfo mirrors Chrome's /json output shape.
type chromeTabInfo struct {
	Type                  string `json:"type"`
	ID                    string `json:"id"`
	Title                 string `json:"title"`
	URL                   string `json:"url"`
	WebSocketDebuggerURL   string `json:"webSocketDebuggerUrl"`
}

func (b *DirectCDPBackend) readLoop() {
	defer close(b.readLoopDone)

	for {
		conn := b.getConn()
		if conn == nil {
			return
		}

		conn.SetReadLimit(directMaxSize)
		if err := conn.SetReadDeadline(time.Now().Add(directPongWait)); err != nil {
			b.logger.Debug("SetReadDeadline", "err", err)
		}
		conn.SetPongHandler(func(string) error {
			if err := conn.SetReadDeadline(time.Now().Add(directPongWait)); err != nil {
				b.logger.Debug("SetReadDeadline in pong handler", "err", err)
			}
			return nil
		})

		_, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				b.logger.Debug("DirectCDPBackend read error", "err", err)
			}
			b.mu.Lock()
			b.conn = nil
			b.mu.Unlock()
			return
		}

		b.dispatch(data)
	}
}

func (b *DirectCDPBackend) getConn() *websocket.Conn {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.conn
}

func (b *DirectCDPBackend) dispatch(data []byte) {
	var base struct {
		ID     int64           `json:"id"`
		Result json.RawMessage `json:"result,omitempty"`
		Error  json.RawMessage `json:"error,omitempty"`
		Method string          `json:"method,omitempty"`
	}
	if err := json.Unmarshal(data, &base); err != nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if base.ID != 0 && base.Method == "" {
		cb, ok := b.pending[base.ID]
		if !ok {
			return
		}
		delete(b.pending, base.ID)

		var errMsg string
		if len(base.Error) > 0 && string(base.Error) != "null" {
			errMsg = string(base.Error)
		}
		if errMsg != "" {
			cb.onResult(nil, errMsg)
		} else {
			cb.onResult(base.Result, "")
		}
	} else if base.Method != "" {
		b.logger.Debug("DirectCDPBackend event", "method", base.Method)
	}
}

func (b *DirectCDPBackend) writeJSON(v interface{}) error {
	conn := b.getConn()
	if conn == nil {
		return fmt.Errorf("not connected")
	}
	if err := conn.SetWriteDeadline(time.Now().Add(directWriteWait)); err != nil {
		return err
	}
	return conn.WriteJSON(v)
}

func (b *DirectCDPBackend) nextId() int64 {
	b.mu.Lock()
	b._nextId++
	id := b._nextId
	b.mu.Unlock()
	return id
}

// lastIndexByte returns the last index of c in s, or -1 if not found.
func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}