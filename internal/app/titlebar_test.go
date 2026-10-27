package app

import (
	"math"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
)

// TestTitleBar runs one band across the window: the app's name over the
// sidebar, the way back while the settings show, and the window's own
// buttons where the app draws them.
func TestTitleBar(t *testing.T) {
	a, tt := newTestApp(t)
	band := float32(toolbarHeight)
	in := func(s string) bool {
		r, ok := tt.Find(s)
		return ok && r.Y+r.H <= band
	}
	if !in("SSHYard") {
		t.Errorf("app name not in the title bar")
	}
	if r, ok := tt.Find("Search hosts"); !ok || r.Y < band {
		t.Errorf("search at %v, in the title bar", r)
	}
	if tt.HasText("Minimize") {
		t.Error("window buttons without the window's")
	}

	var mins, maxes, restores, closes int
	a.SetWindow(WindowControls{
		Minimize:   func() { mins++ },
		Maximize:   func() { maxes++ },
		Unmaximize: func() { restores++ },
		Close:      func() { closes++ },
	})
	tt.Frame()
	if !in("Minimize") || !in("Maximize") || !in("Close Window") {
		t.Fatalf("no window buttons in the title bar: %q", tt.Texts())
	}
	if r, _ := tt.Find("Close Window"); r.X+r.W < 1200-40 {
		t.Errorf("close at %v, not at the end", r)
	}
	mustClick(t, tt, "Minimize")
	mustClick(t, tt, "Maximize")
	a.SetMaximized(true)
	tt.Frame()
	if tt.HasText("Maximize") {
		t.Error("maximize while maximized")
	}
	mustClick(t, tt, "Restore")
	mustClick(t, tt, "Close Window")
	if mins != 1 || maxes != 1 || restores != 1 || closes != 1 {
		t.Errorf("minimize %d, maximize %d, restore %d, close %d", mins, maxes, restores, closes)
	}

	a.openSettings(a.prefNow())
	tt.Frame()
	if !in("Back to Sessions") || in("SSHYard") {
		t.Errorf("settings head not in the title bar: %q", tt.Texts())
	}
	mustClick(t, tt, "Back to Sessions")
	if a.page == pageSettings {
		t.Error("still in the settings")
	}
}

// TestPaneGutter keeps panes as far from each other as from the edges of
// the content.
func TestPaneGutter(t *testing.T) {
	a, tt := newTestApp(t)
	if _, err := a.OpenLocalTerminal(); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	first := a.focused()
	waitForIn(t, tt, slowWait, "shell", func() bool { return first.s.Status().State == session.Connected })
	a.SplitFocused(layout.Right)
	tt.Frame()
	tt.Frame()
	second := a.focused()
	p1, p2 := a.paneBounds[first.id], a.paneBounds[second.id]
	near := func(what string, got, want float32) {
		t.Helper()
		if math.Abs(float64(got-want)) > 0.6 {
			t.Errorf("%s %v, want %v", what, got, want)
		}
	}
	near("left", p1.X-(a.sbWidth+1), paneGap)
	near("top", p1.Y-toolbarHeight, paneGap)
	near("between", p2.X-(p1.X+p1.W), paneGap+1) // and the split's line
	if p1.Y != p2.Y || p1.H != p2.H {
		t.Errorf("panes %v and %v not level", p1, p2)
	}
}

// TestBrowserTabs stands the tabs on the title bar's foot and switches
// between them.
func TestBrowserTabs(t *testing.T) {
	a, tt := newTestApp(t)
	for range 2 {
		if _, err := a.open(localID, layout.Terminal, openAt{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	tt.Frame()
	tt.Frame()
	if len(a.ws.Tabs) != 2 || a.ws.Active != 1 {
		t.Fatalf("tabs %d, active %d", len(a.ws.Tabs), a.ws.Active)
	}
	first, ok := tt.Find("Local terminal")
	if !ok {
		t.Fatalf("no tabs in %q", tt.Texts())
	}
	// Centred in a tab that stands on the bar's foot.
	if mid := first.Y + first.H/2; math.Abs(float64(mid-(toolbarHeight-tabHeight/2))) > 1 {
		t.Errorf("tab text at %v, not on the bar's foot", first)
	}
	tt.ClickAt(first.X+first.W/2, first.Y+first.H/2)
	tt.Frame()
	if a.ws.Active != 0 {
		t.Errorf("active %d after a click on the first tab", a.ws.Active)
	}
}

// TestHostCard sets the chosen host into the sidebar on one line, its
// rarer actions in a menu.
func TestHostCard(t *testing.T) {
	a, tt := newTestApp(t)
	h, err := a.Store().Add(assets.Host{Name: "web", Address: "10.0.0.5", User: "root"})
	if err != nil {
		t.Fatal(err)
	}
	a.selected = h.ID
	tt.Frame()
	conn, ok1 := tt.Find("Connect")
	target, ok2 := tt.Find("root@10.0.0.5")
	more, ok3 := tt.Find("More Actions")
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("card misses something: %q", tt.Texts())
	}
	if conn.Y > target.Y+target.H || more.Y > target.Y+target.H {
		t.Errorf("actions under the host: connect %v, more %v, target %v", conn, more, target)
	}
	mustClick(t, tt, "More Actions")
	if err := tt.ChooseMenuItem("Add to Favorites"); err != nil {
		t.Fatalf("%v menu %q", err, tt.Menu())
	}
	tt.Frame()
	if got, _ := a.Store().Get(h.ID); !got.Favorite {
		t.Error("not a favorite")
	}
	mustClick(t, tt, "More Actions")
	if err := tt.ChooseMenuItem("Edit Host"); err != nil {
		t.Fatalf("%v menu %q", err, tt.Menu())
	}
	tt.Frame()
	if !a.editing || a.form == nil || a.form.orig.ID != h.ID {
		t.Error("editor not open")
	}
}
