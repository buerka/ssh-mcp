package internal

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Forwarder struct {
	mu sync.Mutex
	m  map[string]*forward
	n  int
}

type forward struct {
	id      string
	listen  net.Listener
	client  io.Closer
	remotes string
}

func NewForwarder() *Forwarder { return &Forwarder{m: make(map[string]*forward)} }

type ForwardIn struct {
	Action     string `json:"action" jsonschema:"\"start\" or \"stop\""`
	Host       string `json:"host,omitempty" jsonschema:"host alias to tunnel through (required to start)"`
	RemoteHost string `json:"remote_host,omitempty" jsonschema:"target host as seen from the SSH server (default 127.0.0.1)"`
	RemotePort int    `json:"remote_port,omitempty" jsonschema:"target port on remote_host (required to start)"`
	ID         string `json:"id,omitempty" jsonschema:"forward id returned by start (required to stop)"`
}

// Forward manages local port forwards of the form
// 127.0.0.1:<auto> -> <remote_host>:<remote_port> through the SSH server.
func (a *App) Forward(ctx context.Context, req *mcp.CallToolRequest, in ForwardIn) (*mcp.CallToolResult, any, error) {
	switch in.Action {
	case "start":
		if in.Host == "" || in.RemotePort <= 0 {
			return ToolError(fmt.Errorf("start requires host and remote_port"))
		}
		id, addr, err := a.Forwards.Start(a.Pool, in.Host, in.RemoteHost, in.RemotePort)
		if err != nil {
			return ToolError(err)
		}
		return Text(fmt.Sprintf("listening on %s -> %s:%d via %s (id=%s)", addr, in.RemoteHost, in.RemotePort, in.Host, id))
	case "stop":
		if err := a.Forwards.Stop(in.ID); err != nil {
			return ToolError(err)
		}
		return Text(fmt.Sprintf("stopped %s", in.ID))
	default:
		return ToolError(fmt.Errorf("action must be \"start\" or \"stop\""))
	}
}

func (f *Forwarder) Start(pool *Pool, alias, remoteHost string, remotePort int) (string, string, error) {
	if remoteHost == "" {
		remoteHost = "127.0.0.1"
	}
	client, err := pool.Client(alias)
	if err != nil {
		return "", "", err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", err
	}
	f.mu.Lock()
	f.n++
	id := fmt.Sprintf("fwd-%d", f.n)
	fw := &forward{id: id, listen: ln, client: client, remotes: net.JoinHostPort(remoteHost, strconv.Itoa(remotePort))}
	f.m[id] = fw
	f.mu.Unlock()

	go func() {
		for {
			local, err := ln.Accept()
			if err != nil {
				return
			}
			go pipe(local, client, fw.remotes)
		}
	}()
	return id, ln.Addr().String(), nil
}

func (f *Forwarder) Stop(id string) error {
	f.mu.Lock()
	fw, ok := f.m[id]
	if ok {
		delete(f.m, id)
	}
	f.mu.Unlock()
	if !ok {
		return fmt.Errorf("no forward with id %s", id)
	}
	fw.listen.Close()
	return nil
}

func pipe(local net.Conn, client interface {
	Dial(network, address string) (net.Conn, error)
}, remoteAddr string) {
	remote, err := client.Dial("tcp", remoteAddr)
	if err != nil {
		local.Close()
		return
	}
	go func() {
		io.Copy(remote, local)
		local.Close()
		remote.Close()
	}()
	go func() {
		io.Copy(local, remote)
		local.Close()
		remote.Close()
	}()
}
