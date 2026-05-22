package proxy

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// newTestServer creates a CdpServer on a random port.
// Returns the server instance and the actual port for WebSocket dial.
func newTestServer(t *testing.T) (*CdpServer, int) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	server := NewCdpServer(0, "", logger)
	if err := server.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	t.Cleanup(func() { server.Stop() })
	time.Sleep(50 * time.Millisecond) // let port bind
	return server, server.Port()
}

func wsDial(t *testing.T, url string) *websocket.Conn {
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	return conn
}

func wsReadJSON(t *testing.T, conn *websocket.Conn, v interface{}) {
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := conn.ReadJSON(v); err != nil {
		t.Fatalf("ws read failed: %v", err)
	}
}

func wsWriteJSON(t *testing.T, conn *websocket.Conn, v interface{}) {
	if err := conn.WriteJSON(v); err != nil {
		t.Fatalf("ws write failed: %v", err)
	}
}

// ─── P0.1: GET /json/version ────────────────────────────────────────────────

// TestJsonVersionEndpoint verifies the Chrome CDP HTTP discovery endpoint.
// Chrome native: GET /json/version returns Browser metadata.
// browserctl currently: returns "Use /extension or /devtools/... paths" (400).
//
// Required fields per Chrome CDP protocol:
//   - Browser: "Chrome/VERSION"
//   - Protocol-Version: "1.3"
//   - webSocketDebuggerUrl: "ws://host:port/devtools/browser/..."
//
// Ref: https://chromedevtools.github.io/devtools-protocol/
func TestJsonVersionEndpoint(t *testing.T) {
	server, _ := newTestServer(t)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/json/version", nil)
	server.handleWS(resp, req)

	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d. body: %s", resp.Code, resp.Body.String())
		return
	}

	var result map[string]interface{}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	// Required by Chrome CDP protocol
	for _, field := range []string{"Browser", "Protocol-Version", "webSocketDebuggerUrl"} {
		if _, ok := result[field]; !ok {
			t.Errorf("json/version missing required field: %q", field)
		}
	}

	// Protocol-Version must be "1.3" per CDP spec
	if pv, _ := result["Protocol-Version"].(string); pv != "1.3" {
		t.Errorf("Protocol-Version: expected '1.3', got %q", pv)
	}

	// webSocketDebuggerUrl must be a valid ws:// URL
	if wsu, _ := result["webSocketDebuggerUrl"].(string); !strings.HasPrefix(wsu, "ws://") {
		t.Errorf("webSocketDebuggerUrl: expected ws:// prefix, got %q", wsu)
	}

	// Browser must contain "Chrome"
	if browser, _ := result["Browser"].(string); !strings.Contains(browser, "Chrome") {
		t.Errorf("Browser: expected 'Chrome/...', got %q", browser)
	}
}

// ─── P0.2: GET /json/list ───────────────────────────────────────────────────

// TestJsonListEndpoint verifies the Chrome CDP tab discovery endpoint.
// Chrome native: GET /json/list returns all tab targets.
// browserctl currently: returns "Use /extension or /devtools/... paths" (400).
//
// Required per-tab fields:
//   - id: string starting with "tab-" (e.g. "tab-123")
//   - type: "page"
//   - title: string
//   - url: full URL string
//   - webSocketDebuggerUrl: ws://host:port/devtools/page/{id}
//
// Ref: https://chromedevtools.github.io/devtools-protocol/
func TestJsonListEndpoint(t *testing.T) {
	server, _ := newTestServer(t)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/json/list", nil)
	server.handleWS(resp, req)

	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d. body: %s", resp.Code, resp.Body.String())
		return
	}

	var result []map[string]interface{}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("response is not valid JSON array: %v", err)
	}

	// Each tab must have required fields per CDP spec
	for i, tab := range result {
		for _, field := range []string{"id", "type", "title", "url", "webSocketDebuggerUrl"} {
			if _, ok := tab[field]; !ok {
				t.Errorf("tab[%d] missing required field: %q", i, field)
			}
		}

		if typ, _ := tab["type"].(string); typ != "page" {
			t.Errorf("tab[%d] type: expected 'page', got %q", i, typ)
		}

		if wsu, _ := tab["webSocketDebuggerUrl"].(string); !strings.Contains(wsu, "/devtools/page/") {
			t.Errorf("tab[%d] webSocketDebuggerUrl: expected /devtools/page/, got %q", i, wsu)
		}

		if id, _ := tab["id"].(string); !strings.HasPrefix(id, "tab-") {
			t.Errorf("tab[%d] id: expected 'tab-...' format, got %q", i, id)
		}
	}
}

// ─── P0.2 variant: GET /json/new?url=... ───────────────────────────────────

// TestJsonNewEndpoint tests creating a new tab via HTTP.
// Chrome native: GET /json/new?url=https://example.com creates a new tab.
// Current behavior: returns 400 (not implemented).
func TestJsonNewEndpoint(t *testing.T) {
	server, _ := newTestServer(t)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/json/new?url=https://example.com", nil)
	server.handleWS(resp, req)

	if resp.Code != http.StatusOK {
		t.Logf("NOTE: /json/new not implemented yet, got %d. body: %s", resp.Code, resp.Body.String())
	}
}

// ─── P0.3: CDP Events Mishandled in readClientLoop ─────────────────────────

// TestCdpEventRouting verifies that CDP events (messages with no "id" field,
// only a "method" field) are NOT routed to dispatchCdpCommand.
//
// Chrome CDP protocol distinguishes:
//   - Request/Response: {"id":1, "method":"Page.captureScreenshot", "params":{}}
//   - Event (notification): {"method":"Page.loadEventFired", "params":{}}
//
// Bug: readClientLoop routes ALL messages to dispatchCdpCommand.
// Events have req.ID == 0, so dispatchCdpCommand returns "session required" error.
//
// Fix: Events should be routed to routeEventToClients, NOT dispatchCdpCommand.
func TestCdpEventRouting(t *testing.T) {
	_, port := newTestServer(t)

	wsURL := fmt.Sprintf("ws://localhost:%d/devtools/page/test-tab", port)
	conn := wsDial(t, wsURL)
	defer conn.Close()

	// Drain initial getTargets response
	var discard map[string]interface{}
	wsReadJSON(t, conn, &discard)

	// Send a CDP event (no "id" field — this is the distinguishing characteristic)
	// This is exactly what Chrome sends when a page event fires.
	event := map[string]interface{}{
		"method": "Page.loadEventFired",
		"params": map[string]interface{}{"timestamp": 1234567890.0},
	}
	wsWriteJSON(t, conn, event)

	// Read with a short deadline. Two possible outcomes:
	//
	// BUG (current): dispatchCdpCommand is called → returns error response:
	//   {"id":0,"error":{"code":-32602,"message":"session required"}}
	//
	// CORRECT: routeEventToClients is called → no response sent to client:
	//   (timeout after 1 second = correct, events produce no response)
	conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	var resp map[string]interface{}
	err := conn.ReadJSON(&resp)

	if err == nil {
		if errObj, ok := resp["error"].(map[string]interface{}); ok {
			if msg, _ := errObj["message"].(string); msg == "session required" {
				t.Errorf("P0.3 REGRESSION: CDP event was routed to dispatchCdpCommand, got 'session required' error. " +
					"Events (no id) must be routed to routeEventToClients, not dispatchCdpCommand.")
			}
		}
		t.Logf("NOTE: Received unexpected response to event: %v", resp)
	}
	// timeout (err != nil) is CORRECT — events produce no response
}

// ─── P0.4: Go Client Read-Write Race ───────────────────────────────────────

// TestClientReadWriteRace is an integration test that verifies the race
// condition in cli/client/client.go.
//
// Current architecture (buggy):
//   goroutine 1: readLoop() → conn.ReadJSON() (blocks)
//   goroutine 2: Send() → conn.WriteJSON() + conn.ReadJSON() ← RACES with goroutine 1!
//
// chrome-use SDK correct architecture:
//   goroutine 1: readLoop() → pending map → dispatch to Send() channels
//   goroutines 2-N: Send() → conn.WriteJSON() only (never reads)
//
// Expected with buggy code: at least one of 3 concurrent Eval calls times out.
// Expected after fix: all 3 complete within 30s.
func TestClientReadWriteRace(t *testing.T) {
	// Requires running browserctl-svc on port 9222.
	// Run as: go test -v -run TestClientReadWriteRace ./...
	t.Skip("P0.4: Integration test — requires running browserctl-svc on port 9222")
}

// ─── P0.5: Target.attachToTarget SessionId Management ────────────────────────

// TestAttachToTargetFormat verifies the sessionId format returned by
// Target.attachToTarget.
//
// Chrome native CDP:
//   Response: {"id":1,"result":{"sessionId":"A0B1C2D3E4F5...","success":true}}
//   (sessionId is a hex string assigned by Chrome, NOT "cs-123")
//
// browserctl current (buggy):
//   Response: {"id":1,"result":{"sessionId":"cs-123"}}
//   (always returns "cs-" prefix, not a real Chrome session ID)
//
// After P0.5 fix:
//   - sessionId should be a unique hex string (not "cs-" prefix)
//   - Multiple clients attaching to same tab should get DIFFERENT sessionIds
//   - sessionIds should be usable in subsequent commands via SessionId field
func TestAttachToTargetFormat(t *testing.T) {
	// Requires running browserctl-svc with active Chrome extension.
	t.Skip("P0.5: Integration test — requires running browserctl-svc with active extension")
}

// TestSessionIsolation verifies that multiple clients can attach to the
// same tab and maintain independent sessions.
func TestSessionIsolation(t *testing.T) {
	t.Skip("P0.5: Integration test — requires running browserctl-svc with active extension")
}

// ─── P1.1: Browser.getVersion product hardcoded ─────────────────────────────

// TestBrowserGetVersionProduct verifies that Browser.getVersion returns
// the actual Chrome version, not a hardcoded placeholder.
func TestBrowserGetVersionProduct(t *testing.T) {
	_, port := newTestServer(t)

	wsURL := fmt.Sprintf("ws://localhost:%d/devtools/browser", port)
	conn := wsDial(t, wsURL)
	defer conn.Close()

	// Drain any initial message
	var discard map[string]interface{}
	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	conn.ReadJSON(&discard)

	// Send Browser.getVersion
	req := map[string]interface{}{
		"id":     1,
		"method": "Browser.getVersion",
		"params": map[string]interface{}{},
	}
	wsWriteJSON(t, conn, req)

	var resp map[string]interface{}
	wsReadJSON(t, conn, &resp)

	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result map, got: %v", resp)
	}

	product, _ := result["product"].(string)
	// Currently hardcoded to "Chrome/999.0.0" (P1.1 bug)
	if product == "Chrome/999.0.0" {
		t.Errorf("P1.1: Browser.getVersion product is hardcoded to 'Chrome/999.0.0'. " +
			"Should return actual Chrome version.")
	}

	// After fix, should contain "Chrome/" with a real version
	if !strings.Contains(product, "Chrome/") {
		t.Errorf("Browser.getVersion product: expected 'Chrome/VERSION', got %q", product)
	}
}

// ─── P1.2: targetId/sessionId format inconsistency ─────────────────────────

// TestTargetIdFormatConsistency verifies that all CDP responses use
// consistent targetId/sessionId formats throughout the codebase.
//
// Current mixing of formats:
//   - /json/list tab.id → "tab-{numeric}"
//   - Target.attachToTarget result → "cs-{numeric}"  (wrong prefix)
//   - buildTargetList() → "tab-{numeric}"
//
// After P1.2 fix: all external-facing IDs should use "tab-{numeric}" format.
func TestTargetIdFormatConsistency(t *testing.T) {
	_, port := newTestServer(t)

	wsURL := fmt.Sprintf("ws://localhost:%d/devtools/browser", port)
	conn := wsDial(t, wsURL)
	defer conn.Close()

	// Drain initial message
	var discard map[string]interface{}
	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	conn.ReadJSON(&discard)

	// Send Target.getTargets
	req := map[string]interface{}{
		"id":     1,
		"method": "Target.getTargets",
		"params": map[string]interface{}{},
	}
	wsWriteJSON(t, conn, req)

	var resp map[string]interface{}
	wsReadJSON(t, conn, &resp)

	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result map, got: %v", resp)
	}

	targets, ok := result["targetInfos"].([]interface{})
	if !ok {
		t.Fatalf("expected targetInfos array, got: %v", result)
	}

	for i, targetRaw := range targets {
		target, ok := targetRaw.(map[string]interface{})
		if !ok {
			t.Errorf("target[%d] is not a map", i)
			continue
		}

		targetId, _ := target["targetId"].(string)
		if !strings.HasPrefix(targetId, "tab-") {
			t.Errorf("P1.2: target[%d].targetId: expected 'tab-' prefix, got %q", i, targetId)
		}
	}
}

// ─── P1.3: Target.createTarget return value inconsistency ────────────────────

// TestCreateTargetReturnValue verifies that Target.createTarget returns
// "targetId" (not "sessionId") in its result, consistent with CDP spec.
func TestCreateTargetReturnValue(t *testing.T) {
	t.Skip("P1.3: Integration test — requires active Chrome extension")
}

// ─── P1.4: close_tab / switch_tab fire-and-forget ─────────────────────────

// TestCloseTabAwaitsExtension verifies that Target.closeTarget waits for
// extension confirmation before returning success to the client.
func TestCloseTabAwaitsExtension(t *testing.T) {
	t.Skip("P1.4: Integration test — requires active Chrome extension")
}
