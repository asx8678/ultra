package app

// RuntimeProfile describes which frontend capabilities an App initializes.
// Named constructors keep callers from assembling unsupported combinations.
type RuntimeProfile struct {
	initializeAgent bool
	interactive     bool
	clipboard       bool
	eventBridge     bool
	mcp             bool
	trackLSP        bool
}

// InteractiveProfile initializes the full terminal UI runtime.
func InteractiveProfile() RuntimeProfile {
	return RuntimeProfile{
		initializeAgent: true,
		interactive:     true,
		clipboard:       true,
		eventBridge:     true,
		mcp:             true,
		trackLSP:        true,
	}
}

// CodeProfile initializes one non-interactive coordinator while retaining
// coding capabilities such as MCP and LSP tools.
func CodeProfile() RuntimeProfile {
	return RuntimeProfile{
		initializeAgent: true,
		mcp:             true,
		trackLSP:        true,
	}
}

// ServerProfile defers coordinator construction until the client selects an
// interactive or headless frontend. Domain events remain bridged for SSE.
func ServerProfile() RuntimeProfile {
	return RuntimeProfile{
		eventBridge: true,
		mcp:         true,
		trackLSP:    true,
	}
}
