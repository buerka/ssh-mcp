package main

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/buerka/ssh-mcp/internal"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// smokeServer wires a full server + client session over in-memory transports.
func smokeServer(t *testing.T, insecure bool) *mcp.ClientSession {
	t.Helper()
	server := newServer(internal.NewApp(insecure))
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	go server.Run(ctx, st)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func toolText(t *testing.T, res *mcp.CallToolResult, err error) string {
	t.Helper()
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("expected 1 content, got %d", len(res.Content))
	}
	return res.Content[0].(*mcp.TextContent).Text
}

func TestListTools(t *testing.T) {
	cs := smokeServer(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	want := []string{"ssh_exec", "ssh_forward", "ssh_get", "ssh_hosts", "ssh_ls", "ssh_output", "ssh_put"}
	if len(names) != len(want) {
		t.Fatalf("want %v, got %v", want, names)
	}
	got := append([]string(nil), names...)
	sort.Strings(got)
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("want %v, got %v", want, got)
		}
	}
}

func TestSSHHosts(t *testing.T) {
	cs := smokeServer(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "ssh_hosts",
		Arguments: map[string]any{},
	})
	out := toolText(t, res, err)
	// The user's real ~/.ssh/config is read; just require plain-text output.
	if strings.ContainsAny(out, "{}") {
		t.Fatalf("output is not plain text: %q", out)
	}
}

func TestExecUnknownHostFailsCleanly(t *testing.T) {
	cs := smokeServer(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "ssh_exec",
		Arguments: map[string]any{
			"host":    "ssh-mcp-test-no-such-host.invalid",
			"command": "echo hi",
		},
	})
	if err == nil && (res == nil || !res.IsError) {
		t.Fatal("expected tool error for unresolvable host")
	}
}

func TestOutputUnknownIDFails(t *testing.T) {
	cs := smokeServer(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "ssh_output",
		Arguments: map[string]any{"id": "out-999"},
	})
	if err == nil && (res == nil || !res.IsError) {
		t.Fatal("expected error for unknown output id")
	}
}
