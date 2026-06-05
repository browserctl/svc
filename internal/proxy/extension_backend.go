package proxy

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ExtensionBackend is the production backend — it communicates with
// the Browserctl Chrome Extension via the internal WebSocket protocol.
type ExtensionBackend struct {
	logger *slog.Logger

	extWS *extensionWS // single extension connection

	pending map[int64]*extPendingCallback
	mu      sync.Mutex
	_nextId int64

	// Cached tabs from extension
	cachedTabs []Tab
}

type extPendingCallback struct {
	onResult func(result interface{}, errMsg string)
	handled   bool
	handledMu sync.Mutex
}

func (p *extPendingCallback) tryMarkHandled() bool {
	p.handledMu.Lock()
	defer p.handledMu.Unlock()
	if p.handled {
		return false
	}
	p.handled = true
	return true
}

const extWriteWait = 10 * time.Second

// NewExtensionBackend creates a new ExtensionBackend.
func NewExtensionBackend(logger *slog.Logger) *ExtensionBackend {
	return &ExtensionBackend{
		logger:  logger,
		pending: make(map[int64]*extPendingCallback),
	}
}

// Tabs returns cached tabs received from the extension.
func (b *ExtensionBackend) Tabs() []Tab {
	return b.cachedTabs
}

// SendCommand forwards a CDP command to the Chrome Extension.
// It is synchronous — blocks until the extension responds or timeout.
func (b *ExtensionBackend) SendCommand(tabId int, method string, params map[string]interface{}) (interface{}, error) {
	extWS := b.getExtWS()
	if extWS == nil {
		return nil, fmt.Errorf("no extension connected")
	}

	// Strip domain prefix: "Runtime.evaluate" → "evaluate"
	methodName := method
	if idx := lastIndexByte(method, '.'); idx >= 0 {
		methodName = method[idx+1:]
	}

	reqId := b.nextId()
	resultCh := make(chan json.RawMessage, 1)
	errCh := make(chan error, 1)

	b.mu.Lock()
	b.pending[reqId] = &extPendingCallback{
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

	b.sendToExtension(extWS, map[string]interface{}{
		"type":   "cdp_command",
		"tabId":  tabId,
		"method": methodName,
		"params": params,
		"id":     reqId,
	})

	select {
	case <-time.After(30 * time.Second):
		b.mu.Lock()
		delete(b.pending, reqId)
		b.mu.Unlock()
		return nil, fmt.Errorf("timeout after 30s")
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
func (b *ExtensionBackend) Close() error {
	return nil
}

// ─── Extension Connection ─────────────────────────────────────────────────────

// SetExtensionWS sets the extension WebSocket connection.
// Called by ws_server.go when the extension connects.
func (b *ExtensionBackend) SetExtensionWS(ws *extensionWS) {
	b.mu.Lock()
	b.extWS = ws
	b.mu.Unlock()
}

// ClearExtensionWS clears the extension connection.
// Called by ws_server.go when the extension disconnects.
func (b *ExtensionBackend) ClearExtensionWS() {
	b.mu.Lock()
	b.extWS = nil
	b.mu.Unlock()
}

// UpdateTabs updates the cached tab list.
func (b *ExtensionBackend) UpdateTabs(tabs []Tab) {
	b.mu.Lock()
	b.cachedTabs = tabs
	b.mu.Unlock()
}

// ResolvePending resolves a pending callback by id.
// Called by ws_server.go's handleExtMessage when it receives a cdp_result.
func (b *ExtensionBackend) ResolvePending(id int64, result interface{}, errMsg string) {
	b.mu.Lock()
	cb, ok := b.pending[id]
	delete(b.pending, id)
	b.mu.Unlock()

	if ok && cb != nil && cb.onResult != nil {
		if cb.tryMarkHandled() {
			cb.onResult(result, errMsg)
		}
	}
}

// ─── Internal ─────────────────────────────────────────────────────────────────

func (b *ExtensionBackend) getExtWS() *extensionWS {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.extWS
}

func (b *ExtensionBackend) nextId() int64 {
	b.mu.Lock()
	b._nextId++
	id := b._nextId
	b.mu.Unlock()
	return id
}

func (b *ExtensionBackend) sendToExtension(extWS *extensionWS, msg interface{}) {
	if extWS == nil || extWS.ws == nil {
		return
	}
	extWS.sendMu.Lock()
	defer extWS.sendMu.Unlock()
	_ = extWS.ws.SetWriteDeadline(time.Now().Add(extWriteWait))
	_ = extWS.ws.WriteJSON(msg)
}

// PendingCount returns the number of pending callbacks (for health reporting).
func (b *ExtensionBackend) PendingCount() int {
	b.mu.Lock()
	n := len(b.pending)
	b.mu.Unlock()
	return n
}