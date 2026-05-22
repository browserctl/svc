package proxy

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"browserctl/svc/internal/chrome"
	"github.com/gorilla/websocket"
)

const (
	PathExtension = "/extension"
	PathDevTools   = "/devtools/"
	DefaultPort    = 9222
	WriteWait      = 10 * time.Second
	PongWait       = 60 * time.Second
	MaxMessageSize = 512 * 1024
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// extensionWS represents a connection from the Chrome extension
type extensionWS struct {
	ws     *websocket.Conn
	sendMu sync.Mutex
}

// pendingCallback handles async response waiting
type pendingCallback struct {
	id        int64
	client    *clientWS
	method    string
	onResult  func(result interface{}, errMsg string)
	handled   bool // true when result was sent via handleCdpResult
	handledMu sync.Mutex
}

// tryMarkHandled atomically marks this callback as already-handled (by handleCdpResult)
func (p *pendingCallback) tryMarkHandled() bool {
	p.handledMu.Lock()
	defer p.handledMu.Unlock()
	if p.handled {
		return false // already handled
	}
	p.handled = true
	return true
}

// newPendingCallback is only used by tests (excluded from lint)
//nolint:unused
func newPendingCallback(client *clientWS, method string, onResult func(result interface{}, errMsg string)) *pendingCallback {
	return &pendingCallback{client: client, method: method, onResult: onResult}
}

// CdpServer is the transparent CDP proxy.
type CdpServer struct {
	port       int
	secret     string
	logger     *slog.Logger
	router     *Router
	profileDir string

	httpServer    *http.Server
	extensionConn *extensionWS
	startTime     time.Time

	pending map[int64]*pendingCallback
	_nextSeq int64
	mu      sync.Mutex

	clientTabs map[*clientWS]tabSet
	cachedTabs []Tab
	prevTabIds map[int]string
}

type clientWS struct {
	ws *websocket.Conn
}

type tabSet map[int]bool

func NewCdpServer(port int, secret string, logger *slog.Logger) *CdpServer {
	return &CdpServer{
		port:       port,
		secret:     secret,
		logger:     logger,
		router:     NewRouter(logger),
		pending:    make(map[int64]*pendingCallback),
		clientTabs: make(map[*clientWS]tabSet),
		prevTabIds: make(map[int]string),
		startTime:  time.Now(),
	}
}

func (s *CdpServer) SetProfileDir(profileDir string) {
	s.profileDir = profileDir
}

// Start the WebSocket + HTTP server
func (s *CdpServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleWS)

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	// Update Addr with the actual bound port (especially important when port=0)
	s.httpServer = &http.Server{
		Addr:    ln.Addr().String(),
		Handler: mux,
	}

	go func() {
		s.logger.Info("CDP server listening", "port", s.port)
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error("CDP server error", "err", err)
			os.Exit(1)
		}
	}()

	return nil
}

// Port returns the actual TCP port the server is listening on.
// Useful when Start() is called with port=0 (random port assignment).
func (s *CdpServer) Port() int {
	if s.httpServer == nil {
		return s.port
	}
	addr := s.httpServer.Addr
	if addr == "" {
		return s.port
	}
	// addr is ":8080" or "127.0.0.1:8080"
	if idx := strings.LastIndexByte(addr, ':'); idx >= 0 {
		if p, err := strconv.Atoi(addr[idx+1:]); err == nil {
			return p
		}
	}
	return s.port
}

func (s *CdpServer) Stop() error {
	return s.httpServer.Close()
}

func (s *CdpServer) handleWS(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == PathExtension {
		s.handleExtension(w, r)
	} else if strings.HasPrefix(path, PathDevTools) {
		s.handleCdpClient(w, r)
	} else {
		http.Error(w, "Use /extension or /devtools/... paths", http.StatusBadRequest)
	}
}

// ─── Extension ────────────────────────────────────────────────────────────

func (s *CdpServer) handleExtension(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("extension upgrade failed", "err", err)
		return
	}

	extWS := &extensionWS{ws: conn}
	s.extensionConn = extWS
	s.logger.Info("extension connected")

	// Request registration
	s.sendToExtension(extWS, map[string]interface{}{"type": "register"})

	s.readLoopExt(extWS)
}

func (s *CdpServer) readLoopExt(extWS *extensionWS) {
	defer func() {
		s.onExtensionClose(extWS)
		_ = extWS.ws.Close()
	}()

	extWS.ws.SetReadLimit(MaxMessageSize)
	_ = extWS.ws.SetReadDeadline(time.Now().Add(PongWait))
	extWS.ws.SetPongHandler(func(string) error {
		_ = extWS.ws.SetReadDeadline(time.Now().Add(PongWait))
		return nil
	})

	for {
		_, data, err := extWS.ws.ReadMessage()
		if err != nil {
			if !websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				s.logger.Debug("extension read error", "err", err)
			}
			break
		}
		s.handleExtMessage(extWS, data)
	}
}

func (s *CdpServer) onExtensionClose(extWS *extensionWS) {
	s.logger.Info("extension disconnected")
	if s.extensionConn == extWS {
		s.extensionConn = nil
	}
	for windowId, ext := range s.router.GetWindows() {
		if ext == extWS {
			s.router.UnregisterWindow(windowId)
		}
	}
}

func (s *CdpServer) handleExtMessage(extWS *extensionWS, data []byte) {
	fmt.Fprintf(os.Stderr, "[DEBUG] handleExtMessage: %s\n", string(data))
	var base struct {
		Type string `json:"type"`
		ID   int64  `json:"id"`
	}
	if json.Unmarshal(data, &base) != nil {
		return
	}

	switch base.Type {
	case "register":
		var msg MsgRegister
		if json.Unmarshal(data, &msg) == nil {
			s.logger.Info("extension registered", "windowId", msg.WindowId, "role", msg.Role)
			s.router.RegisterWindow(msg.WindowId, extWS)
		}

	case "tabs_list":
		var msg MsgTabsList
		if json.Unmarshal(data, &msg) == nil {
			s.onTabsList(msg.Tabs)
		}

	case "tab_attach_result":
		var msg MsgTabAttachResult
		if json.Unmarshal(data, &msg) == nil && msg.ID != 0 {
			s.resolvePending(msg.ID, msg.Success, msg.Error)
		}

	case "new_tab_result":
		var msg MsgNewTabResult
		if json.Unmarshal(data, &msg) == nil && msg.ID != 0 {
			s.resolvePending(msg.ID, msg.Tab, msg.Error)
		}

	case "cdp_result", "navigate_result", "evaluate_result",
		"switch_tab_result", "close_tab_result", "cdp_subscribe_result":
		s.handleCdpResult(data)

	case "cdp_event":
		var msg MsgCdpEvent
		if json.Unmarshal(data, &msg) == nil {
			s.routeEventToClients(msg.TabId, msg.Method, msg.Params)
		}

	case "pong":
		// keepalive ack, ignore
	}
}

func (s *CdpServer) onTabsList(tabs []Tab) {
	s.router.UpdateTabs(tabs)
	s.cachedTabs = tabs
}

func (s *CdpServer) handleCdpResult(data []byte) {
	var base struct {
		ID     int64           `json:"id"`
		Result interface{}     `json:"result,omitempty"`
		Error  interface{}     `json:"error,omitempty"`
	}
	if json.Unmarshal(data, &base) != nil || base.ID == 0 {
		return
	}

	var errMsg string
	if base.Error != nil {
		errMsg = fmt.Sprintf("%v", base.Error)
	}
	s.resolvePending(base.ID, base.Result, errMsg)
}

func (s *CdpServer) resolvePending(id int64, result interface{}, errMsg string) {
	s.mu.Lock()
	pcb, ok := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()

	if ok && pcb != nil && pcb.onResult != nil {
		if pcb.tryMarkHandled() {
			pcb.onResult(result, errMsg)
		}
	}
}

// ─── CDP Client ─────────────────────────────────────────────────────────────

func (s *CdpServer) handleCdpClient(w http.ResponseWriter, r *http.Request) {
	if s.secret != "" && !s.checkAuth(r) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4401, "Unauthorized"))
			_ = conn.Close()
		}
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("cdp client upgrade failed", "err", err)
		return
	}

	client := &clientWS{ws: conn}
	s.clientTabs[client] = make(tabSet)
	s.logger.Info("cdp client connected", "path", r.URL.Path)
	s.readClientLoop(client)
}

func (s *CdpServer) readClientLoop(client *clientWS) {
	defer func() {
		delete(s.clientTabs, client)
		_ = client.ws.Close()
	}()

	client.ws.SetReadLimit(MaxMessageSize)
	_ = client.ws.SetReadDeadline(time.Now().Add(PongWait))
	client.ws.SetPongHandler(func(string) error {
		_ = client.ws.SetReadDeadline(time.Now().Add(PongWait))
		return nil
	})

	for {
		_, data, err := client.ws.ReadMessage()
		if err != nil {
			if !websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				s.logger.Debug("client read error", "err", err)
			}
			break
		}
		s.handleCdpClientMsg(client, data)
	}
}

func (s *CdpServer) handleCdpClientMsg(client *clientWS, data []byte) {
	var req JsonRpcRequest
	if err := json.Unmarshal(data, &req); err != nil {
		s.writeJson(client.ws, JsonRpcResponse{ID: 0, Error: &RpcError{Code: -32700, Message: "Parse error"}})
		return
	}
	s.dispatchCdpCommand(client, &req)
}

func (s *CdpServer) dispatchCdpCommand(client *clientWS, req *JsonRpcRequest) {
	method := req.Method
	params := req.Params

	// Browser domain (no session)
	switch method {
	case "Browser.getVersion":
		s.writeJson(client.ws, JsonRpcResponse{
			ID: req.ID,
			Result: map[string]string{
				"protocolVersion": "1.3",
				"product":         "Chrome/999.0.0",
				"revision":        "@browserctl",
				"userAgent":       "",
				"jsVersion":       "",
			},
		})
		return

	case "Browser.getBrowserCommandLine":
		s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Result: map[string]interface{}{"args": []interface{}{}}})
		return

	case "Browser.close":
		s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Result: map[string]interface{}{}}) // no-op
		return

	case "Browser.crash":
		s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Result: map[string]interface{}{}})
		return

	case "Browser.launch":
		extPath := ""
		if params != nil {
			if v, ok := params["extPath"].(string); ok {
				extPath = v
			}
		}
		launcher := chrome.NewLauncher(s.logger, s.profileDir, extPath)
		if err := launcher.Launch(); err != nil {
			s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Error: &RpcError{Code: -32000, Message: err.Error()}})
			return
		}
		s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Result: map[string]interface{}{"success": true}})
		return

	case "Schema.getDomains":
		s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Result: map[string]interface{}{"domains": s.getDomains()}})
		return
	}

	// Target domain
	sessionId := req.SessionId
	tabId := ParseSessionId(sessionId)

	switch method {
	case "Target.getTargets":
		s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Result: s.buildTargetList()})
		return

	case "Target.setDiscoverTargets":
		for _, tab := range s.cachedTabs {
			targetId := "tab-" + strconv.Itoa(tab.ID)
			s.prevTabIds[tab.ID] = targetId
			s.writeJson(client.ws, JsonRpcNotification{
				Method: "Target.targetCreated",
				Params: map[string]interface{}{
					"targetInfo": TargetInfo{
						TargetId:       targetId,
						Type:           "page",
						Title:          tab.Title,
						URL:            tab.URL,
						Attached:       tab.Active,
						BrowserContextId: "browserctl-default",
					},
				},
			})
		}
		s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Result: map[string]interface{}{}})
		return

	case "Target.attachToTarget":
		targetId := ""
		if params != nil {
			if v, ok := params["targetId"].(string); ok {
				targetId = v
			}
		}
		tabId = ParseSessionId(targetId)
		if tabId == 0 {
			s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Error: &RpcError{Code: -32602, Message: "invalid targetId"}})
			return
		}

		result, err := s.awaitTabAttach(tabId)
		if err != nil {
			s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Error: &RpcError{Code: -32000, Message: err.Error()}})
			return
		}
		if !result {
			s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Error: &RpcError{Code: -32000, Message: "attach failed"}})
			return
		}

		s.clientTabs[client][tabId] = true
		s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Result: map[string]string{"sessionId": "cs-" + strconv.Itoa(tabId)}})
		return

	case "Target.createTarget":
		url := "about:blank"
		if params != nil {
			if v, ok := params["url"].(string); ok {
				url = v
			}
		}

		domain := extractDomain(url)
		extWS := s.router.GetWindowForDomain(domain)
		if extWS == nil {
			extWS = s.router.GetFirstWindow()
		}
		if extWS == nil {
			s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Error: &RpcError{Code: -32000, Message: "no extension connected"}})
			return
		}

		// Try domain reuse
		if domain != "" {
			for _, tab := range s.cachedTabs {
				if extractDomain(tab.URL) == domain && !tab.Active {
					s.sendToExtension(extWS, map[string]interface{}{"type": "switch_tab", "tabId": tab.ID, "id": s._nextId()})
					s.sendToExtension(extWS, map[string]interface{}{"type": "cdp_command", "tabId": tab.ID, "method": "Page.navigate", "params": map[string]interface{}{"url": url}, "id": s._nextId()})
					s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Result: map[string]string{"sessionId": "cs-" + strconv.Itoa(tab.ID)}})
					return
				}
			}
		}

		// New tab
		tabId, err := s.awaitNewTab(url)
		if err != nil || tabId == 0 {
			s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Error: &RpcError{Code: -32000, Message: "new_tab failed"}})
			return
		}

		s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Result: map[string]string{"targetId": "tab-" + strconv.Itoa(tabId)}})
		return

	case "Target.closeTarget":
		targetId := ""
		if params != nil {
			if v, ok := params["targetId"].(string); ok {
				targetId = v
			}
		}
		tabId = ParseSessionId(targetId)
		extWS := s.router.GetWindowForTab(tabId)
		if extWS == nil {
			s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Result: map[string]bool{"success": false}})
			return
		}
		s.sendToExtension(extWS, map[string]interface{}{"type": "close_tab", "tabId": tabId, "id": s._nextId()})
		s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Result: map[string]bool{"success": true}})
		return

	case "Target.activateTarget":
		targetId := ""
		if params != nil {
			if v, ok := params["targetId"].(string); ok {
				targetId = v
			}
		}
		tabId = ParseSessionId(targetId)
		extWS := s.router.GetWindowForTab(tabId)
		if extWS == nil {
			s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Error: &RpcError{Code: -32602, Message: "tab not found"}})
			return
		}
		s.sendToExtension(extWS, map[string]interface{}{"type": "switch_tab", "tabId": tabId, "id": s._nextId()})
		s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Result: map[string]interface{}{}})
		return

	case "Target.detachFromTarget":
		tabId := ParseSessionId(req.SessionId)
		if tabs, ok := s.clientTabs[client]; ok {
			delete(tabs, tabId)
		}
		s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Result: map[string]interface{}{}})
		return
	}

	// Generic CDP command — requires session
	if tabId == 0 {
		s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Error: &RpcError{Code: -32602, Message: "session required"}})
		return
	}

	extWS := s.router.GetWindowForTab(tabId)
	if extWS == nil {
		s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Error: &RpcError{Code: -32000, Message: "tab not found"}})
		return
	}

	// Strip domain prefix: "Runtime.evaluate" → "evaluate"
	methodName := method
	if idx := strings.LastIndex(method, "."); idx >= 0 {
		methodName = method[idx+1:]
	}

	reqId := s._nextId()
	resultCh := make(chan json.RawMessage, 1)
	errCh := make(chan error, 1)

	s.mu.Lock()
	// Create callback first, then set onResult separately to avoid reference cycle
	pcb := &pendingCallback{id: reqId, client: client, method: method}
	pcb.onResult = func(result interface{}, errMsg string) {
		if !pcb.tryMarkHandled() {
			return // already handled by handleCdpResult
		}
		if errMsg != "" {
			select { case errCh <- fmt.Errorf("%s", errMsg): default: }
			return
		}
		if raw, ok := result.(json.RawMessage); ok {
			select { case resultCh <- raw: default: }
		} else {
			data, merr := json.Marshal(result)
			if merr != nil {
				select { case errCh <- fmt.Errorf("marshal result: %w", merr): default: }
				return
			}
			select { case resultCh <- data: default: }
		}
	}
	s.pending[reqId] = pcb
	s.mu.Unlock()

	s.sendToExtension(extWS, map[string]interface{}{
		"type":   "cdp_command",
		"tabId":  tabId,
		"method": methodName,
		"params": params,
		"id":     reqId,
	})

	select {
	case <-time.After(30 * time.Second):
		s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Error: &RpcError{Code: -32000, Message: "timeout"}})
	case result := <-resultCh:
		s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Result: result})
	case err := <-errCh:
		s.writeJson(client.ws, JsonRpcResponse{ID: req.ID, Error: &RpcError{Code: -32000, Message: err.Error()}})
	}
}

// ─── Await helpers ─────────────────────────────────────────────────────────

func (s *CdpServer) _nextId() int64 {
	s.mu.Lock()
	s._nextSeq++
	ret := s._nextSeq
	s.mu.Unlock()
	return ret
}

func (s *CdpServer) awaitTabAttach(tabId int) (bool, error) {
	extWS := s.router.GetWindowForTab(tabId)
	if extWS == nil {
		extWS = s.router.GetFirstWindow()
	}
	if extWS == nil {
		return false, fmt.Errorf("no extension connected")
	}

	reqId := s._nextId()
	resultCh := make(chan bool, 1)
	errCh := make(chan error, 1)

	s.mu.Lock()
	s.pending[reqId] = &pendingCallback{
		onResult: func(result interface{}, errMsg string) {
			if errMsg != "" {
				select {
				case errCh <- fmt.Errorf("%s", errMsg):
				default:
				}
				return
			}
			if b, ok := result.(bool); ok {
				select {
				case resultCh <- b:
				default:
				}
			} else {
				select {
				case errCh <- fmt.Errorf("unexpected result type"):
				default:
				}
			}
		},
	}
	s.mu.Unlock()

	s.sendToExtension(extWS, map[string]interface{}{"type": "tab_attach", "tabId": tabId, "id": reqId})

	select {
	case <-time.After(30 * time.Second):
		return false, fmt.Errorf("timeout")
	case ok := <-resultCh:
		return ok, nil
	case err := <-errCh:
		return false, err
	}
}

func (s *CdpServer) awaitNewTab(url string) (int, error) {
	extWS := s.router.GetFirstWindow()
	if extWS == nil {
		return 0, fmt.Errorf("no extension connected")
	}

	reqId := s._nextId()
	resultCh := make(chan int, 1)
	errCh := make(chan error, 1)

	s.mu.Lock()
	s.pending[reqId] = &pendingCallback{
		onResult: func(result interface{}, errMsg string) {
			if errMsg != "" {
				select { case errCh <- fmt.Errorf("%s", errMsg): default: }
				return
			}
			// Extension sends tab object with id (float64)
			if m, ok := result.(map[string]interface{}); ok {
				if id, ok := m["id"].(float64); ok {
					select { case resultCh <- int(id): default: }
					return
				}
			}
			// Fallback: result might be tabId as float64
			if n, ok := result.(float64); ok {
				select { case resultCh <- int(n): default: }
				return
			}
			select { case errCh <- fmt.Errorf("invalid result"): default: }
		},
	}
	s.mu.Unlock()

	s.sendToExtension(extWS, map[string]interface{}{"type": "new_tab", "url": url, "id": reqId})

	select {
	case <-time.After(30 * time.Second):
		return 0, fmt.Errorf("timeout")
	case tabId := <-resultCh:
		return tabId, nil
	case err := <-errCh:
		return 0, err
	}
}

// ─── Event Routing ─────────────────────────────────────────────────────────

func (s *CdpServer) routeEventToClients(tabId int, method string, params map[string]interface{}) {
	for client, tabs := range s.clientTabs {
		if tabs[tabId] && client.ws != nil {
			s.writeJson(client.ws, JsonRpcNotification{Method: method, Params: params})
		}
	}
}

// ─── Send helpers ─────────────────────────────────────────────────────────

func (s *CdpServer) sendToExtension(extWS *extensionWS, msg interface{}) {
	if extWS == nil || extWS.ws == nil {
		return
	}
	extWS.sendMu.Lock()
	defer extWS.sendMu.Unlock()
	_ = extWS.ws.SetWriteDeadline(time.Now().Add(WriteWait))
	_ = extWS.ws.WriteJSON(msg)
}

func (s *CdpServer) writeJson(conn *websocket.Conn, v interface{}) {
	if conn == nil {
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(WriteWait))
	_ = conn.WriteJSON(v)
}

func (s *CdpServer) checkAuth(r *http.Request) bool {
	if s.secret == "" {
		return true
	}
	if r.Header.Get("Authorization") == s.secret {
		return true
	}
	if r.URL != nil && r.URL.Query().Get("secret") == s.secret {
		return true
	}
	return false
}

func (s *CdpServer) buildTargetList() map[string]interface{} {
	targets := make([]TargetInfo, 0, len(s.cachedTabs))
	for _, tab := range s.cachedTabs {
		targets = append(targets, TargetInfo{
			TargetId:       "tab-" + strconv.Itoa(tab.ID),
			Type:           "page",
			Title:          tab.Title,
			URL:            tab.URL,
			Attached:       tab.Active,
			BrowserContextId: "browserctl-default",
		})
	}
	return map[string]interface{}{"targetInfos": targets}
}

func (s *CdpServer) getDomains() []map[string]string {
	return []map[string]string{
		{"name": "Browser", "version": "1.3"},
		{"name": "Target", "version": "1.3"},
		{"name": "Page", "version": "1.3"},
		{"name": "Runtime", "version": "1.3"},
		{"name": "DOM", "version": "1.3"},
		{"name": "DOMDebugger", "version": "1.3"},
		{"name": "Network", "version": "1.3"},
		{"name": "Input", "version": "1.3"},
		{"name": "Log", "version": "1.3"},
		{"name": "Fetch", "version": "1.3"},
		{"name": "Emulation", "version": "1.3"},
		{"name": "Security", "version": "1.3"},
		{"name": "Accessibility", "version": "1.3"},
		{"name": "Performance", "version": "1.3"},
		{"name": "LayerTree", "version": "1.3"},
		{"name": "Storage", "version": "1.3"},
		{"name": "Schema", "version": "1.3"},
	}
}

// PingExtension sends keepalive ping
func (s *CdpServer) PingExtension() {
	if s.extensionConn != nil {
		s.sendToExtension(s.extensionConn, map[string]interface{}{"type": "ping"})
	}
}

// GetStatus returns server status for HTTP endpoint
func (s *CdpServer) GetStatus() map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]interface{}{
		"startTime": s.startTime,
		"cdp": map[string]interface{}{
			"port":    s.port,
			"clients": len(s.clientTabs),
			"tabs":    len(s.cachedTabs),
			"pending": len(s.pending),
		},
	}
}

// WindowCount returns number of registered extension windows
func (s *CdpServer) WindowCount() int {
	return len(s.router.GetWindows())
}