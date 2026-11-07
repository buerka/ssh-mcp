package internal

import (
	"io"
	"log"
	"os"
)

// VLog emits connection diagnostics to stderr. Silent unless SetVerbose(true)
// is called (the --verbose flag); stderr only, never stdout, so the MCP
// protocol stream stays clean. Diagnostics contain host names, resolved
// settings and key fingerprints — never key material.
var VLog = log.New(io.Discard, "ssh-mcp: ", log.Ltime)

// SetVerbose enables diagnostic logging.
func SetVerbose(v bool) {
	if v {
		VLog.SetOutput(os.Stderr)
	}
}
