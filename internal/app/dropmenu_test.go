package app

import (
	"runtime"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/egoist/mygo/ui"
)

// TestDropMenu: the sidebar's "+" opens the app's own menu, not the
// system's: its entries with their shortcut, one disabled that does
// nothing; Escape and its button close it; the arrows move through it and
// Enter chooses.
func TestDropMenu(t *testing.T) {
	a, tt := newTestApp(t)
	mustClick(t, tt, "Add")
	if got := menuOf(a); got != nil {
		t.Fatalf("a menu of the system: %q", got)
	}
	add, _ := tt.Find("Add")
	for _, s := range []string{"New Host", "New group", "Quick Connect…", shortcutText(runtime.GOOS, appMods, ui.KeyK), "Local terminal", "Import from ~/.ssh/config…"} {
		if r, ok := tt.Find(s); !ok || r.Y < add.Y+add.H {
			t.Errorf("%q at %v, not in a menu below the button at %v", s, r, add)
		}
	}
	// No host: no port forward to make.
	mustClick(t, tt, "New Port Forward…")
	if a.fwdForm != nil || !tt.HasText("New Port Forward…") {
		t.Error("the disabled entry was chosen, or closed the menu")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.HasText("New group") || a.menu.key != "" {
		t.Error("Escape left the menu open")
	}
	mustClick(t, tt, "Add")
	mustClick(t, tt, "Add")
	if tt.HasText("New group") {
		t.Error("its button left the menu open")
	}

	// From the keyboard: the first entry has the focus, Down moves it.
	mustClick(t, tt, "Add")
	tt.Frame()
	if !tt.Focused("New Host") {
		t.Fatal("the first entry has not the focus")
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if !tt.Focused("New group") {
		t.Fatal("Down did not move to the next entry")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.group == nil || tt.HasText("Quick Connect…") || a.menu.key != "" {
		t.Fatalf("Enter did not choose New group, or left the menu: %q", tt.Texts())
	}
}

// TestDropSubmenu: a host's "more" menu shows its submenu as the pointer
// comes on to its entry, and choosing from it takes the host out of its
// group; its row's actions stay while the menu is open.
func TestDropSubmenu(t *testing.T) {
	a, tt := newTestApp(t)
	h, err := a.Store().Add(assets.Host{Name: "web", Address: "10.0.0.5", User: "root", Group: "Prod"})
	if err != nil {
		t.Fatal(err)
	}
	a.selected = h.ID
	tt.Frame()
	mustClick(t, tt, "More Actions")
	a.selected = ""
	tt.Move(600, 700)
	tt.Frame()
	if !tt.HasText("More Actions") {
		t.Error("the row's actions hid under its open menu")
	}
	if tt.HasText("No group") {
		t.Fatal("submenu shown before asked")
	}
	mv, ok := tt.Find("Move to group")
	if !ok {
		t.Fatalf("no Move to group in %q", tt.Texts())
	}
	tt.Move(mv.X+mv.W/2, mv.Y+mv.H/2)
	tt.Frame()
	tt.Frame()
	if r, ok := tt.Find("No group"); !ok || r.X < mv.X+mv.W {
		t.Fatalf("submenu at %v, not after its entry at %v", r, mv)
	}
	mustClick(t, tt, "No group")
	if got, _ := a.Store().Get(h.ID); got.Group != "" {
		t.Errorf("group %q", got.Group)
	}
	if a.menu.key != "" || tt.HasText("Move to group") {
		t.Error("the menu stayed open")
	}
}
