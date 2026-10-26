package internal

import (
	"os"
	"path/filepath"
	"testing"
)

func withTempCreds(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	old := CredsFile
	CredsFile = filepath.Join(dir, "ssh-mcp.json")
	t.Cleanup(func() { CredsFile = old })
}

func TestPasswordRoundTrip(t *testing.T) {
	withTempCreds(t)

	if pw, err := LoadPasswords(); err != nil || len(pw) != 0 {
		t.Fatalf("missing file should load empty: pw=%v err=%v", pw, err)
	}
	if _, ok := LookupPassword("web1", "web1.example.com"); ok {
		t.Fatal("no password expected yet")
	}

	if err := SavePassword("web1", "s3cret"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(CredsFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("file mode = %v, want 0600", got)
	}
	if p, ok := LookupPassword("web1", "web1.example.com"); !ok || p != "s3cret" {
		t.Fatalf("lookup failed: %q ok=%v", p, ok)
	}

	// Overwrite, then add a second host.
	if err := SavePassword("web1", "new"); err != nil {
		t.Fatal(err)
	}
	if err := SavePassword("db1", "dbpass"); err != nil {
		t.Fatal(err)
	}

	// Lookup may fall back to the resolved hostname.
	if p, ok := LookupPassword("other-alias", "db1"); !ok || p != "dbpass" {
		t.Fatalf("lookup by hostname failed: %q ok=%v", p, ok)
	}
	hosts, err := ListPasswordHosts()
	if err != nil || len(hosts) != 2 || hosts[0] != "db1" || hosts[1] != "web1" {
		t.Fatalf("list = %v err=%v", hosts, err)
	}

	// Delete via empty password.
	if err := SavePassword("web1", ""); err != nil {
		t.Fatal(err)
	}
	if hosts, _ := ListPasswordHosts(); len(hosts) != 1 || hosts[0] != "db1" {
		t.Fatalf("after delete: %v", hosts)
	}
}

func TestPasswordRejectsLoosePerms(t *testing.T) {
	withTempCreds(t)
	if err := SavePassword("web1", "s3cret"); err != nil {
		t.Fatal(err)
	}
	os.Chmod(CredsFile, 0644)
	if _, err := LoadPasswords(); err == nil {
		t.Fatal("expected error for group/world-readable credentials file")
	}
}
