package internal

import (
	"strings"
	"testing"
)

func TestRedactPrivateKeys(t *testing.T) {
	in := "before\n-----BEGIN OPENSSH PRIVATE KEY-----\nabc\ndef\n-----END OPENSSH PRIVATE KEY-----\nafter"
	got := Redact(in)
	if strings.Contains(got, "abc") || strings.Contains(got, "BEGIN") {
		t.Fatalf("private key leaked: %q", got)
	}
	if !strings.HasPrefix(got, "before") || !strings.HasSuffix(got, "after") {
		t.Fatalf("surrounding text damaged: %q", got)
	}
	if Redact("no keys here") != "no keys here" {
		t.Fatal("plain text was modified")
	}
}

func TestTruncateUnderLimit(t *testing.T) {
	b := []byte("hello")
	if got := Truncate(b, 1024, "out-1"); got != "hello" {
		t.Fatalf("small output changed: %q", got)
	}
}

func TestTruncateHeadTailAndPaging(t *testing.T) {
	full := []byte(strings.Repeat("a", 10000) + strings.Repeat("b", 10000))
	max := 1024
	id := "out-9"
	got := Truncate(full, max, id)
	if len(got) > max+200 {
		t.Fatalf("truncated output too large: %d", len(got))
	}
	if !strings.Contains(got, "bytes omitted") || !strings.Contains(got, "id="+id) {
		t.Fatalf("missing truncation note: %q", got[len(got)-200:])
	}
	if !strings.HasPrefix(got, "aaaa") {
		t.Fatal("head not preserved")
	}
	if !strings.HasSuffix(got, "bbbb") {
		t.Fatal("tail not preserved")
	}

	store := NewOutputStore()
	sid := store.Save(full)
	page, ok := store.Page(sid, max, 10)
	if !ok || len(page) != 10 {
		t.Fatalf("page failed: ok=%v len=%d", ok, len(page))
	}
	if page != strings.Repeat("a", 10) {
		t.Fatal("page content mismatch")
	}
	if _, ok := store.Page("out-nonexistent", 0, 0); ok {
		t.Fatal("unknown id should not be found")
	}
}

func TestParseSSHConfig(t *testing.T) {
	cfg, err := parseSSHConfig("testdata/ssh_config", 0)
	if err != nil {
		t.Fatal(err)
	}
	var hasWeb1 bool
	for _, h := range cfg.Hosts {
		for _, p := range h.Patterns {
			if p.String() == "web1" {
				hasWeb1 = true
			}
		}
	}
	if !hasWeb1 {
		t.Fatal("expected pattern web1 in parsed config")
	}
}
