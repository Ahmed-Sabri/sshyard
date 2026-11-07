package app

import (
	"errors"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/egoist/mygo/ui"
)

// broadcastPair connects two terminals side by side in a tab, the first
// in bracketed paste mode, and a third alone in a second tab, which is
// active.
func broadcastPair(t *testing.T) (*App, *ui.Tester, [3]*conn, [3]*scriptShell) {
	t.Helper()
	a, tt := newTestApp(t)
	var cs [3]*conn
	var shells [3]*scriptShell
	for i, first := range []string{"\x1b[?2004hready", "ready", "ready"} {
		srv, sh := startScript(t, first)
		h := addHost(t, a, srv, string(rune('a'+i)), "secret")
		a.known.Add(srv.Addr, srv.HostKey.PublicKey())
		at := openAt{}
		if i == 1 {
			at = openAt{pane: a.ws.ActiveTab().Focus, edge: layout.Right}
		}
		if _, err := a.Connect(h.ID, at); err != nil {
			t.Fatal(err)
		}
		cs[i], shells[i] = a.focused(), sh
	}
	for _, c := range cs {
		waitFor(t, tt, "connected", func() bool { return c.s.Status().State == session.Connected })
	}
	waitFor(t, tt, "bracketed paste", func() bool { return cs[0].s.Term.Modes().BracketedPaste })
	for _, sh := range shells {
		sh.take()
	}
	return a, tt, cs, shells
}

// readExactly waits for a shell to read want, and nothing else.
func readExactly(t *testing.T, tt *ui.Tester, sh *scriptShell, want string) {
	t.Helper()
	var got string
	waitFor(t, tt, "the input "+want, func() bool {
		got += sh.take()
		return len(got) >= len(want)
	})
	if got != want {
		t.Fatalf("read %q, want %q", got, want)
	}
}

// waitBroadcast runs frames until the writes of a send are done.
func waitBroadcast(t *testing.T, a *App, tt *ui.Tester) {
	t.Helper()
	waitFor(t, tt, "broadcast writes", func() bool { return a.bc.sending == 0 })
}

func TestBroadcastTargets(t *testing.T) {
	a, _, cs, _ := broadcastPair(t)
	ids := func(cs []*conn) []int {
		var out []int
		for _, c := range cs {
			out = append(out, c.id)
		}
		return out
	}
	// The active tab's terminals, then every tab's.
	if got := ids(a.broadcastTargets()); len(got) != 1 || got[0] != cs[2].id {
		t.Fatalf("active tab targets %v", got)
	}
	a.ws.Activate(cs[0].id)
	if got := ids(a.broadcastTargets()); len(got) != 2 || got[0] != cs[0].id || got[1] != cs[1].id {
		t.Fatalf("first tab targets %v", got)
	}
	a.bc.all = true
	a.bc.left = map[int]bool{cs[1].id: true}
	if got := ids(a.broadcastTargets()); len(got) != 2 || got[0] != cs[0].id || got[1] != cs[2].id {
		t.Fatalf("all tabs less one %v", got)
	}
	if len(a.broadcastPanes()) != 3 {
		t.Fatal("a pane left out is no longer listed")
	}
	// Files and AI panes are never targets.
	a.bc.left = nil
	cs[1].kind = layout.Files
	if got := ids(a.broadcastTargets()); len(got) != 2 {
		t.Fatalf("files pane among the targets: %v", got)
	}
	cs[1].kind = layout.Terminal
}

func TestBroadcastEncodesForEachTerminal(t *testing.T) {
	a, tt, cs, shells := broadcastPair(t)
	a.ws.Activate(cs[0].id)
	cs[1].tv.offset, cs[1].tv.selected = 4, true
	if err := a.sendBroadcast("echo 中文", true); err != nil {
		t.Fatal(err)
	}
	waitBroadcast(t, a, tt)
	readExactly(t, tt, shells[0], "\x1b[200~echo 中文\x1b[201~\r")
	readExactly(t, tt, shells[1], "echo 中文\r")
	if got := shells[2].take(); got != "" {
		t.Fatalf("a terminal of another tab got %q", got)
	}
	if a.bc.sent[cs[0].id] != nil || a.bc.sent[cs[1].id] != nil || len(a.bc.sent) != 2 {
		t.Fatalf("results %v", a.bc.sent)
	}
	if cs[1].tv.offset != 0 || cs[1].tv.selected {
		t.Fatal("a terminal sent to kept its scroll and selection")
	}

	// Inserted, not run: a tab is only safe in bracketed paste. The
	// terminal unable to take it is skipped; the other still gets it.
	if err := a.sendBroadcast("a\tb", false); err != nil {
		t.Fatal(err)
	}
	waitBroadcast(t, a, tt)
	readExactly(t, tt, shells[0], "\x1b[200~a\tb\x1b[201~")
	if !errors.Is(a.bc.sent[cs[1].id], errSnippetUnsafePaste) {
		t.Fatalf("unsafe insert result %v", a.bc.sent[cs[1].id])
	}
	if got := shells[1].take(); got != "" {
		t.Fatalf("unsafe insert sent %q", got)
	}
}

func TestBroadcastRefusals(t *testing.T) {
	a, tt, cs, shells := broadcastPair(t)
	a.ws.Activate(cs[0].id)
	var field *assets.FieldError
	if err := a.sendBroadcast("  ", true); !errors.As(err, &field) {
		t.Fatalf("empty command: %v", err)
	}
	if err := a.sendBroadcast("echo\x03", true); !errors.As(err, &field) {
		t.Fatalf("control character: %v", err)
	}
	a.bc.sending = 1
	if err := a.sendBroadcast("echo", true); !errors.Is(err, errSnippetBusy) {
		t.Fatalf("send while sending: %v", err)
	}
	a.bc.sending = 0
	a.bc.left = map[int]bool{cs[0].id: true, cs[1].id: true}
	if err := a.sendBroadcast("echo", true); !errors.Is(err, errBroadcastNoTarget) {
		t.Fatalf("send with every terminal left out: %v", err)
	}
	a.bc.left = nil

	// A terminal disconnected is skipped and said so.
	cs[1].s.Close()
	waitFor(t, tt, "closed", func() bool { return cs[1].s.Status().State != session.Connected })
	if err := a.sendBroadcast("uptime", true); err != nil {
		t.Fatal(err)
	}
	waitBroadcast(t, a, tt)
	readExactly(t, tt, shells[0], "\x1b[200~uptime\x1b[201~\r")
	if !errors.Is(a.bc.sent[cs[1].id], errSnippetDisconnected) {
		t.Fatalf("disconnected result %v", a.bc.sent[cs[1].id])
	}
}

func TestBroadcastAsksBeforeSeveralLines(t *testing.T) {
	a, tt, cs, shells := broadcastPair(t)
	a.ws.Activate(cs[0].id)
	a.bc.draft = "cd /tmp\nls\n"
	a.submitBroadcast(false)
	if !a.bc.confirm || a.bc.sent != nil {
		t.Fatalf("several lines sent without asking: confirm %v, sent %v", a.bc.confirm, a.bc.sent)
	}
	a.bc.confirm = false
	a.submitBroadcast(true)
	waitBroadcast(t, a, tt)
	readExactly(t, tt, shells[1], "cd /tmp\rls\r")

	// One line, its line end aside, goes at once.
	a.bc.draft = "pwd\r\n"
	a.submitBroadcast(false)
	if a.bc.confirm {
		t.Fatal("asked before one line")
	}
	waitBroadcast(t, a, tt)
	readExactly(t, tt, shells[1], "pwd\r")
}
