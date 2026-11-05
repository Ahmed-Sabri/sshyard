package app

import (
	"strings"
	"testing"
)

// Opening Files beside a terminal narrows it, and closing it widens it
// again: a long line wraps at each width and is whole afterwards.
func TestTerminalReflowsBesideFiles(t *testing.T) {
	skipWindowsSFTP(t)
	srv, sh := startScript(t, "$ ")
	srv.SFTPRoot = t.TempDir()
	a, tt := newTestApp(t)
	c := connected(t, a, tt, srv)
	tt.Frame()
	full := c.tv.cols
	long := strings.Repeat("0123456789", (2*full+10)/10+1)[:2*full+5]
	sh.out <- "ps\r\n" + long + "\r\n$ "
	waitFor(t, tt, "the output", func() bool { return strings.Contains(c.s.Term.ScreenText(), long[2*full:]) })
	c.tv.selected = true

	mustClick(t, tt, "Open Files for This Host")
	tt.Frame()
	tab := a.ws.ActiveTab()
	if len(tab.Panes()) != 2 || c.tv.cols >= full {
		t.Fatalf("terminal %d columns beside files, %d alone", c.tv.cols, full)
	}
	if c.tv.selected {
		t.Error("the selection outlived the reflow")
	}
	narrow := c.s.Term.ScreenText()
	if !strings.Contains(narrow, long[:c.tv.cols]+"\n"+long[c.tv.cols:2*c.tv.cols]) {
		t.Errorf("the long line not wrapped at %d columns: %q", c.tv.cols, narrow)
	}

	a.CloseSession(a.focused().id)
	tt.Frame()
	if c.tv.cols != full {
		t.Fatalf("terminal %d columns after closing files, want %d", c.tv.cols, full)
	}
	want := long[:full] + "\n" + long[full:2*full] + "\n" + long[2*full:] + "\n$"
	if got := c.s.Term.ScreenText(); !strings.Contains(got, want) {
		t.Errorf("after closing files %q, want it to hold %q", got, want)
	}
}
