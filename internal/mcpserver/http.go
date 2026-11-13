package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rolloutcat/xshell-bridge-public/internal/client"
	"net/http"
)

// Stateless HTTP multiplexes conversations in Bridge without retaining idle
// transports. Tools still call the ordinary client API and its authorization.
func HTTPHandler(c *client.Client) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return New(c) },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
}
