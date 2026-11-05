package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/locales"
)

func TestLayoutSkippedPanesRemapFocusAndActiveTab(t *testing.T) {
	dir, backend := t.TempDir(), keychain.NewMemory()
	a, _ := appIn(t, dir, backend)
	h := savedHost(t, a, "surviving", "127.0.0.1")
	a.Close()
	snap := layoutSnap{Version: 1, Active: 2, Tabs: []tabSnap{
		{Title: "deleted tab", Focus: 1, Root: &nodeSnap{ID: 1, Kind: "terminal", Host: "deleted-host"}},
		{Title: "mixed", Focus: 4, Root: &nodeSnap{Split: "row", Ratio: 0.2,
			A: &nodeSnap{ID: 2, Kind: "terminal", Host: "deleted-host"},
			B: &nodeSnap{Split: "column", Ratio: 0.7,
				A: &nodeSnap{ID: 3, Kind: "terminal", Host: h.ID},
				B: &nodeSnap{ID: 4, Kind: "ai", Host: h.ID, Link: 3}}}},
		{Title: "active", Focus: 5, Root: &nodeSnap{ID: 5, Kind: "ai", Host: h.ID}},
		{Title: "missing shell", Focus: 6, Root: &nodeSnap{ID: 6, Kind: "terminal", Host: "local:missing-shell-test-id"}},
	}}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "layout.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	again, tt := appIn(t, dir, backend)
	if len(again.ws.Tabs) != 2 || again.ws.Active != 1 || again.ws.ActiveTab().Title != "active" {
		t.Fatalf("filtered tabs lost active mapping: tabs=%d active=%d", len(again.ws.Tabs), again.ws.Active)
	}
	mixed := again.ws.Tabs[0]
	if mixed.Title != "mixed" || mixed.Root.Axis != layout.Column || mixed.Root.Ratio != 0.7 || mixed.Focused().Kind != layout.AI {
		t.Fatalf("collapsed tree lost surviving focus: %+v", mixed)
	}
	ai := again.conns[mixed.Focused().ID]
	if ai.ai.link != mixed.Root.A.Item.ID {
		t.Fatal("AI link did not map to surviving terminal")
	}
	if !tt.HasText(locales.In("en").LayoutRestoreSkipped()) {
		t.Fatalf("skipped panes were not reported: %q", tt.Texts())
	}
}

func TestLayoutInvalidSnapshotsRemainProtected(t *testing.T) {
	for name, raw := range map[string]string{
		"corrupt":               "{",
		"null":                  "null",
		"empty object":          "{}",
		"explicit version zero": `{"version":0,"tabs":[]}`,
		"unknown version":       `{"version":99,"tabs":[]}`,
		"invalid ratio":         `{"version":1,"tabs":[{"root":{"split":"row","ratio":1,"a":{"id":1,"kind":"ai","host":"x"},"b":{"id":2,"kind":"ai","host":"y"}}}]}`,
		"duplicate identity":    `{"version":1,"tabs":[{"root":{"id":1,"kind":"ai","host":"x"}},{"root":{"id":1,"kind":"ai","host":"y"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "layout.json")
			if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			a, tt := appIn(t, dir, nil)
			if len(a.ws.Tabs) != 0 || !tt.HasText(locales.In("en").ErrLayoutRead()) {
				t.Fatalf("invalid layout not reported: tabs=%d texts=%q", len(a.ws.Tabs), tt.Texts())
			}
			h := savedHost(t, a, "new", "127.0.0.1")
			if _, err := a.OpenAI(h.ID, openAt{}, 0); err != nil {
				t.Fatal(err)
			}
			a.saveLayout()
			a.Close()
			after, err := os.ReadFile(file)
			if err != nil || string(after) != raw {
				t.Fatalf("invalid source overwritten: %q err=%v", after, err)
			}
		})
	}
}

func TestLayoutTogglePreservesRunningConnection(t *testing.T) {
	srv, _ := startScript(t, "ready\r\n")
	dir, backend := t.TempDir(), keychain.NewMemory()
	a, tt := appIn(t, dir, backend)
	c := connected(t, a, tt, srv)
	a.saveLayout()
	file := filepath.Join(dir, "layout.json")
	s := a.Settings()
	s.RememberLayout = false
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("disabled layout still exists: %v", err)
	}
	if a.focused() != c || c.s.Status().State != session.Connected || srv.Logins() != 1 {
		t.Fatal("disabling layout interrupted the current connection")
	}
	a.saveLayout()
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("disabled persistence wrote layout: %v", err)
	}
	s.RememberLayout = true
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("enabling did not save current layout:", err)
	}
	if a.focused() != c || srv.Logins() != 1 {
		t.Fatal("enabling layout replaced the current connection")
	}
	a.Close()
	again, tt := appIn(t, dir, backend)
	waitFor(t, tt, "reenabled layout login", func() bool { return again.focused() != nil && again.focused().s.Status().State == session.Connected })
	if srv.Logins() != 2 || len(again.ws.Tabs) != 1 {
		t.Fatalf("reenabled layout logins=%d tabs=%d", srv.Logins(), len(again.ws.Tabs))
	}
}

func TestLayoutLegacySnapshotDoesNotBlockFutureMemory(t *testing.T) {
	dir, backend := t.TempDir(), keychain.NewMemory()
	file := filepath.Join(dir, "layout.json")
	raw := `{"active":0,"tabs":[{"focus":1,"root":{"kind":"ai","host":"old-host"}}]}`
	if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	a, _ := appIn(t, dir, backend)
	if len(a.ws.Tabs) != 0 {
		t.Fatal("legacy AI snapshot restored automatically")
	}
	h := savedHost(t, a, "new workspace", "127.0.0.1")
	if _, err := a.OpenAI(h.ID, openAt{}, 0); err != nil {
		t.Fatal(err)
	}
	a.ws.ActiveTab().Title = "After upgrade"
	a.Close()
	again, _ := appIn(t, dir, backend)
	if len(again.ws.Tabs) != 1 || again.ws.ActiveTab().Title != "After upgrade" || again.focused().host.ID != h.ID {
		t.Fatal("legacy snapshot blocked saving and restoring the new workspace")
	}
}
