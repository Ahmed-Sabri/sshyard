package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTerminalCursorBlink(t *testing.T) {
	if !Default().TerminalCursorBlink {
		t.Fatal("cursor blinking must default on")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if got, err := Load(path); err != nil || !got.TerminalCursorBlink {
		t.Fatalf("missing file: cursor blink = %v, error %v", got.TerminalCursorBlink, err)
	}
	for _, tc := range []struct {
		body string
		want bool
	}{{`{}`, true}, {`{"terminalCursorBlink":false}`, false}, {`{"terminalCursorBlink":true}`, true}} {
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil || got.TerminalCursorBlink != tc.want {
			t.Fatalf("%s: cursor blink = %v, want %v, error %v", tc.body, got.TerminalCursorBlink, tc.want, err)
		}
	}
	for _, on := range []bool{false, true} {
		s := Default()
		s.TerminalCursorBlink = on
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil || got.TerminalCursorBlink != on {
			t.Fatalf("saved %v: cursor blink = %v, error %v", on, got.TerminalCursorBlink, err)
		}
	}
}
