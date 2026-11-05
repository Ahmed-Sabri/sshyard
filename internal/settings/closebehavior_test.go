package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCloseBehavior(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	got, err := Load(path)
	if err != nil || got.CloseBehavior != CloseToTray {
		t.Fatalf("missing file: close behavior %q, error %v", got.CloseBehavior, err)
	}
	for _, data := range []string{`{}`, `{"closeBehavior":""}`, `{"closeBehavior":"unknown"}`} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil || got.CloseBehavior != CloseToTray {
			t.Fatalf("%s: got %q, error %v", data, got.CloseBehavior, err)
		}
	}
	for _, behavior := range []string{CloseQuit, CloseToTray} {
		s := Default()
		s.CloseBehavior = behavior
		s.Normalize()
		if s.CloseBehavior != behavior {
			t.Fatalf("Normalize lost %q", behavior)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil || got.CloseBehavior != behavior {
			t.Fatalf("saved %q: got %q, error %v", behavior, got.CloseBehavior, err)
		}
	}
}
