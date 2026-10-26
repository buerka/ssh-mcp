package internal

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ExecIn struct {
	Host       string `json:"host" jsonschema:"host alias from ~/.ssh/config, or a raw hostname"`
	Command    string `json:"command" jsonschema:"shell command to run on the remote host"`
	TimeoutSec int    `json:"timeout_sec,omitempty" jsonschema:"timeout in seconds (default 60)"`
	MaxBytes   int    `json:"max_bytes,omitempty" jsonschema:"max output bytes returned (default 8192)"`
}

// Exec runs a command on the remote host. Output is redacted and truncated;
// the full output is cached under an id retrievable via ssh_output.
func (a *App) Exec(ctx context.Context, req *mcp.CallToolRequest, in ExecIn) (*mcp.CallToolResult, any, error) {
	client, err := a.Pool.Client(in.Host)
	if err != nil {
		return ToolError(fmt.Errorf("connect to %s: %w", in.Host, err))
	}
	session, err := client.NewSession()
	if err != nil {
		return ToolError(fmt.Errorf("open session on %s: %w", in.Host, err))
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	timeout := defaultExecTimeout
	if in.TimeoutSec > 0 {
		timeout = time.Duration(in.TimeoutSec) * time.Second
	}
	done := make(chan error, 1)
	go func() { done <- session.Run(in.Command) }()

	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(timeout):
		session.Close()
		select {
		case runErr = <-done:
		case <-time.After(time.Second):
			runErr = fmt.Errorf("timed out after %s (session force-closed)", timeout)
		}
	}

	exit := 0
	if runErr != nil {
		if ee, ok := runErr.(*ssh.ExitError); ok {
			exit = ee.ExitStatus()
			runErr = nil // non-zero exit is reported via the exit line, not as a tool error
		}
	}

	full := stdout.Bytes()
	if stderr.Len() > 0 {
		full = append(append(append(full, '\n'), []byte("[stderr]\n")...), stderr.Bytes()...)
	}
	id := a.Store.Save(full)

	var head string
	switch {
	case runErr != nil:
		head = fmt.Sprintf("error: %v", runErr)
	default:
		head = fmt.Sprintf("exit:%d", exit)
	}
	body := Truncate(full, in.MaxBytes, id)
	if body == "" {
		body = "(no output)"
	}
	return Text(head + "\n" + body)
}

type OutputIn struct {
	ID     string `json:"id" jsonschema:"output id from a previous truncated ssh_exec result"`
	Offset int    `json:"offset,omitempty" jsonschema:"byte offset to start from"`
	Length int    `json:"length,omitempty" jsonschema:"number of bytes to return (0 = to the end)"`
}

// Output pages through a cached exec result that was truncated earlier.
func (a *App) Output(ctx context.Context, req *mcp.CallToolRequest, in OutputIn) (*mcp.CallToolResult, any, error) {
	page, ok := a.Store.Page(in.ID, in.Offset, in.Length)
	if !ok {
		return ToolError(fmt.Errorf("no cached output with id %s (ids are valid for this server session only)", in.ID))
	}
	if page == "" {
		page = "(empty)"
	}
	return Text(page)
}
