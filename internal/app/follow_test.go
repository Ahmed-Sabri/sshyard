package app

import (
	"io"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/sftpsvc"
	"github.com/Licoy/sshyard/internal/sshtest"
	"golang.org/x/crypto/ssh"
)

// terminalBeside opens a terminal beside the Files pane fc, in its tab.
func terminalBeside(t *testing.T, a *App, tt interface{ Frame() }, fc *conn) *conn {
	t.Helper()
	a.Connect(fc.host.ID, openAt{pane: fc.id, edge: layout.Right})
	tt.Frame()
	tc := a.focused()
	if tc == nil || tc.kind != layout.Terminal || a.ws.ActiveTab().Count(layout.Terminal) != 1 {
		t.Fatalf("no terminal beside the files pane: %v", a.ws.Items())
	}
	return tc
}

func TestFilesFollowTerminal(t *testing.T) {
	a, tt, fc, root := filesPane(t)
	write(t, filepath.Join(root, "var", "log", "syslog"), "x")
	write(t, filepath.Join(root, "etc", "hosts"), "x")
	// A shell reports an SFTP path. On Windows that is /C:/..., not the
	// local path the files were created with.
	logs := sftpsvc.RemoteJoin(fc.fs.remote.dir, "var", "log")
	etc := sftpsvc.RemoteJoin(fc.fs.remote.dir, "etc")
	h, _ := a.store.Get(fc.host.ID)
	h.CwdFollow = true // as the host editor makes it
	if _, err := a.store.Update(h); err != nil {
		t.Fatal(err)
	}
	tc := terminalBeside(t, a, tt, fc)
	waitFor(t, tt, "terminal up", func() bool { return !tc.up.IsZero() })

	// The shell reports nothing: after a while, the snippet.
	if tt.HasText(cwdSnippet) {
		t.Fatal("snippet at once")
	}
	tt.Move(600, 700) // over nothing that has a tooltip
	tt.Advance(noReportAfter + time.Second)
	tt.Frame()
	if !tt.HasText(cwdSnippet) {
		t.Fatalf("no snippet in %q", tt.Texts())
	}
	a.ws.Activate(fc.id)
	tt.Frame()
	mustClick(t, tt, "Copy")
	tt.Frame()
	if tt.Clipboard() != cwdSnippet {
		t.Errorf("clipboard %q", tt.Clipboard())
	}

	// cd /var/log: the Files pane goes there.
	tc.s.Term.WriteString("\x1b]7;file://box" + logs + "\x07")
	tt.Frame()
	waitFor(t, tt, "followed", func() bool { return fc.fs.remote.dir == logs && tt.HasText("syslog") })
	if tt.HasText(cwdSnippet) {
		t.Error("snippet after a report")
	}

	// Stopped in the header: it stays.
	mustClick(t, tt, "Follow terminal")
	tc.s.Term.WriteString("\x1b]1337;CurrentDir=" + etc + "\x07")
	tt.Frame()
	time.Sleep(100 * time.Millisecond)
	tt.Frame()
	if fc.fs.remote.dir != logs {
		t.Fatalf("followed while stopped: %q", fc.fs.remote.dir)
	}
	// Following again catches up.
	mustClick(t, tt, "Follow terminal")
	waitFor(t, tt, "caught up", func() bool { return fc.fs.remote.dir == etc })

	// The host switched off: no button, no following.
	h, _ = a.store.Get(fc.host.ID)
	h.CwdFollow = false
	if _, err := a.store.Update(h); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Follow terminal") {
		t.Error("follow button with the host switch off")
	}
	tc.s.Term.WriteString("\x1b]7;file://box" + logs + "\x07")
	tt.Frame()
	time.Sleep(100 * time.Millisecond)
	tt.Frame()
	if fc.fs.remote.dir != etc {
		t.Errorf("followed with the host switch off: %q", fc.fs.remote.dir)
	}
}

func TestFilesFollowOnlyTheTerminalsHost(t *testing.T) {
	var asked atomic.Int32
	a, tt, fc, root := filesPaneOn(t, func(srv *sshtest.Server) {
		srv.Exec = func(ch ssh.Channel, cmd string) int {
			if cmd != "hostname" {
				return 127
			}
			asked.Add(1)
			io.WriteString(ch, "web1.example.com\n")
			return 0
		}
	})
	dirs := map[string]string{}
	for _, d := range []string{"a", "b", "c", "d", "e", "f"} {
		write(t, filepath.Join(root, d, "in-"+d), "x")
		dirs[d] = sftpsvc.RemoteJoin(fc.fs.remote.dir, d)
	}
	h, _ := a.store.Get(fc.host.ID)
	h.CwdFollow = true
	if _, err := a.store.Update(h); err != nil {
		t.Fatal(err)
	}
	tc := terminalBeside(t, a, tt, fc)
	waitFor(t, tt, "terminal up", func() bool { return !tc.up.IsZero() })
	report := func(host, dir string) {
		tc.s.Term.WriteString("\x1b]7;file://" + host + dir + "\x07")
		tt.Frame()
	}
	stays := func(what, dir string) {
		t.Helper()
		for range 5 {
			time.Sleep(20 * time.Millisecond)
			tt.Frame()
		}
		if fc.fs.remote.dir != dir {
			t.Fatalf("%s: followed to %q", what, fc.fs.remote.dir)
		}
	}

	// The server's own name, short or full: followed (once asked).
	report("WEB1", dirs["a"])
	waitFor(t, tt, "own short name", func() bool { return fc.fs.remote.dir == dirs["a"] })
	report("web1.example.com", dirs["b"])
	waitFor(t, tt, "own full name", func() bool { return fc.fs.remote.dir == dirs["b"] })
	// No host, localhost and the address in the settings: followed.
	report("", dirs["c"])
	waitFor(t, tt, "no host", func() bool { return fc.fs.remote.dir == dirs["c"] })
	report("localhost", dirs["d"])
	waitFor(t, tt, "localhost", func() bool { return fc.fs.remote.dir == dirs["d"] })
	report("127.0.0.1", dirs["e"])
	waitFor(t, tt, "address", func() bool { return fc.fs.remote.dir == dirs["e"] })

	// ssh db2 inside the terminal: its reports are not this server's.
	report("db2", dirs["f"])
	stays("another host", dirs["e"])
	report("web1.other.net", dirs["f"])
	stays("another domain", dirs["e"])
	// Back out of it: followed again.
	report("web1", dirs["a"])
	waitFor(t, tt, "back", func() bool { return fc.fs.remote.dir == dirs["a"] })
	if n := asked.Load(); n != 1 {
		t.Errorf("asked the hostname %d times, want once per connection", n)
	}
}

func TestSameHostName(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"web1", "web1", true},
		{"web1", "WEB1", true},
		{"web1", "web1.example.com", true},
		{"web1.example.com", "web1", true},
		{"web1.example.com", "web1.other.net", false},
		{"web1", "web2", false},
		{"10", "10.0.0.1", false},
		{"10.0.0.1", "10.0.0.2", false},
		{"", "", false},
	} {
		if got := sameHostName(c.a, c.b); got != c.same {
			t.Errorf("sameHostName(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestFollowDir(t *testing.T) {
	for in, want := range map[string]string{
		"/srv/app":          "/srv/app",
		"/srv/app/":         "/srv/app",
		"/srv/a/../b/./c//": "/srv/b/c",
		"/../..":            "/",
		"/":                 "/",
		"":                  "",
		"srv/app":           "",
		"~/app":             "",
		"../etc":            "",
		"/a\x00b":           "",
	} {
		if got := followDir(in); got != want {
			t.Errorf("followDir(%q) = %q, want %q", in, got, want)
		}
	}
}
