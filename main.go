// Command ssh-mcp is a lightweight stdio MCP server that exposes SSH command
// execution, SFTP file transfer and port forwarding for hosts configured in
// ~/.ssh/config. Tool responses are plain text and truncated to keep model
// context small; full output is paged back on demand.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"

	"github.com/buerka/ssh-mcp/internal"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	insecure := flag.Bool("insecure", false, "skip known_hosts host key verification (insecure)")
	verbose := flag.Bool("verbose", false, "log tool calls to stderr")
	flag.Parse()

	if v := os.Getenv("SSH_MCP_MAX_OUTPUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			internal.MaxOutput = n
		}
	}
	if !*verbose {
		log.SetOutput(io.Discard) // MCP uses stdout; keep stderr quiet unless asked
	} else {
		log.SetFlags(0)
	}

	app := internal.NewApp(*insecure)
	server := newServer(app)

	ctx := context.Background()
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "ssh-mcp:", err)
		os.Exit(1)
	}
}

// newServer registers every tool on a fresh MCP server. Split out of main so
// the smoke test can drive the server over an in-memory transport.
func newServer(app *internal.App) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "ssh-mcp", Version: "0.1.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ssh_hosts",
		Description: "list host aliases available in ~/.ssh/config",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		hosts, err := internal.ListHosts()
		if err != nil {
			return internal.ToolError(err)
		}
		out := ""
		for _, h := range hosts {
			out += h + "\n"
		}
		if out == "" {
			out = "(no hosts defined)"
		}
		return internal.Text(out)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ssh_exec",
		Description: "run a shell command on an SSH host; output is truncated, full output fetchable via ssh_output",
	}, app.Exec)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ssh_output",
		Description: "page through a previously truncated ssh_exec output by id",
	}, app.Output)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ssh_ls",
		Description: "list a remote directory over SFTP",
	}, app.LS)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ssh_get",
		Description: "download a remote file over SFTP",
	}, app.Get)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ssh_put",
		Description: "upload a local file over SFTP",
	}, app.Put)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ssh_forward",
		Description: "start or stop a local port forward through an SSH host",
	}, app.Forward)

	return server
}
