package app

import (
	"github.com/Licoy/sshyard/internal/settings"
	"testing"
)

func TestOSC52DefaultAsksBeforeClipboardWrite(t *testing.T) {
	a, tt := newTestApp(t)
	if a.Settings().OSC52Write != settings.OSC52Ask {
		t.Fatal("new app must ask")
	}
	c := connectOSC(t, a, "box")
	tt.SetClipboard("local clipboard")
	writeOSC(t, c, "remote clipboard")
	tt.Frame()
	if tt.Clipboard() != "local clipboard" {
		t.Fatalf("remote changed clipboard: %q", tt.Clipboard())
	}
	if !tt.HasText("Remote clipboard write") || !tt.HasText("Allow once") {
		t.Fatalf("missing confirmation: %q", tt.Texts())
	}
	mustClick(t, tt, "Deny")
	if tt.Clipboard() != "local clipboard" {
		t.Fatal("deny changed clipboard")
	}
}
