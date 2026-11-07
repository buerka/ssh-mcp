package internal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestBackoffDelaySequence(t *testing.T) {
	oldBase, oldMax := BackoffBase, BackoffMax
	BackoffBase, BackoffMax = 5*time.Millisecond, 45*time.Millisecond
	defer func() { BackoffBase, BackoffMax = oldBase, oldMax }()

	for n, want := range map[int]time.Duration{1: 5 * time.Millisecond, 2: 15 * time.Millisecond, 3: 45 * time.Millisecond, 10: 45 * time.Millisecond} {
		if got := backoffDelay(n); got != want {
			t.Fatalf("backoffDelay(%d) = %v, want %v", n, got, want)
		}
	}
}

func TestBackoffBlocksThenRecovers(t *testing.T) {
	oldBase := BackoffBase
	BackoffBase = time.Millisecond
	defer func() { BackoffBase = oldBase }()

	p := NewPool(true)
	p.recordFailure("tx", os.ErrNotExist)
	if wait, n := p.backoffRemaining("tx"); n != 1 || wait <= 0 {
		t.Fatalf("expected active backoff after 1 failure, got wait=%v n=%d", wait, n)
	}
	// Simulate the window elapsing.
	p.mu.Lock()
	p.lastFail["tx"] = time.Now().Add(-time.Hour)
	p.mu.Unlock()
	if wait, n := p.backoffRemaining("tx"); wait != 0 || n != 1 {
		t.Fatalf("expected backoff expired, got wait=%v n=%d", wait, n)
	}
	p.resetFailures("tx")
	if _, n := p.backoffRemaining("tx"); n != 0 {
		t.Fatalf("expected clean state after success reset, got n=%d", n)
	}
}

func TestAuthMethodsDiagnostics(t *testing.T) {
	dir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "test key")
	if err != nil {
		t.Fatal(err)
	}
	pemFile := filepath.Join(dir, "id_pem")
	if err := os.WriteFile(pemFile, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SSH_AUTH_SOCK", "") // no agent
	cfg := &HostConfig{
		Alias:         "t1",
		HostName:      "t1.example.com",
		User:          "u",
		Port:          22,
		IdentityFiles: []string{pemFile, filepath.Join(dir, "missing")},
	}
	p := NewPool(true)
	methods, keys := p.authMethods(cfg)
	if len(methods) < 1 {
		t.Fatalf("expected at least 1 method, got %d", len(methods))
	}
	var sawKey, sawMissing bool
	for _, k := range keys {
		if k.Source == pemFile && k.Fingerprint != "" && k.Err == "" {
			sawKey = true
		}
		if strings.HasSuffix(k.Source, "missing") && strings.Contains(k.Err, "unreadable") {
			sawMissing = true
		}
	}
	if !sawKey || !sawMissing {
		t.Fatalf("diagnostics incomplete: %+v", keys)
	}
	suffix := diagSuffix(cfg, keys)
	if !strings.Contains(suffix, "resolved: user=u t1.example.com:22") {
		t.Fatalf("diagSuffix missing resolved line: %q", suffix)
	}
	if !strings.Contains(suffix, "ssh-mcp password set") {
		t.Fatalf("diagSuffix missing password hint: %q", suffix)
	}
}
