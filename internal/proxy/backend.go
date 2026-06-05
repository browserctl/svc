package proxy

// BackendProvider is the interface for CDP command execution.
// Both ExtensionBackend and DirectCDPBackend implement this interface,
// allowing CdpServer to be backend-agnostic.
type BackendProvider interface {
	// Tabs returns the current list of tabs known to the backend.
	Tabs() []Tab

	// SendCommand sends a CDP command to the given tab and returns the result.
	// It is synchronous — blocks until the response arrives or timeout.
	// method is the CDP method name (e.g. "Runtime.evaluate", "Page.navigate").
	// params is the CDP command parameters (may be nil).
	// Returns the CDP result or an error.
	SendCommand(tabId int, method string, params map[string]interface{}) (interface{}, error)

	// Close cleans up backend resources.
	Close() error
}