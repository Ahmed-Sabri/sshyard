package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTerminalCursorStyle(t *testing.T) {
	if got := Default().TerminalCursorStyle; got != CursorBlock {
		t.Fatalf("default cursor = %q, want %q", got, CursorBlock)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	for _, body := range []string{`{}`, `{"terminalCursorStyle":""}`, `{"terminalCursorStyle":"beam"}`} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil || got.TerminalCursorStyle != CursorBlock {
			t.Fatalf("%s: cursor = %q, error %v", body, got.TerminalCursorStyle, err)
		}
	}
	for _, style := range []string{CursorBlock, CursorBar, CursorUnderline} {
		s := Default()
		s.TerminalCursorStyle = style
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil || got.TerminalCursorStyle != style {
			t.Fatalf("saved %q: cursor = %q, error %v", style, got.TerminalCursorStyle, err)
		}
	}
}
