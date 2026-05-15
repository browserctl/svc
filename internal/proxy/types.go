package proxy

// ─── Extension ↔ Svc 消息类型 ─────────────────────────────────────────────────

// ExtToSvc messages
type MsgRegister struct {
	Type     string `json:"type"` // "register"
	Role     string `json:"role"` // "extension"
	API      string `json:"api,omitempty"`
	WindowId int    `json:"windowId"`
}

type MsgTabsList struct {
	Type string `json:"type"` // "tabs_list"
	ID   int64  `json:"id,omitempty"`
	Tabs []Tab  `json:"tabs"`
}

type Tab struct {
	ID       int    `json:"id"`
	WindowId int    `json:"windowId,omitempty"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Active   bool   `json:"active"`
}

// SvcToExt messages
type MsgGetTabs struct {
	Type string `json:"type"` // "get_tabs"
	ID   int64  `json:"id"`
}

type MsgCdpCommand struct {
	Type   string                 `json:"type"` // "cdp_command"
	TabId  int                    `json:"tabId"`
	Method string                 `json:"method"`
	Params map[string]interface{} `json:"params"`
	ID     int64                  `json:"id"`
}

type MsgTabAttach struct {
	Type  string `json:"type"` // "tab_attach"
	TabId int    `json:"tabId"`
	ID    int64  `json:"id"`
}

type MsgTabDetach struct {
	Type  string `json:"type"` // "tab_detach"
	TabId int    `json:"tabId"`
	ID    int64  `json:"id"`
}

type MsgSwitchTab struct {
	Type  string `json:"type"` // "switch_tab"
	TabId int    `json:"tabId"`
	ID    int64  `json:"id"`
}

type MsgCloseTab struct {
	Type  string `json:"type"` // "close_tab"
	TabId int    `json:"tabId"`
	ID    int64  `json:"id"`
}

type MsgNewTab struct {
	Type string `json:"type"` // "new_tab"
	URL  string `json:"url"`
	ID   int64  `json:"id"`
}

type MsgCdpResult struct {
	Type   string      `json:"type,omitempty"`
	ID     int64       `json:"id"`
	Result interface{} `json:"result,omitempty"`
	Error  interface{} `json:"error,omitempty"`
}

type MsgCdpEvent struct {
	Type   string                 `json:"type"` // "cdp_event"
	TabId  int                    `json:"tabId"`
	Method string                 `json:"method"`
	Params map[string]interface{} `json:"params"`
}

type MsgTabAttachResult struct {
	Type    string `json:"type"` // "tab_attach_result"
	TabId   int    `json:"tabId"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	ID      int64  `json:"id,omitempty"`
}

type MsgNewTabResult struct {
	Type  string `json:"type"` // "new_tab_result"
	Tab   *Tab   `json:"tab,omitempty"`
	Error string `json:"error,omitempty"`
	ID    int64  `json:"id,omitempty"`
}

// ─── CDP JSON-RPC ─────────────────────────────────────────────────────────────

type JsonRpcRequest struct {
	ID        int64                  `json:"id"`
	Method    string                 `json:"method"`
	Params    map[string]interface{} `json:"params,omitempty"`
	SessionId string                 `json:"sessionId,omitempty"`
}

type JsonRpcResponse struct {
	ID     int64       `json:"id"`
	Result interface{} `json:"result,omitempty"`
	Error  *RpcError   `json:"error,omitempty"`
}

type JsonRpcNotification struct {
	Method string                 `json:"method"`
	Params map[string]interface{} `json:"params,omitempty"`
}

type RpcError struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
}

// ─── Pending request ──────────────────────────────────────────────────────────

type PendingRequest struct {
	Ws     interface{} // *extensionWS or *ClientWS
	Method string
}

// ─── Target info ─────────────────────────────────────────────────────────────

type TargetInfo struct {
	TargetId         string `json:"targetId"`
	Type             string `json:"type"`
	Title            string `json:"title"`
	URL              string `json:"url"`
	Attached         bool   `json:"attached"`
	BrowserContextId string `json:"browserContextId"`
}
