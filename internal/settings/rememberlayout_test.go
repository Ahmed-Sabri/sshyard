package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRememberLayoutDefaultAndPersistence(t *testing.T) {
	if Default().RememberLayout {
		t.Fatal("layout memory must default off")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if s, err := Load(path); err != nil || s.RememberLayout {
		t.Fatalf("missing settings: remember=%v, %v", s.RememberLayout, err)
	}
	for _, raw := range []string{`{}`, `{"theme":"dark"}`, `{"rememberLayout":true}`, `{"rememberLayout":false}`} {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Contains(raw, "true")
		if s.RememberLayout != want {
			t.Fatalf("load %s: remember=%v", raw, s.RememberLayout)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		saved, err := Load(path)
		if err != nil || saved.RememberLayout != want {
			t.Fatalf("roundtrip %s: %+v, %v", raw, saved, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"rememberLayout":`) {
			t.Fatal("preference omitted from saved settings")
		}
	}
}
