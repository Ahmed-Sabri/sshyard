package app

import (
	"slices"
	"testing"

	"github.com/Licoy/sshyard/internal/layout"
)

// TestTabDragReorders: a tab dropped on another's first half goes before
// it, on its second half after it; the shown tab stays shown.
func TestTabDragReorders(t *testing.T) {
	a, tt := newTestApp(t)
	for _, name := range []string{"one", "two", "three"} {
		if _, err := a.open(localID, layout.Terminal, openAt{}, nil); err != nil {
			t.Fatal(err)
		}
		a.RenameTab(a.ws.ActiveTab().ID, name)
	}
	tt.Frame()
	tt.Frame()
	order := func() []string {
		var out []string
		for _, tab := range a.ws.Tabs {
			out = append(out, tab.Title)
		}
		return out
	}
	drop := func(from, onto string, after bool) {
		t.Helper()
		r := mustFind(t, tt, onto)
		x := r.X + 2
		if after {
			x = r.X + r.W + 40
		}
		dragSidebarTo(t, tt, from, x, r.Y+r.H/2)
		tt.Release(x, r.Y+r.H/2)
		tt.Frame()
		tt.Frame()
	}

	drop("three", "one", false)
	if got := order(); !slices.Equal(got, []string{"three", "one", "two"}) {
		t.Fatalf("before: %q", got)
	}
	if a.ws.ActiveTab().Title != "three" {
		t.Errorf("shown tab %q", a.ws.ActiveTab().Title)
	}
	drop("three", "two", true)
	if got := order(); !slices.Equal(got, []string{"one", "two", "three"}) {
		t.Fatalf("after: %q", got)
	}
	if a.ws.ActiveTab().Title != "three" || len(a.ws.Tabs[2].Panes()) != 1 {
		t.Errorf("shown tab %q, panes %d", a.ws.ActiveTab().Title, len(a.ws.Tabs[2].Panes()))
	}
}
