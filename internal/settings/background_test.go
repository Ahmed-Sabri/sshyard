package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackgroundPreferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"theme":"auto"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil || s.Background != DefaultBackground() {
		t.Fatalf("old settings: %+v %v", s.Background, err)
	}
	s.Background = Background{Image: "background-123.png", Fit: "contain", ChromeOpacity: 0, ContentOpacity: 100}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got.Background != s.Background {
		t.Fatalf("round trip: %+v %v", got.Background, err)
	}
	for _, name := range []string{"/tmp/a.png", "../background-a.png", `background-a\b.png`, "background-a.png/else", "background-a.gif"} {
		s.Background = Background{Image: name, Fit: "bad", ChromeOpacity: -1, ContentOpacity: 101}
		s.Normalize()
		if s.Background != DefaultBackground() {
			t.Fatalf("unsafe %q: %+v", name, s.Background)
		}
	}
}
