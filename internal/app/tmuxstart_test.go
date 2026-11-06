package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/locales"
)

// startOnce is the command c's shell is to be given once, "" for none.
func startOnce(c *conn) string {
	if c.start == nil {
		return ""
	}
	c.start.mu.Lock()
	defer c.start.mu.Unlock()
	return c.start.once
}

// setStartOnce gives c's shell cmd once.
func setStartOnce(c *conn, cmd string) {
	if c.start == nil {
		c.start = &shellStart{}
	}
	c.start.mu.Lock()
	c.start.once = cmd
	c.start.mu.Unlock()
}

// markOfConn is the tmux session c is in, nil for none.
func markOfConn(c *conn) *tmuxMark {
	c.start.mu.Lock()
	defer c.start.mu.Unlock()
	return c.start.mark
}

// TestTmuxStartCmd: a starting shell attaches to the session it was in,
// the same one by its ID or one of its name, else to its host's.
func TestTmuxStartCmd(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	now := t0.Add(time.Hour)
	info := session.TmuxInfo{Sessions: []session.TmuxSession{
		{ID: "$1", Name: "dev", Created: t0},
		{ID: "$4", Name: "api", Created: t0.Add(time.Minute)},
	}}
	for _, tc := range []struct {
		what string
		err  error
		mark *tmuxMark
		host string
		cmd  string
		keep string // the kept mark's ID, "" for none
		note int
	}{
		{what: "the same session", mark: &tmuxMark{id: "$1", name: "dev", created: t0}, cmd: "tmux attach-session -t '$1'", keep: "$1"},
		{what: "renamed", mark: &tmuxMark{id: "$1", name: "old", created: t0}, host: "main", cmd: "tmux attach-session -t '$1'", keep: "$1"},
		{what: "an ID taken again, the name", mark: &tmuxMark{id: "$1", name: "api", created: t0.Add(-time.Hour)}, cmd: "tmux attach-session -t '$4'", keep: "$4"},
		{what: "an ID with no time", mark: &tmuxMark{id: "$4"}, cmd: "tmux attach-session -t '$4'", keep: "$4"},
		{what: "gone", mark: &tmuxMark{id: "$9", name: "gone"}, note: tmuxNoteGone},
		{what: "gone, the host's", mark: &tmuxMark{id: "$9", name: "gone"}, host: "it's", cmd: `tmux new-session -A -s 'it'\''s'`},
		{what: "the host's", host: "main", cmd: "tmux new-session -A -s 'main'"},
		{what: "no tmux", err: session.ErrNoTmux, host: "main", note: tmuxNoteMissing},
		{what: "a failure, the host's", err: errors.New("timeout"), mark: &tmuxMark{id: "$1"}, host: "main", cmd: "tmux new-session -A -s 'main'", keep: "$1"},
		{what: "a failure", err: errors.New("timeout"), mark: &tmuxMark{id: "$1"}, keep: "$1", note: tmuxNoteFailed},
	} {
		in := info
		if tc.err != nil {
			in = session.TmuxInfo{}
		}
		cmd, keep, note := tmuxStartCmd(in, tc.err, tc.mark, tc.host, now)
		id := ""
		if keep != nil {
			id = keep.id
		}
		if cmd != tc.cmd || id != tc.keep || note != tc.note {
			t.Errorf("%s: %q %q %d", tc.what, cmd, id, note)
		}
	}
}

// TestTmuxSeen: a read naming the terminal's client sets the session it is
// in; one without forgets it once the server tells, or the session is gone,
// but not just after an attach or a connect.
func TestTmuxSeen(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	now := t0.Add(time.Hour)
	dev := session.TmuxSession{ID: "$1", Name: "dev", Created: t0}
	c := &conn{start: &shellStart{}}
	a := &App{}
	a.tmuxSeen(c, session.TmuxInfo{Tells: true, Sessions: []session.TmuxSession{dev},
		Clients: []session.TmuxClient{{TTY: "/dev/pts/1", Session: "$1", Mine: true}}}, now)
	if k := markOfConn(c); k == nil || k.id != "$1" || k.name != "dev" || !k.created.Equal(t0) {
		t.Fatalf("mine: %+v", k)
	}
	none := session.TmuxInfo{Tells: true, Sessions: []session.TmuxSession{dev}}
	a.tmuxSeen(c, none, now.Add(time.Second))
	if markOfConn(c) == nil {
		t.Fatal("forgotten just after the attach")
	}
	c.tmuxUp = now.Add(time.Minute)
	a.tmuxSeen(c, none, now.Add(time.Minute+time.Second))
	if markOfConn(c) == nil {
		t.Fatal("forgotten just after a connect")
	}
	untold := session.TmuxInfo{Sessions: []session.TmuxSession{dev}}
	a.tmuxSeen(c, untold, now.Add(time.Hour))
	if markOfConn(c) == nil {
		t.Fatal("forgotten by a server that cannot tell")
	}
	a.tmuxSeen(c, none, now.Add(time.Hour))
	if markOfConn(c) != nil {
		t.Fatal("kept with no client")
	}
	c.start.setMark(&tmuxMark{id: "$1", name: "dev", created: t0})
	a.tmuxSeen(c, session.TmuxInfo{}, now.Add(2*time.Hour))
	if markOfConn(c) != nil {
		t.Fatal("kept with the session gone")
	}
}

// TestTmuxReattach: after a reconnect the terminal attaches again to the
// session it was in; one gone is said so and forgotten.
func TestTmuxReattach(t *testing.T) {
	x := &tmuxExec{}
	srv := startMetricSrv(t, x.run)
	a, tt := newTestApp(t)
	m := locales.In("en")
	cn := connected(t, a, tt, srv)
	cn.start.setMark(&tmuxMark{id: "$1", name: "dev", created: time.Unix(1700000000, 0)})
	srv.Drop()
	waitFor(t, tt, "the attach", func() bool {
		return cn.s.Status().Connects == 2 && strings.Contains(cn.s.Term.ScreenText(), "you said: tmux attach-session -t '$1'")
	})

	cn.start.setMark(&tmuxMark{id: "$7", name: "old"})
	srv.Drop()
	waitFor(t, tt, "the toast", func() bool { return tt.HasText(m.TmuxStartGone("old")) })
	if markOfConn(cn) != nil {
		t.Error("a gone session kept")
	}
}

// TestTmuxHostAutoAttach: a host's terminals attach to its tmux session,
// except one opened without; a server without tmux says so once.
func TestTmuxHostAutoAttach(t *testing.T) {
	x := &tmuxExec{}
	srv := startMetricSrv(t, x.run)
	a, tt := newTestApp(t)
	m := locales.In("en")
	h := addHost(t, a, srv, "box", "secret")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	h.Tmux = "main"
	if _, err := a.Store().Update(h); err != nil {
		t.Fatal(err)
	}
	a.Connect(h.ID, openAt{})
	cn := a.focused()
	waitFor(t, tt, "the attach", func() bool {
		return strings.Contains(cn.s.Term.ScreenText(), "you said: tmux new-session -A -s 'main'")
	})

	a.ConnectWithoutTmux(h.ID)
	plain := a.focused()
	waitFor(t, tt, "connected", func() bool { return plain.s.Status().State == session.Connected })
	time.Sleep(dirQuietForTest)
	tt.Frame()
	if strings.Contains(plain.s.Term.ScreenText(), "tmux") {
		t.Errorf("attached without tmux: %q", plain.s.Term.ScreenText())
	}

	x.mu.Lock()
	x.missing = true
	x.mu.Unlock()
	a.Connect(h.ID, openAt{})
	a.Connect(h.ID, openAt{})
	warn := m.TmuxStartMissing("box")
	waitFor(t, tt, "the warning", func() bool { return tt.HasText(warn) })
	for _, c := range a.conns {
		c := c
		if c.s != nil {
			waitFor(t, tt, "connected", func() bool { return c.s.Status().State == session.Connected })
		}
	}
	time.Sleep(dirQuietForTest)
	tt.Frame()
	if n := textCount(tt, warn); n != 1 {
		t.Errorf("warned %d times", n)
	}
}

// dirQuietForTest outlasts the wait for a new shell's prompt.
const dirQuietForTest = 800 * time.Millisecond

// TestTmuxHostEditor: the editor keeps a host's tmux session, refusing a
// name tmux would change; the host's menu then connects without it too.
func TestTmuxHostEditor(t *testing.T) {
	srv := startMetricSrv(t, (&tmuxExec{}).run)
	a, tt := newTestApp(t)
	m := locales.In("en")
	h := addHost(t, a, srv, "box", "")
	a.openEditor(&h)
	tt.Frame()
	if !tt.HasText(m.TmuxAutoAttach()) || !tt.HasText(m.TmuxAutoAttachHint()) {
		t.Fatalf("no field: %q", tt.Texts())
	}
	a.form.tmux = "a:b"
	if a.SaveForm(m) || a.form.err != m.TmuxNameInvalid() {
		t.Fatalf("a bad name saved: %q", a.form.err)
	}
	a.form.tmux = " work "
	if !a.SaveForm(m) {
		t.Fatal(a.form.err)
	}
	h, _ = a.Store().Get(h.ID)
	if h.Tmux != "work" {
		t.Fatalf("saved %q", h.Tmux)
	}
	labels := func() []string {
		var ls []string
		hh, _ := a.Store().Get(h.ID)
		for _, e := range a.hostEntries(m, hh) {
			ls = append(ls, e.Label)
		}
		return ls
	}
	if !strings.Contains(strings.Join(labels(), "|"), m.ConnectWithoutTmux()) {
		t.Errorf("menu %q", labels())
	}
	h.Tmux = ""
	a.Store().Update(h)
	if strings.Contains(strings.Join(labels(), "|"), m.ConnectWithoutTmux()) {
		t.Errorf("menu without a session %q", labels())
	}
}
