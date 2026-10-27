package app

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/egoist/mygo/ui"
)

// TestTitleBar runs one band across the window: no name over the
// sidebar; at the end the app's buttons, updates, GitHub and the
// settings, in that order; while the settings show, no tabs, and the
// settings button closes them; the window's own buttons, last, where the
// app draws them.
func TestTitleBar(t *testing.T) {
	a, tt := newTestApp(t)
	band := float32(toolbarHeight)
	in := func(s string) bool {
		r, ok := tt.Find(s)
		return ok && r.Y+r.H <= band
	}
	if in("SSHYard") {
		t.Errorf("app name in the title bar")
	}
	prev := ui.Rect{X: 1200 - 160}
	for _, s := range []string{"Check for Updates", "GitHub Repository", "Settings"} {
		r, ok := tt.Find(s)
		if !ok || !in(s) || r.X < prev.X+prev.W {
			t.Errorf("%s at %v, not after %v at the end", s, r, prev)
		}
		prev = r
	}
	if tt.HasText("Split Right") {
		t.Error("split buttons in the title bar")
	}
	mustClick(t, tt, "GitHub Repository")
	if got := tt.OpenedURLs(); !slices.Equal(got, []string{repoURL}) {
		t.Errorf("GitHub opened %q", got)
	}
	mustClick(t, tt, "Check for Updates")
	if a.page != pageSettings || a.prefNow() != prefAbout {
		t.Errorf("updates show %v %v, not the about page", a.page, a.prefNow())
	}
	if !in("Settings") {
		t.Error("no settings button in the settings")
	}
	mustClick(t, tt, "Settings")
	if a.page == pageSettings {
		t.Error("the settings button did not close them")
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

	if _, err := a.open(localID, layout.Terminal, openAt{}, nil); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !in("Local terminal") {
		t.Fatalf("no tab in the title bar: %q", tt.Texts())
	}
	a.openSettings(a.prefNow())
	tt.Frame()
	if in("Local terminal") || in("Close Tab") {
		t.Errorf("tabs in the title bar over the settings: %q", tt.Texts())
	}
	// Just before the window's buttons.
	if st, cl := mustFind(t, tt, "Settings"), mustFind(t, tt, "Minimize"); st.X > cl.X {
		t.Errorf("settings at %v, after the window's buttons at %v", st, cl)
	}
	mustClick(t, tt, "Settings")
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

// TestTabs centres the tabs' capsule in the title bar, shows a tab's
// close button only while it is active or under the pointer, and switches
// between them.
func TestTabs(t *testing.T) {
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
	// Centred in the tab, which stands on the band's foot.
	if mid := first.Y + first.H/2; math.Abs(float64(mid-(toolbarHeight-tabHeight/2))) > 1 {
		t.Errorf("tab text at %v, not centred in the tab", first)
	}
	// One close button, the active tab's; the other's with the pointer.
	closes := func() (n int) {
		for _, s := range tt.Texts() {
			if s == "Close Tab" {
				n++
			}
		}
		return n
	}
	if n := closes(); n != 1 {
		t.Errorf("%d close buttons at rest, want the active tab's", n)
	}
	tt.Move(first.X+first.W/2, first.Y+first.H/2)
	tt.Frame()
	tt.Frame()
	if n := closes(); n != 2 {
		t.Errorf("%d close buttons with the pointer on a tab, want 2", n)
	}
	tt.ClickAt(first.X+first.W/2, first.Y+first.H/2)
	tt.Frame()
	if a.ws.Active != 0 {
		t.Errorf("active %d after a click on the first tab", a.ws.Active)
	}
}

// TestHostActions puts what to do with a host on its sidebar row, shown
// while it is chosen or under the pointer, its rarer actions in a menu,
// and no card for it under the hosts.
func TestHostActions(t *testing.T) {
	a, tt := newTestApp(t)
	h, err := a.Store().Add(assets.Host{Name: "web", Address: "10.0.0.5", User: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store().Add(assets.Host{Name: "db", Address: "10.0.0.6", User: "root"}); err != nil {
		t.Fatal(err)
	}
	count := func(s string) (n int) {
		for _, x := range tt.Texts() {
			if x == s {
				n++
			}
		}
		return n
	}
	tt.Move(600, 700) // over nothing
	tt.Frame()
	if n := count("More Actions"); n != 0 {
		t.Errorf("%d hosts show actions with none chosen", n)
	}
	a.selected = h.ID
	tt.Frame()
	name, ok1 := tt.Find("web")
	files, ok2 := tt.Find("Open Files")
	more, ok3 := tt.Find("More Actions")
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("row misses something: %q", tt.Texts())
	}
	if hasExact(tt, "Connect") {
		t.Error("a connect button on the row: a double click connects")
	}
	mid := func(r ui.Rect) float32 { return r.Y + r.H/2 }
	if math.Abs(float64(mid(files)-mid(name))) > 2 || math.Abs(float64(mid(more)-mid(name))) > 2 || files.X < name.X {
		t.Errorf("actions not on the host's row: name %v, files %v, more %v", name, files, more)
	}
	if more.X+more.W > a.sbWidth {
		t.Errorf("more at %v, past the sidebar", more)
	}
	if tt.HasText("root@10.0.0.5") {
		t.Error("a card for the chosen host")
	}
	if n := count("More Actions"); n != 1 {
		t.Errorf("%d hosts show actions, want the chosen one", n)
	}
	// The other with the pointer on it.
	db, _ := tt.Find("db")
	tt.Move(db.X+db.W/2, db.Y+db.H/2)
	tt.Frame()
	tt.Frame()
	if n := count("More Actions"); n != 2 {
		t.Errorf("%d hosts show actions, want the chosen and the hovered", n)
	}

	tt.Move(600, 700)
	tt.Frame()
	tt.Frame()
	mustClick(t, tt, "More Actions")
	mustClick(t, tt, "Favorite")
	tt.Frame()
	if got, _ := a.Store().Get(h.ID); !got.Favorite {
		t.Error("not a favorite")
	}
	// A favorite is listed twice; the chosen row is the favorites' one.
	a.selected = "fav:" + h.ID
	tt.Frame()
	mustClick(t, tt, "More Actions")
	mustClick(t, tt, "Edit")
	tt.Frame()
	if !a.editing || a.form == nil || a.form.orig.ID != h.ID {
		t.Error("editor not open")
	}
}

// TestTabsOverflow shows as many tabs as fit, the active one among them,
// and the others in the menu of a button after them; none runs under the
// actions beside the tabs.
func TestTabsOverflow(t *testing.T) {
	a, tt := newTestApp(t)
	for range 12 {
		if _, err := a.open(localID, layout.Terminal, openAt{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	tt.Frame()
	tt.Frame()
	tt.Frame()
	if !tt.HasText("more tabs") {
		t.Fatalf("no button for the tabs that do not fit: %q", tt.Texts())
	}
	add, ok := tt.Find("New Tab")
	if !ok {
		t.Fatalf("no actions: %q", tt.Texts())
	}
	shown := 0
	for _, s := range tt.Texts() {
		if s == "Local terminal" {
			shown++
		}
	}
	if shown == 0 || shown >= 12+1 { // and the pane's header
		t.Fatalf("%d tabs shown", shown)
	}
	more, _ := tt.Find(strconv.Itoa(12-a.tabRoomShown()) + " more tabs")
	if more.X+more.W > add.X {
		t.Errorf("more tabs at %v, under the actions at %v", more, add)
	}
	if a.ws.Active != 11 || a.tabStart+a.tabRoomShown() != 12 {
		t.Errorf("active %d not shown: from %d", a.ws.Active, a.tabStart)
	}
	before := textCount(tt, "Local terminal")
	if err := tt.Click(strconv.Itoa(12-a.tabRoomShown()) + " more tabs"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// The popover, not a native menu: a row for each tab not shown.
	if !a.tabsOpen || menuOf(a) != nil {
		t.Fatalf("no popover: open %v, menu %q", a.tabsOpen, menuOf(a))
	}
	if n := textCount(tt, "Local terminal") - before; n < 12-a.tabRoomShown() {
		t.Fatalf("%d rows for %d tabs: %q", n, 12-a.tabRoomShown(), tt.Texts())
	}
	// Its first row, under the button and level with its end.
	tt.ClickAt(more.X+more.W-moreTabWidth/2, more.Y+more.H+6+5+18)
	tt.Frame()
	if a.tabsOpen {
		t.Error("the popover stayed open")
	}
	if a.ws.Active != 0 || a.tabStart != 0 {
		t.Errorf("chose the first: active %d, from %d", a.ws.Active, a.tabStart)
	}
}

// tabRoomShown is how many tabs the title bar shows now.
func (a *App) tabRoomShown() int {
	_, k := tabsShown(len(a.ws.Tabs), a.ws.Active, a.tabStart, a.tabRoom)
	return k
}

func TestTabsShown(t *testing.T) {
	room := func(n int) float32 { return 2*tabInset + float32(n)*(tabMinWidth+tabGap) - tabGap }
	for _, c := range []struct {
		n, active, start int
		room             float32
		wantStart, wantK int
	}{
		{5, 4, 0, 0, 0, 5},                      // not measured: all
		{5, 4, 0, room(5), 0, 5},                // all fit
		{10, 0, 0, room(5), 0, 4},               // 4, and the button
		{10, 9, 0, room(5), 6, 4},               // the active one last
		{10, 7, 6, room(5), 6, 4},               // kept where it was
		{10, 2, 6, room(5), 2, 4},               // back to the active one
		{10, 9, 0, 10, 9, 1},                    // at least one
		{3, 1, 7, room(2) + tabMoreWidth, 1, 2}, // a start past the end
	} {
		s, k := tabsShown(c.n, c.active, c.start, c.room)
		if s != c.wantStart || k != c.wantK {
			t.Errorf("%+v: %d, %d", c, s, k)
		}
	}
}

// TestTabTipAndMenu shows a tab's tip at once as the pointer comes on
// it, and closes tabs round one, on its host and on others, from its
// menu.
func TestTabTipAndMenu(t *testing.T) {
	a, tt := newTestApp(t)
	h, err := a.Store().Add(assets.Host{Name: "web", Address: "127.0.0.1", Port: 1, User: "root", Auth: assets.AuthAgent})
	if err != nil {
		t.Fatal(err)
	}
	open := func(id string, n int) {
		t.Helper()
		for range n {
			if _, err := a.open(id, layout.Terminal, openAt{}, nil); err != nil {
				t.Fatal(err)
			}
		}
		tt.Frame()
		tt.Frame()
	}
	hosts := func() (s string) {
		for _, tb := range a.ws.Tabs {
			if a.tabHost(tb) == h.ID {
				s += "W"
			} else {
				s += "L"
			}
		}
		return s
	}
	menu := func(label, item string) {
		t.Helper()
		if err := tt.RightClick(label); err != nil {
			t.Fatal(err)
		}
		if err := chooseMenu(a, tt, item); err != nil {
			t.Fatalf("%v: %q", err, menuOf(a))
		}
		tt.Frame()
		tt.Frame()
	}
	open(localID, 3)
	open(h.ID, 2)
	if hosts() != "LLLWW" {
		t.Fatalf("tabs %s", hosts())
	}

	// The tip, at once: the host's target, as the pane's header shows it.
	targets := func() (n int) {
		for _, s := range tt.Texts() {
			if strings.Contains(s, "root@127.0.0.1") {
				n++
			}
		}
		return n
	}
	before := targets()
	w, _ := tt.Find("web")
	tt.Move(w.X+w.W/2, w.Y+w.H/2)
	tt.Frame()
	if targets() != before+1 {
		t.Errorf("no tip at once: %q", tt.Texts())
	}
	tt.Move(600, 700)
	tt.Frame()

	if err := tt.RightClick("web"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Close Tab", "Close Other Tabs", "Close Tabs to the Left", "Close Tabs to the Right",
		"Close All Tabs of This Host", "Close Tabs of Other Hosts", "Close All Tabs"} {
		if !slices.Contains(menuOf(a), want) {
			t.Errorf("no %q in %q", want, menuOf(a))
		}
	}
	if err := chooseMenu(a, tt, "Close Tabs of Other Hosts"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if hosts() != "WW" {
		t.Fatalf("other hosts closed: %s", hosts())
	}
	open(localID, 2) // WWLL
	menu("web", "Close Tabs to the Right")
	if hosts() != "W" {
		t.Fatalf("right of the first closed: %s", hosts())
	}
	open(localID, 2) // WLL
	open(h.ID, 1)    // WLLW
	menu("Local terminal", "Close All Tabs of This Host")
	if hosts() != "WW" {
		t.Fatalf("the host's closed: %s", hosts())
	}
	open(localID, 1) // WWL
	menu("Local terminal", "Close Tabs to the Left")
	if hosts() != "L" {
		t.Fatalf("left closed: %s", hosts())
	}
	open(localID, 2) // LLL
	menu("Local terminal", "Close Other Tabs")
	if len(a.ws.Tabs) != 1 {
		t.Fatalf("others closed: %s", hosts())
	}
	open(h.ID, 1)
	menu("web", "Close All Tabs")
	if len(a.ws.Tabs) != 0 {
		t.Fatalf("all closed: %s", hosts())
	}
}

// TestNewTabButton puts the "+" right after the tabs, in the band; it
// opens a drop-down, never a tab of the focused session's host at once:
// a new connection or a saved host, each host with its tile.
func TestNewTabButton(t *testing.T) {
	a, tt := newTestApp(t)
	if _, err := a.Store().Add(assets.Host{Name: "web", Address: "10.0.0.5", User: "root"}); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("New Tab") {
		t.Fatal("a + with no tab")
	}
	if _, err := a.OpenLocalTerminal(); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	cl := mustFind(t, tt, "Close Tab")
	add := mustFind(t, tt, "New Tab")
	if add.Y+add.H > toolbarHeight || add.X < cl.X+cl.W || add.X > cl.X+cl.W+40 {
		t.Fatalf("new tab at %v, not right after the tab's close at %v", add, cl)
	}
	mustClick(t, tt, "New Tab")
	if len(a.ws.Tabs) != 1 || a.menu.key != "newtab" {
		t.Fatalf("%d tabs, menu %q: the + did not open its menu", len(a.ws.Tabs), a.menu.key)
	}
	for _, s := range []string{"Quick Connect…", "New Host"} {
		if r := mustFind(t, tt, s); r.Y < add.Y+add.H {
			t.Errorf("%q at %v, not in the menu", s, r)
		}
	}
	if n := textCount(tt, "web"); n != 2 {
		t.Errorf("web shows %d times, want in the sidebar and the menu", n)
	}
	mustClick(t, tt, "Quick Connect…")
	tt.Frame()
	if a.quick == nil || a.menu.key != "" || len(a.ws.Tabs) != 1 {
		t.Fatalf("quick %v, menu %q, %d tabs", a.quick != nil, a.menu.key, len(a.ws.Tabs))
	}
}

// TestHostDot marks a sidebar host's tile with how its sessions are, on
// its top right corner, and with no session, not at all.
func TestHostDot(t *testing.T) {
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "box", "secret")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	tt.Frame()
	if hasExact(tt, "Connected") {
		t.Fatal("a dot with no session")
	}
	id, err := a.Connect(h.ID, openAt{})
	if err != nil {
		t.Fatal(err)
	}
	waitConnected(t, a, tt, id)
	tt.Frame()
	// The first "box" is the tab's; the tile starts the sidebar's row.
	d := mustFind(t, tt, "Connected")
	if d.Y < toolbarHeight || d.X > 60 {
		t.Errorf("dot at %v, not on the corner of the sidebar's tile", d)
	}
}
