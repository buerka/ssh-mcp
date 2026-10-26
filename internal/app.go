package internal

import (
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// App bundles the shared state behind all tool handlers.
type App struct {
	Pool      *Pool
	Store     *OutputStore
	Forwards  *Forwarder
	LocalUser string
}

func NewApp(insecure bool) *App {
	return &App{
		Pool:     NewPool(insecure),
		Store:    NewOutputStore(),
		Forwards: NewForwarder(),
	}
}

// Text wraps a plain-text payload as the only content of a tool result.
// Responses are plain text, never JSON, to keep them cheap in model context.
func Text(s string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}, nil, nil
}

func ToolError(err error) (*mcp.CallToolResult, any, error) {
	return nil, nil, err
}

const defaultExecTimeout = 60 * time.Second
