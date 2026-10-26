package internal

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type LSIn struct {
	Host string `json:"host" jsonschema:"host alias from ~/.ssh/config, or a raw hostname"`
	Path string `json:"path" jsonschema:"absolute remote directory path"`
}

// LS lists a remote directory, one entry per line: mode size name.
func (a *App) LS(ctx context.Context, req *mcp.CallToolRequest, in LSIn) (*mcp.CallToolResult, any, error) {
	client, err := a.Pool.SFTP(in.Host)
	if err != nil {
		return ToolError(err)
	}
	entries, err := client.ReadDir(in.Path)
	if err != nil {
		return ToolError(fmt.Errorf("readdir %s on %s: %w", in.Path, in.Host, err))
	}
	var out []byte
	for _, e := range entries {
		out = append(out, []byte(fmt.Sprintf("%s %12d %s\n", e.Mode().String(), e.Size(), e.Name()))...)
	}
	return Text(Redact(string(out)))
}

type GetIn struct {
	Host       string `json:"host" jsonschema:"host alias from ~/.ssh/config, or a raw hostname"`
	RemotePath string `json:"remote_path" jsonschema:"file to download from the remote host"`
	LocalPath  string `json:"local_path" jsonschema:"destination file path on this machine"`
}

// Get downloads a file over SFTP.
func (a *App) Get(ctx context.Context, req *mcp.CallToolRequest, in GetIn) (*mcp.CallToolResult, any, error) {
	client, err := a.Pool.SFTP(in.Host)
	if err != nil {
		return ToolError(err)
	}
	src, err := client.Open(in.RemotePath)
	if err != nil {
		return ToolError(fmt.Errorf("open %s on %s: %w", in.RemotePath, in.Host, err))
	}
	defer src.Close()
	dst, err := os.Create(in.LocalPath)
	if err != nil {
		return ToolError(fmt.Errorf("create %s: %w", in.LocalPath, err))
	}
	defer dst.Close()
	n, err := io.Copy(dst, src)
	if err != nil {
		return ToolError(fmt.Errorf("download: %w", err))
	}
	return Text(fmt.Sprintf("downloaded %d bytes: %s:%s -> %s", n, in.Host, in.RemotePath, in.LocalPath))
}

type PutIn struct {
	Host       string `json:"host" jsonschema:"host alias from ~/.ssh/config, or a raw hostname"`
	LocalPath  string `json:"local_path" jsonschema:"file on this machine to upload"`
	RemotePath string `json:"remote_path" jsonschema:"destination file path on the remote host"`
}

// Put uploads a file over SFTP.
func (a *App) Put(ctx context.Context, req *mcp.CallToolRequest, in PutIn) (*mcp.CallToolResult, any, error) {
	client, err := a.Pool.SFTP(in.Host)
	if err != nil {
		return ToolError(err)
	}
	src, err := os.Open(in.LocalPath)
	if err != nil {
		return ToolError(fmt.Errorf("open %s: %w", in.LocalPath, err))
	}
	defer src.Close()
	dst, err := client.Create(in.RemotePath)
	if err != nil {
		return ToolError(fmt.Errorf("create %s on %s: %w", in.RemotePath, in.Host, err))
	}
	defer dst.Close()
	n, err := io.Copy(dst, src)
	if err != nil {
		return ToolError(fmt.Errorf("upload: %w", err))
	}
	return Text(fmt.Sprintf("uploaded %d bytes: %s -> %s:%s", n, in.LocalPath, in.Host, in.RemotePath))
}
