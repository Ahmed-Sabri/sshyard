package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrayIcon(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	got, err := Load(path)
	if err != nil || got.TrayIcon != TrayIconColor {
		t.Fatalf("default %q, error %v", got.TrayIcon, err)
	}
	for _, data := range []string{`{}`, `{"trayIcon":""}`, `{"trayIcon":"unknown"}`} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil || got.TrayIcon != TrayIconColor {
			t.Fatalf("%s: got %q, error %v", data, got.TrayIcon, err)
		}
	}
	for _, style := range []string{TrayIconColor, TrayIconMono} {
		s := Default()
		s.TrayIcon = style
		s.Normalize()
		if s.TrayIcon != style {
			t.Fatalf("Normalize lost %q", style)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil || got.TrayIcon != style {
			t.Fatalf("saved %q: got %q, error %v", style, got.TrayIcon, err)
		}
	}
}
