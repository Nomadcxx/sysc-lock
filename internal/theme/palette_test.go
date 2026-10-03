package theme

import (
	"os"
	"path/filepath"
	"testing"
)

const fixture = `{
  "name": "Test",
  "dark": {"surface": "#101014", "on-surface": "#E2E2E9", "error": "#F2B8B5", "primary": "#B4C5FF"},
  "light": {"surface": "#FAFAFF"}
}`

func TestLoadPaletteRoles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "palette.json")
	if err := os.WriteFile(p, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	pal, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if pal.Surface.R != 0x10 || pal.Surface.B != 0x14 || pal.Surface.A != 0xFF {
		t.Fatalf("surface = %+v", pal.Surface)
	}
	if pal.OnSurface.R != 0xE2 {
		t.Fatalf("on-surface = %+v", pal.OnSurface)
	}
	if pal.Error.G != 0xB8 || pal.Primary.R != 0xB4 {
		t.Fatalf("error/primary = %+v %+v", pal.Error, pal.Primary)
	}
}

func TestMissingFileUsesDefaults(t *testing.T) {
	pal, err := Load("/nonexistent/palette.json")
	if err != nil {
		t.Fatal(err)
	}
	if pal.Surface == (pal.OnSurface) {
		t.Fatal("defaults look unset")
	}
}

func TestHalfWrittenFileDoesNotCrash(t *testing.T) {
	p := filepath.Join(t.TempDir(), "palette.json")
	if err := os.WriteFile(p, []byte(fixture[:len(fixture)/2]), 0o600); err != nil {
		t.Fatal(err)
	}
	pal, err := Load(p)
	if err != nil {
		t.Fatal(err) // truncated file must fall back, not fail
	}
	if pal.Surface.A != 0xFF {
		t.Fatal("bad default")
	}
}

func TestNoAlphaInHex(t *testing.T) {
	// PaletteFile stores #RRGGBB; alpha must come out 0xFF.
	p := filepath.Join(t.TempDir(), "palette.json")
	if err := os.WriteFile(p, []byte(`{"name":"x","dark":{"surface":"#ABCDEF"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pal, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := [4]uint8{pal.Surface.R, pal.Surface.G, pal.Surface.B, pal.Surface.A}; got != [4]uint8{0xAB, 0xCD, 0xEF, 0xFF} {
		t.Fatalf("got %v", got)
	}
}

func TestDefaultsNonzero(t *testing.T) {
	pal := Default()
	if pal.Surface.A != 0xFF || pal.OnSurface.A != 0xFF {
		t.Fatal("defaults must be opaque")
	}
}
