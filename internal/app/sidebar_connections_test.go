package app

import (
	"testing"

	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestSidebarLiveConnections(t *testing.T) {
	srv := startServer(t)
	a, tt := newTestApp(t)
	assertSidebarConnections(t, a, 0)
	if _, err := a.OpenLocalTerminal(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "local shell", func() bool { return a.focused().s.Status().State == session.Connected })
	assertSidebarConnections(t, a, 0)
	h := addHost(t, a, srv, "box", "secret")
	a.Connect(h.ID, openAt{})
	waitFor(t, tt, "trust prompt", func() bool { return tt.HasText("Unknown Host Key") })
	mustClick(t, tt, "Trust and Connect")
	waitFor(t, tt, "connected", func() bool { return a.focused().s.Client() != nil })
	first := a.focused()
	assertSidebarConnections(t, a, 1)
	a.SplitFocused(layout.Right)
	tt.Frame()
	waitFor(t, tt, "shared connection", func() bool { return a.focused().s.Client() != nil })
	if first.s.Client() != a.focused().s.Client() {
		t.Fatal("split did not share the SSH client")
	}
	assertSidebarConnections(t, a, 1)
	a.CloseFocused()
	tt.Frame()
	assertSidebarConnections(t, a, 1)
	a.CloseFocused()
	tt.Frame()
	assertSidebarConnections(t, a, 0)
}

func assertSidebarConnections(t *testing.T, a *App, want int) {
	t.Helper()
	if got := a.connectedCount(); got != want {
		t.Fatalf("connections = %d, want %d", got, want)
	}
	for _, dark := range []bool{false, true} {
		tt := ui.NewTester(func(c *ui.Context) { a.sidebarView(c, locales.In("en"), currentTokens(c)) }, 240, 500)
		tt.SetDark(dark)
		tt.Frame()
		r := mustFind(t, tt, locales.In("en").ConnectionCount(want))
		tok := theme.For(theme.Auto, dark)
		color := tok.TextMuted
		if want > 0 {
			color = tok.Success
		}
		p := tt.Image().RGBAAt(int(r.X-7), int(r.Y+r.H/2))
		if !nearColor(p.R, p.G, p.B, color.R, color.G, color.B) {
			t.Fatalf("status dot = %v, want %v", p, color)
		}
	}
}
