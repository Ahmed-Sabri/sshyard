package app

import (
	"testing"

	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// TestRenameTab: the tab's menu renames it through a sheet that takes the
// typing at once; the tab and the more-tabs list show the name, which
// survives a split, and an empty name gives back the host's.
func TestRenameTab(t *testing.T) {
	withShells(t, session.Shell{ID: "/bin/sh", Name: "sh", Path: "/bin/sh"})
	a, tt := newTestApp(t)
	m := locales.In("en")
	if _, err := a.OpenLocalTerminal(); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tab := a.ws.ActiveTab()
	if got := a.tabLabel(tab); got != "Local terminal" {
		t.Fatalf("tab named %q", got)
	}
	a.openContext(10, 10, func() []menuEntry { return a.tabEntries(m, 0) })
	tt.Frame()
	if err := chooseMenu(a, tt, "Rename Tab…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	mustFind(t, tt, "Rename Tab")
	tt.Type("build box")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if tab.Title != "build box" || a.renaming {
		t.Fatalf("title %q, sheet open %v", tab.Title, a.renaming)
	}
	if !tt.HasText("build box") {
		t.Errorf("the tab does not show its name: %q", tt.Texts())
	}
	if _, err := a.OpenLocalTerminal(); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.tabLabel(tab) != "build box" {
		t.Errorf("after a split the tab is %q", a.tabLabel(tab))
	}

	// The sheet opens on the name, and Cancel keeps it.
	a.openTabRename(tab)
	tt.Frame()
	if a.tabRename.name != "build box" {
		t.Errorf("sheet opened on %q", a.tabRename.name)
	}
	mustClick(t, tt, "Cancel")
	tt.Frame()
	if tab.Title != "build box" || a.tabRename != nil {
		t.Errorf("Cancel: title %q, form %v", tab.Title, a.tabRename)
	}
	a.RenameTab(tab.ID, "  ")
	if a.tabLabel(tab) != "Local terminal +1" {
		t.Errorf("an empty name left %q", a.tabLabel(tab))
	}
}
