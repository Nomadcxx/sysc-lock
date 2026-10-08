package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFollowShell(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "sysc-shell"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sysc-shell", "shell-theme")
	for _, name := range []string{"dracula", "blue", "tokyo-night", "catppuccin", "rose-pine", "kanagawa", "noctalia", "eldritch-abyss", "void", "red", "cyan", "coral", "pink"} {
		if err := os.WriteFile(path, []byte(name+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if got := Default().WithShellTheme(); got.Palette != name {
			t.Fatalf("%s => %s", name, got.Palette)
		}
	}
	c := Default()
	off := false
	c.FollowShell = &off
	if c.WithShellTheme().Palette != c.Palette {
		t.Fatal("ignored opt-out")
	}
	for _, body := range []string{"../dracula", "unknown", "", string(make([]byte, 200))} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if got := Default().WithShellTheme(); got.Palette != Default().Palette {
			t.Fatal("invalid file changed palette")
		}
	}
	os.Remove(path)
	if err := os.Symlink("/dev/zero", path); err != nil {
		t.Fatal(err)
	}
	if got := Default().WithShellTheme(); got.Palette != Default().Palette {
		t.Fatal("followed symlink")
	}
}
