package chrome

// Bridge provides high-level Chrome control via the extension.
// Currently a placeholder — actual implementation uses CdpServer directly.

type Bridge struct{}

func NewBridge() *Bridge {
	return &Bridge{}
}