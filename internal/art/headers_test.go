package art

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestHeaderConfPreservesLiteralArt(t *testing.T) {
	hs, err := ParseHeaders("# outside\r\nascii_1=\"\"\"\r\n  # = /\\\r\n A    B\r\n\"\"\"\r\nascii_2=\"\"\"\nSECOND\n\"\"\"\n")
	if err != nil || len(hs) != 2 || hs[0].ID != "ascii_1" || hs[0].Text != "  # = /\\\n A    B" || hs[1].Text != "SECOND" {
		t.Fatalf("headers=%+v err=%v", hs, err)
	}
}
func TestHeaderConfRejectsMalformedOrUnboundedArt(t *testing.T) {
	for _, s := range []string{"", "ascii_1=\"\"\"\nA", "ascii_1=\"\"\"\n\x1b[31mA\n\"\"\"", "ascii_1=\"\"\"\nA\n\"\"\"\nascii_1=\"\"\"\nB\n\"\"\"", strings.Repeat("x", MaxHeaderBytes+1), "ascii_1=\"\"\"\n" + strings.Repeat("X", 121) + "\n\"\"\""} {
		if _, err := ParseHeaders(s); err == nil {
			t.Fatalf("invalid config accepted: length %d", len(s))
		}
	}
}
func TestHeaderLoadFallsBackWithoutFollowingSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.conf")
	hs, err := LoadHeaders(missing)
	if err != nil || len(hs) < 4 {
		t.Fatal("missing catalogue did not use shipped headers", err)
	}
	actual := filepath.Join(dir, "actual")
	link := filepath.Join(dir, "link")
	fifo := filepath.Join(dir, "fifo")
	if err = os.WriteFile(actual, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(actual, link); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{actual, link, fifo, dir} {
		hs, err = LoadHeaders(path)
		if err == nil || len(hs) < 4 {
			t.Fatal("bad customization prevented safe fallback", path, err)
		}
	}
}

func TestSeedHeadersPreservesCustomization(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sysc-lock", "headers.conf")
	if err := SeedHeaders(p); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadHeaders(p); err != nil || len(got) != len(DefaultHeaders()) {
		t.Fatalf("seed: %v %v", got, err)
	}
	custom := []byte("# custom artwork\n")
	if err := os.WriteFile(p, custom, 0600); err != nil {
		t.Fatal(err)
	}
	if err := SeedHeaders(p); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(p); err != nil || string(got) != string(custom) {
		t.Fatal("seed replaced customization")
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "missing")
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
	if err := SeedHeaders(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("seed followed dangling symlink")
	}
}
