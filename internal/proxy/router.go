package proxy

import (
	"log/slog"
	"strconv"
	"strings"
)

// Router maintains tab/window routing state.
type Router struct {
	logger *slog.Logger

	windowRegistry map[int]*extensionWS
	firstWindowId  int

	tabToWindow    map[int]int
	domainToWindow map[string]int
}

func NewRouter(logger *slog.Logger) *Router {
	return &Router{
		logger:         logger,
		windowRegistry: make(map[int]*extensionWS),
		tabToWindow:    make(map[int]int),
		domainToWindow: make(map[string]int),
	}
}

func (r *Router) RegisterWindow(windowId int, ws *extensionWS) {
	r.windowRegistry[windowId] = ws
	if r.firstWindowId == 0 {
		r.firstWindowId = windowId
		r.logger.Info("first window set", "windowId", windowId)
	}
	r.logger.Info("window registered", "windowId", windowId, "total", len(r.windowRegistry))
}

func (r *Router) UnregisterWindow(windowId int) {
	delete(r.windowRegistry, windowId)
	if r.firstWindowId == windowId {
		r.firstWindowId = 0
		for wid := range r.windowRegistry {
			if r.firstWindowId == 0 || wid < r.firstWindowId {
				r.firstWindowId = wid
			}
		}
	}
	for tabId, wid := range r.tabToWindow {
		if wid == windowId {
			delete(r.tabToWindow, tabId)
		}
	}
	r.rebuildDomainMap()
	r.logger.Info("window unregistered", "windowId", windowId)
}

func (r *Router) UpdateTabs(tabs []Tab) {
	r.tabToWindow = make(map[int]int)
	r.domainToWindow = make(map[string]int)

	for _, tab := range tabs {
		if !isHttp(tab.URL) {
			continue
		}
		r.tabToWindow[tab.ID] = tab.WindowId

		domain := extractDomain(tab.URL)
		if domain != "" {
			r.domainToWindow[domain] = tab.WindowId
		}
	}
}

func (r *Router) GetWindowForTab(tabId int) *extensionWS {
	windowId, ok := r.tabToWindow[tabId]
	if !ok {
		return r.GetFirstWindow()
	}
	return r.windowRegistry[windowId]
}

func (r *Router) GetWindowForDomain(domain string) *extensionWS {
	windowId, ok := r.domainToWindow[domain]
	if !ok {
		return r.GetFirstWindow()
	}
	return r.windowRegistry[windowId]
}

func (r *Router) GetWindows() map[int]*extensionWS {
	return r.windowRegistry
}

func (r *Router) GetFirstWindow() *extensionWS {
	if r.firstWindowId == 0 {
		return nil
	}
	return r.windowRegistry[r.firstWindowId]
}

func (r *Router) rebuildDomainMap() {
	r.domainToWindow = make(map[string]int)
}

// ParseSessionId parses target/session IDs from various formats:
//   - "cs-<tabId>" (chrome-use session format) → tabId
//   - "tab-<tabId>" (browserctl tab format) → tabId
//   - raw numeric string "123" → tabId
func ParseSessionId(id string) int {
	if id == "" {
		return 0
	}
	// Handle "cs-<tabId>" format
	if strings.HasPrefix(id, "cs-") {
		rest := id[3:]
		if idx := strings.IndexByte(rest, '-'); idx >= 0 {
			rest = rest[:idx]
		}
		if n, err := strconv.Atoi(rest); err == nil {
			return n
		}
	}
	// Handle "tab-<tabId>" format
	if strings.HasPrefix(id, "tab-") {
		rest := id[4:]
		if idx := strings.IndexByte(rest, '-'); idx >= 0 {
			rest = rest[:idx]
		}
		if n, err := strconv.Atoi(rest); err == nil {
			return n
		}
	}
	// Handle raw numeric string
	if n, err := strconv.Atoi(id); err == nil {
		return n
	}
	return 0
}

func extractDomain(url string) string {
	if len(url) < 8 {
		return ""
	}
	var start int
	if strings.HasPrefix(url, "https://") {
		start = 8
	} else if strings.HasPrefix(url, "http://") {
		start = 7
	} else {
		return ""
	}
	remaining := url[start:]
	for i, c := range remaining {
		if c == '/' || c == ':' || c == '?' || c == '#' {
			remaining = remaining[:i]
			break
		}
	}
	return remaining
}

func isHttp(url string) bool {
	return strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
}

func TabIdFromTargetId(targetId string) int {
	return ParseSessionId(targetId)
}

func TargetIdFromTabId(tabId int) string {
	return "tab-" + strconv.Itoa(tabId)
}
