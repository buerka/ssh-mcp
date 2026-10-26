package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

// CredsFile stores optional SSH passwords keyed by host alias. It lives in
// ~/.ssh next to the keys it supplements, mode 0600. The file never leaves
// this machine and its contents never appear in tool output or logs.
var CredsFile = filepath.Join(homeDir(), ".ssh", "ssh-mcp.json")

// LoadPasswords reads the credentials file. A missing file is not an error.
func LoadPasswords() (map[string]string, error) {
	b, err := os.ReadFile(CredsFile)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", CredsFile, err)
	}
	// Unix permission bits don't exist on Windows; skip the check there.
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(CredsFile); err == nil && info.Mode().Perm()&0077 != 0 {
			return nil, fmt.Errorf("%s is readable by group/others; fix with: chmod 600 %s", CredsFile, CredsFile)
		}
	}
	var pw map[string]string
	if err := json.Unmarshal(b, &pw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", CredsFile, err)
	}
	return pw, nil
}

// LookupPassword returns the password for a host, trying the alias first and
// then the resolved hostname.
func LookupPassword(alias, hostName string) (string, bool) {
	pw, err := LoadPasswords()
	if err != nil {
		return "", false
	}
	if p, ok := pw[alias]; ok {
		return p, true
	}
	p, ok := pw[hostName]
	return p, ok
}

// SavePassword writes one host's password, creating the file with 0600.
func SavePassword(alias, password string) error {
	pw, err := LoadPasswords()
	if err != nil {
		return err
	}
	if pw == nil {
		pw = map[string]string{}
	}
	if password == "" {
		delete(pw, alias)
	} else {
		pw[alias] = password
	}
	return writePasswords(pw)
}

func writePasswords(pw map[string]string) error {
	dir := filepath.Dir(CredsFile)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(pw, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	// Write through a temp file so a crash never leaves a half-written
	// credentials file, and the final mode is set before data lands.
	tmp, err := os.CreateTemp(dir, ".ssh-mcp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, CredsFile); err != nil {
		return err
	}
	os.Chmod(CredsFile, 0600) // rename may have kept tmp's mode; enforce anyway
	return nil
}

// ListPasswordHosts returns the aliases with stored passwords, sorted.
func ListPasswordHosts() ([]string, error) {
	pw, err := LoadPasswords()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(pw))
	for h := range pw {
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}
