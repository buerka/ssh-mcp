// Package internal implements the ssh-mcp server: a stdio MCP server that
// exposes SSH command execution, SFTP file transfer and port forwarding for
// hosts configured in ~/.ssh/config.
package internal

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// MaxOutput bounds how many bytes of command output are returned to the model
// in a single tool response. The rest stays in memory and can be fetched on
// demand via the ssh_output tool. Settable via SSH_MCP_MAX_OUTPUT.
var MaxOutput = 8 * 1024

var privKeyRe = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)

// Redact scrubs private key material from arbitrary text before it reaches
// the model. Applied to every tool response.
func Redact(s string) string {
	return privKeyRe.ReplaceAllLiteralString(s, "[REDACTED PRIVATE KEY]")
}

// OutputStore keeps the full, untruncated output of each exec so the model
// can page through it with ssh_output when it actually needs more.
type OutputStore struct {
	mu sync.Mutex
	m  map[string][]byte
	n  int
}

func NewOutputStore() *OutputStore { return &OutputStore{m: make(map[string][]byte)} }

func (s *OutputStore) Save(b []byte) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	id := fmt.Sprintf("out-%d", s.n)
	s.m[id] = b
	return id
}

// Page returns the [offset, offset+length) window of stored output.
// length <= 0 means "to the end".
func (s *OutputStore) Page(id string, offset, length int) (string, bool) {
	s.mu.Lock()
	b, ok := s.m[id]
	s.mu.Unlock()
	if !ok {
		return "", false
	}
	if offset < 0 {
		offset = 0
	}
	if offset > len(b) {
		return "", true
	}
	end := len(b)
	if length > 0 && offset+length < end {
		end = offset + length
	}
	return string(b[offset:end]), true
}

// Truncate returns printable output for the model: redacted, and cropped to
// head+tail when over max. The inline note names the id and byte offset for
// fetching the omitted middle with ssh_output.
func Truncate(b []byte, max int, id string) string {
	if max <= 0 {
		max = MaxOutput
	}
	redacted := []byte(Redact(string(b)))
	if len(redacted) <= max {
		return string(redacted)
	}
	head := max * 2 / 3
	tail := max - head
	omitted := len(redacted) - head - tail
	var sb strings.Builder
	sb.Write(redacted[:head])
	fmt.Fprintf(&sb, "\n...[%d bytes omitted; fetch with ssh_output id=%s offset=%d]...\n", omitted, id, head)
	sb.Write(redacted[len(redacted)-tail:])
	return sb.String()
}
