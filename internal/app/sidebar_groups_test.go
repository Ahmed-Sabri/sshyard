package app

import (
	"fmt"
	"slices"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func addGroupedHost(t *testing.T, a *App, name, group string) assets.Host {
	t.Helper()
	h, err := a.store.Add(assets.Host{Name: name, Address: name, User: "u", Group: group})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func assertGroupPositions(t *testing.T, tt *ui.Tester, names ...string) {
	t.Helper()
	last := float32(-1)
	for _, name := range names {
		r := mustFind(t, tt, name)
		if r.Y <= last {
			t.Fatalf("group %s at %v, after y=%v", name, r, last)
		}
		last = r.Y
	}
}

// Begin at a real label, move through several pointer frames, then let callers
// inspect the preview, release, or cancel with Escape.
func dragSidebarTo(t *testing.T, tt *ui.Tester, from string, x, y float32) {
	t.Helper()
	r := mustFind(t, tt, from)
	sx, sy := r.X+r.W/2, r.Y+r.H/2
	tt.Press(sx, sy)
	for i := 1; i <= 8; i++ {
		f := float32(i) / 8
		tt.Move(sx+(x-sx)*f, sy+(y-sy)*f)
		tt.Frame()
	}
}

func TestSidebarGroupDragDrop(t *testing.T) {
	a, tt := newTestApp(t)
	addGroupedHost(t, a, "alpha", "A")
	addGroupedHost(t, a, "bravo", "B")
	addGroupedHost(t, a, "charlie", "C")
	tt.Frame()
	assertGroupPositions(t, tt, "A", "B", "C")
	dst := mustFind(t, tt, "A")
	x, y := dst.X+8, dst.Y+1
	dragSidebarTo(t, tt, "C", x, y)
	assertGroupInsertionLine(t, tt, dst, true)
	saveBrandScreenshot(t, tt, "group-drag-preview")
	tt.Release(x, y)
	tt.Frame()
	if got := a.store.Groups(); !slices.Equal(got, []string{"C", "A", "B"}) {
		t.Fatalf("drop: %q", got)
	}
	assertGroupPositions(t, tt, "C", "A", "B")
	if !tt.HasText("charlie") {
		t.Fatal("dragging a header also collapsed it")
	}

	// A click remains a collapse, and collapsed groups still drag to either end.
	mustClick(t, tt, "C")
	if tt.HasText("charlie") {
		t.Fatal("header click did not collapse")
	}
	mustClick(t, tt, "B")
	dst = mustFind(t, tt, "B")
	x, y = dst.X+8, dst.Y+dst.H-1
	dragSidebarTo(t, tt, "C", x, y)
	tt.Release(x, y)
	tt.Frame()
	assertGroupPositions(t, tt, "A", "B", "C")
	assertGroupInsertionLine(t, tt, dst, false)
	if tt.HasText("charlie") {
		t.Fatal("move lost collapsed state")
	}

	dst = mustFind(t, tt, "A")
	x, y = dst.X+8, dst.Y+1
	dragSidebarTo(t, tt, "C", x, y)
	tt.Key(0, ui.KeyEscape)
	tt.Release(x, y)
	tt.Frame()
	assertGroupPositions(t, tt, "A", "B", "C")

	// Hosts retain their own drag value inside a draggable group.
	assertGroupInsertionLine(t, tt, dst, false)
	dst = mustFind(t, tt, "B")
	x, y = dst.X+8, dst.Y+dst.H/2
	dragSidebarTo(t, tt, "alpha", x, y)
	tt.Release(x, y)
	tt.Frame()
	if len(a.store.InGroup("B")) != 2 {
		t.Fatal("host drop did not move the host")
	}
	if !slices.Equal(a.store.Groups(), []string{"B", "C"}) {
		t.Fatal("host drag reordered groups or preserved implicit A")
	}
}

func assertGroupInsertionLine(t *testing.T, tt *ui.Tester, title ui.Rect, want bool) {
	t.Helper()
	accent := theme.Brand
	img := tt.Image()
	found := false
	for y := int(title.Y) - 12; y < int(title.Y+title.H)+8; y++ {
		pixels := 0
		for x := 12; x < 228; x++ {
			p := img.RGBAAt(x, y)
			if nearColor(p.R, p.G, p.B, accent.R, accent.G, accent.B) {
				pixels++
			}
		}
		found = found || pixels > 70
	}
	if found != want {
		t.Fatalf("insertion line visible=%v, want %v", found, want)
	}
}

func TestSidebarGroupSearchOrder(t *testing.T) {
	a, tt := newTestApp(t)
	addGroupedHost(t, a, "match-a", "A")
	addGroupedHost(t, a, "hidden", "B")
	addGroupedHost(t, a, "match-c", "C")
	if err := a.store.AddGroup("Empty"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.MoveGroup("C", "A", false); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// Use input events so the bound search field owns the value.
	mustClick(t, tt, "Search hosts")
	tt.Type("match")
	tt.Frame()
	assertGroupPositions(t, tt, "C", "A")
	if tt.HasText("B") || tt.HasText("Empty") {
		t.Fatal("search included unmatched groups")
	}
	dst := mustFind(t, tt, "C")
	x, y := dst.X+8, dst.Y+1
	dragSidebarTo(t, tt, "A", x, y)
	tt.Release(x, y)
	tt.Frame()
	if !slices.Equal(a.store.Groups(), []string{"C", "A", "B", "Empty"}) {
		t.Fatal("search drag changed the full order")
	}
}

func TestSidebarSectionsToggle(t *testing.T) {
	a, tt := newTestApp(t)
	h := addGroupedHost(t, a, "alpha", "A")
	addGroupedHost(t, a, "bravo", "B")
	addGroupedHost(t, a, "ungrouped-host", "")
	if err := a.store.SetFavorite(h.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.AddForward(assets.Forward{Name: "forward", HostID: h.ID, Kind: assets.ForwardLocal, ListenPort: 32123, TargetAddr: "localhost", TargetPort: 22}); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	mustClick(t, tt, "Collapse all groups")
	for _, key := range []string{"g:A", "g:B", "fav", "none", "fwd"} {
		if *a.section(key) {
			t.Fatalf("section %s was not collapsed", key)
		}
	}
	if tt.HasText("alpha") || tt.HasText("bravo") || tt.HasText("ungrouped-host") {
		t.Fatal("collapsed hosts still visible")
	}
	mustClick(t, tt, "Expand all groups")
	if !tt.HasText("alpha") || !tt.HasText("bravo") || !tt.HasText("ungrouped-host") {
		t.Fatal("expand did not show hosts")
	}
	mustClick(t, tt, "A") // A mixed state must still offer collapse.
	if !tt.HasText("Collapse all groups") {
		t.Fatal("mixed state offers the wrong action")
	}
	// Search hides B and none; the action must still update their states.
	mustClick(t, tt, "Search hosts")
	tt.Type("alpha")
	tt.Frame()
	mustClick(t, tt, "Collapse all groups")
	mustClick(t, tt, "Clear search")
	if tt.HasText("bravo") || tt.HasText("ungrouped-host") {
		t.Fatal("hidden sections were left open")
	}
	mustClick(t, tt, "Expand all groups")
	for _, key := range a.sidebarSectionKeys() {
		if !*a.section(key) {
			t.Fatalf("section %s stayed closed", key)
		}
	}
}

func TestSidebarGroupToolsLayout(t *testing.T) {
	a, _ := newTestApp(t)
	if err := a.store.AddGroup("Empty"); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "zh-Hans", "zh-Hant", "ko", "ja"} {
		for _, dark := range []bool{false, true} {
			for _, width := range []int{160, 240} {
				t.Run(fmt.Sprintf("%s/dark=%v/width=%d", lang, dark, width), func(t *testing.T) {
					a.ClearSearch()
					tt := ui.NewTester(func(c *ui.Context) { a.sidebarView(c, locales.In(c.Locale()), currentTokens(c)) }, width, 400)
					tt.SetLocale(lang)
					tt.SetDark(dark)
					*a.section("g:Empty") = true
					tt.Frame()
					m := locales.In(lang)
					search := mustFind(t, tt, m.SearchHosts())
					toggle := mustFind(t, tt, m.CollapseAllGroups())
					add := mustFind(t, tt, m.AddMenu())
					if search.W <= 0 || search.X+search.W > toggle.X || toggle.X+toggle.W > add.X || add.X+add.W > float32(width) {
						t.Fatalf("overlapping tools: search=%v toggle=%v add=%v", search, toggle, add)
					}
					if !tt.HasText("Empty") {
						t.Fatal("a store with only empty groups hides its groups")
					}
					mustClick(t, tt, m.CollapseAllGroups())
					if !tt.HasText(m.ExpandAllGroups()) {
						t.Fatal("button did not switch action")
					}
					mustClick(t, tt, m.SearchHosts())
					tt.Type("Empty")
					tt.Frame()
					clear := mustFind(t, tt, m.ClearSearch())
					toggle = mustFind(t, tt, m.ExpandAllGroups())
					if clear.X+clear.W > toggle.X {
						t.Fatalf("search clear button %v overlaps toggle %v", clear, toggle)
					}
					saveBrandScreenshot(t, tt, fmt.Sprintf("group-tools-%s-dark-%v-width-%d", lang, dark, width))
				})
			}
		}
	}
}
