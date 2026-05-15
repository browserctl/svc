package proxy

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ─── Router Tests ─────────────────────────────────────────────────────────────

func TestParseSessionId(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{"basic", "cs-123", 123},
		{"large", "cs-99999", 99999},
		{"with_extra_dash", "cs-456-extra", 456},
		{"invalid_prefix", "tab-123", 0},
		{"empty", "", 0},
		{"cs_only", "cs-", 0},
		{"no_dash", "cs123", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseSessionId(tt.input)
			if got != tt.expected {
				t.Errorf("ParseSessionId(%q) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"https", "https://www.example.com/path", "www.example.com"},
		{"http", "http://api.github.com/v3/users", "api.github.com"},
		{"with_port", "https://localhost:9223/", "localhost"},
		{"with_query", "https://google.com/search?q=test", "google.com"},
		{"https_no_path", "https://github.com", "github.com"},
		{"http_root", "http://example.com/", "example.com"},
		{"short", "http://a.b", "a.b"},
		{"non_http", "chrome://settings", ""},
		{"ftp", "ftp://files.com", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDomain(tt.url)
			if got != tt.expected {
				t.Errorf("extractDomain(%q) = %q, want %q", tt.url, got, tt.expected)
			}
		})
	}
}

func TestIsHttp(t *testing.T) {
	tests := []struct {
		url      string
		expected bool
	}{
		{"https://example.com", true},
		{"http://example.com", true},
		{"https://localhost:9223", true},
		{"chrome://extensions", false},
		{"about:blank", false},
		{"file:///tmp/test.html", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := isHttp(tt.url)
			if got != tt.expected {
				t.Errorf("isHttp(%q) = %v, want %v", tt.url, got, tt.expected)
			}
		})
	}
}

func TestRouterRegisterWindow(t *testing.T) {
	r := NewRouter(newTestLogger())

	ws1 := &extensionWS{windowId: 1}
	r.RegisterWindow(1, ws1)
	if r.firstWindowId != 1 {
		t.Errorf("firstWindowId = %d, want 1", r.firstWindowId)
	}

	ws2 := &extensionWS{windowId: 2}
	r.RegisterWindow(2, ws2)
	if r.firstWindowId != 1 {
		t.Errorf("firstWindowId changed to %d, want 1", r.firstWindowId)
	}

	r.UnregisterWindow(1)
	if r.firstWindowId != 2 {
		t.Errorf("after unregister 1, firstWindowId = %d, want 2", r.firstWindowId)
	}
}

func TestRouterUpdateTabs(t *testing.T) {
	r := NewRouter(newTestLogger())

	tabs := []Tab{
		{ID: 10, WindowId: 1, URL: "https://google.com", Title: "Google"},
		{ID: 11, WindowId: 1, URL: "https://github.com", Title: "GitHub"},
		{ID: 12, WindowId: 2, URL: "chrome://extensions", Title: "Extensions"},
		{ID: 13, WindowId: 1, URL: "about:blank", Title: "Blank"},
	}
	r.UpdateTabs(tabs)

	if _, ok := r.tabToWindow[12]; ok {
		t.Errorf("tab 12 (chrome://) should be filtered out")
	}
	if _, ok := r.tabToWindow[13]; ok {
		t.Errorf("tab 13 (about:) should be filtered out")
	}
	if r.tabToWindow[10] != 1 {
		t.Errorf("tabToWindow[10] = %d, want 1", r.tabToWindow[10])
	}
	if r.domainToWindow["google.com"] != 1 {
		t.Errorf("domainToWindow[google.com] = %d, want 1", r.domainToWindow["google.com"])
	}
}

func TestRouterGetWindowFallback(t *testing.T) {
	r := NewRouter(newTestLogger())
	r.RegisterWindow(1, &extensionWS{windowId: 1})

	got := r.GetWindowForTab(999)
	if got == nil || got.windowId != 1 {
		t.Errorf("GetWindowForTab(999) should return firstWindow")
	}

	got = r.GetWindowForDomain("unknown.com")
	if got == nil || got.windowId != 1 {
		t.Errorf("GetWindowForDomain(unknown.com) should return firstWindow")
	}

	r2 := NewRouter(newTestLogger())
	if r2.GetFirstWindow() != nil {
		t.Errorf("empty router GetFirstWindow should be nil")
	}
}

func TestRouterUnregisterWindow(t *testing.T) {
	r := NewRouter(newTestLogger())
	r.RegisterWindow(1, &extensionWS{windowId: 1})
	r.RegisterWindow(2, &extensionWS{windowId: 2})
	r.UpdateTabs([]Tab{
		{ID: 10, WindowId: 1, URL: "https://a.com"},
		{ID: 20, WindowId: 2, URL: "https://b.com"},
	})

	r.UnregisterWindow(1)
	if _, ok := r.tabToWindow[10]; ok {
		t.Errorf("tab 10 should be removed after window 1 unregister")
	}
	if r.tabToWindow[20] != 2 {
		t.Errorf("tabToWindow[20] = %d, want 2", r.tabToWindow[20])
	}
}

// ─── CdpServer Tests ──────────────────────────────────────────────────────────

func TestCdpServerStartStop(t *testing.T) {
	logger := newTestLogger()
	server := NewCdpServer(19092, "", logger)
	if err := server.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Stop()

	time.Sleep(100 * time.Millisecond)

	if len(server.router.GetWindows()) != 0 {
		t.Errorf("expected 0 windows, got %d", len(server.router.GetWindows()))
	}
}

func TestCdpServerPathRouting(t *testing.T) {
	logger := newTestLogger()
	server := NewCdpServer(19094, "", logger)
	if err := server.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer server.Stop()
	time.Sleep(100 * time.Millisecond)

	// Unknown path → 400
	req3 := httptest.NewRequest("GET", "http://localhost:19094/unknown", nil)
	w3 := httptest.NewRecorder()
	server.handleWS(w3, req3)
	if w3.Code != http.StatusBadRequest {
		t.Errorf("got %d for /unknown, want 400", w3.Code)
	}
}

func TestAuthCheck(t *testing.T) {
	logger := newTestLogger()
	server := NewCdpServer(19095, "secret123", logger)

	reqWithHeader := &http.Request{}
	reqWithHeader.Header = make(http.Header)
	reqWithHeader.Header.Set("Authorization", "secret123")

	if !server.checkAuth(reqWithHeader) {
		t.Errorf("checkAuth with correct header should return true")
	}

	reqWithWrong := &http.Request{}
	reqWithWrong.Header = make(http.Header)
	reqWithWrong.Header.Set("Authorization", "wrong")

	if server.checkAuth(reqWithWrong) {
		t.Errorf("checkAuth with wrong header should return false")
	}

	// No secret → always true
	server2 := NewCdpServer(19096, "", logger)
	if !server2.checkAuth(&http.Request{}) {
		t.Errorf("checkAuth with no secret should return true")
	}
}

func TestJsonRpcRequestParsing(t *testing.T) {
	tests := []struct {
		name       string
		json       string
		wantId     int64
		wantMethod string
	}{
		{"basic", `{"id":42,"method":"Target.getTargets","params":{}}`, 42, "Target.getTargets"},
		{"with_session", `{"id":1,"method":"Runtime.evaluate","params":{"expression":"1+1"},"sessionId":"cs-123"}`, 1, "Runtime.evaluate"},
		{"notification", `{"method":"Target.targetCreated","params":{"targetInfo":{}}}`, 0, "Target.targetCreated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req JsonRpcRequest
			if err := json.Unmarshal([]byte(tt.json), &req); err != nil {
				t.Fatalf("parse error: %v", err)
			}
			if req.ID != tt.wantId {
				t.Errorf("id = %d, want %d", req.ID, tt.wantId)
			}
			if req.Method != tt.wantMethod {
				t.Errorf("method = %q, want %q", req.Method, tt.wantMethod)
			}
		})
	}
}

func TestMsgRegisterParsing(t *testing.T) {
	jsonStr := `{"type":"register","role":"extension","windowId":123}`
	var msg MsgRegister
	if err := json.Unmarshal([]byte(jsonStr), &msg); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if msg.WindowId != 123 {
		t.Errorf("windowId = %d, want 123", msg.WindowId)
	}
	if msg.Role != "extension" {
		t.Errorf("role = %q, want extension", msg.Role)
	}
}

func TestMsgTabsListParsing(t *testing.T) {
	jsonStr := `{"type":"tabs_list","tabs":[{"id":10,"title":"Test","url":"https://example.com","active":true}]}`
	var msg MsgTabsList
	if err := json.Unmarshal([]byte(jsonStr), &msg); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(msg.Tabs) != 1 {
		t.Fatalf("len(tabs) = %d, want 1", len(msg.Tabs))
	}
	if msg.Tabs[0].ID != 10 {
		t.Errorf("tabs[0].id = %d, want 10", msg.Tabs[0].ID)
	}
}

func TestTabIdFromTargetId(t *testing.T) {
	tests := []struct {
		sessionId string
		want      int
	}{
		{"cs-123", 123},
		{"cs-1", 1},
		{"cs-99999", 99999},
		{"cs-123-extra", 123},
		{"tab-123", 0},
		{"", 0},
	}
	for _, tt := range tests {
		t.Run(tt.sessionId, func(t *testing.T) {
			got := TabIdFromTargetId(tt.sessionId)
			if got != tt.want {
				t.Errorf("TabIdFromTargetId(%q) = %d, want %d", tt.sessionId, got, tt.want)
			}
		})
	}
}

func TestTargetIdFromTabId(t *testing.T) {
	tests := []struct {
		tabId int
		want  string
	}{
		{123, "tab-123"},
		{1, "tab-1"},
		{99999, "tab-99999"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := TargetIdFromTabId(tt.tabId)
			if got != tt.want {
				t.Errorf("TargetIdFromTabId(%d) = %q, want %q", tt.tabId, got, tt.want)
			}
		})
	}
}

func TestBuildTargetList(t *testing.T) {
	logger := newTestLogger()
	server := NewCdpServer(19097, "", logger)
	server.cachedTabs = []Tab{
		{ID: 10, Title: "Google", URL: "https://google.com", Active: true},
		{ID: 20, Title: "GitHub", URL: "https://github.com", Active: false},
	}

	result := server.buildTargetList()
	infos, ok := result["targetInfos"].([]TargetInfo)
	if !ok {
		t.Fatalf("targetInfos not a []TargetInfo")
	}
	if len(infos) != 2 {
		t.Fatalf("len(targetInfos) = %d, want 2", len(infos))
	}

	if infos[0].TargetId != "tab-10" {
		t.Errorf("infos[0].targetId = %q, want tab-10", infos[0].TargetId)
	}
	if infos[0].Type != "page" {
		t.Errorf("infos[0].type = %q, want page", infos[0].Type)
	}
	if infos[1].Attached != false {
		t.Errorf("infos[1].attached = %v, want false", infos[1].Attached)
	}
}

func TestGetStatus(t *testing.T) {
	logger := newTestLogger()
	server := NewCdpServer(19098, "", logger)
	if err := server.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer server.Stop()
	time.Sleep(50 * time.Millisecond)

	status := server.GetStatus()
	if status["startTime"] == nil {
		t.Errorf("startTime should not be nil")
	}
	cdp, ok := status["cdp"].(map[string]interface{})
	if !ok {
		t.Fatalf("cdp status not map")
	}
	if cdp["port"].(int) != 19098 {
		t.Errorf("cdp.port = %v, want 19098", cdp["port"])
	}
}

func TestGetDomains(t *testing.T) {
	logger := newTestLogger()
	server := NewCdpServer(19099, "", logger)
	domains := server.getDomains()

	found := make(map[string]bool)
	for _, d := range domains {
		found[d["name"]] = true
	}

	want := []string{"Browser", "Target", "Page", "Runtime", "DOM", "Network", "Input", "Schema"}
	for _, w := range want {
		if !found[w] {
			t.Errorf("domain %q not found in getDomains()", w)
		}
	}
}

// ─── Pending Callbacks ────────────────────────────────────────────────────────

func TestPendingCallback(t *testing.T) {
	logger := newTestLogger()
	server := NewCdpServer(19100, "", logger)

	cb := newPendingCallback(nil, "test", func(result interface{}, errMsg string) {
		if errMsg != "" {
			t.Errorf("unexpected errMsg: %s", errMsg)
		}
	})

	server.mu.Lock()
	server.pending[123] = cb
	server.mu.Unlock()

	server.mu.Lock()
	pcb, ok := server.pending[123]
	server.mu.Unlock()

	if !ok {
		t.Errorf("pending[123] not found")
	}
	if pcb.method != "test" {
		t.Errorf("method = %q, want test", pcb.method)
	}

	// resolvePending
	server.resolvePending(123, "ok_result", "")

	server.mu.Lock()
	_, ok = server.pending[123]
	server.mu.Unlock()
	if ok {
		t.Errorf("pending[123] should be deleted after resolve")
	}
}
