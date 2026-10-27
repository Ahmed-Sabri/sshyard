package session

import (
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/Licoy/sshyard/internal/vt"
	"golang.org/x/crypto/ssh"
)

// A program that went with the mouse on does not leave it on: not when
// its shell ends, not for the next shell.
func TestInputModesResetWithTheShell(t *testing.T) {
	srv := server(t)
	var shells atomic.Int32
	release := make(chan struct{})
	srv.Shell = func(ch ssh.Channel, _ sshtest.PTY, _ <-chan sshtest.PTY) int {
		n := shells.Add(1)
		if n == 1 {
			io.WriteString(ch, "\x1b[?1049h\x1b[?1002;1006;1004;2004hfull screen")
			<-release
			return 0 // exits without undoing them
		}
		io.WriteString(ch, "second shell")
		buf := make([]byte, 64)
		for {
			if _, err := ch.Read(buf); err != nil {
				return 0
			}
		}
	}
	s := New(Config{Addr: srv.Addr, User: "demo", Credentials: password("secret"), HostKey: trusted(t, srv).Callback()},
		Options{Backoff: 20 * time.Millisecond}, 80, 24)
	defer s.Close()
	s.Start()
	waitText(t, s, "full screen")
	m := s.Term.Modes()
	if m.Mouse != vt.MouseButton || !m.MouseSGR || !m.FocusReport || !m.BracketedPaste || !m.AltScreen {
		t.Fatalf("modes %+v", m)
	}
	close(release)
	waitState(t, s, Exited)
	m = s.Term.Modes()
	if m.MouseTracking || m.MouseSGR || m.FocusReport || m.BracketedPaste {
		t.Errorf("after the shell ended: %+v", m)
	}
	if !m.AltScreen {
		t.Error("the last screen of the program is gone at its exit")
	}
	s.Reconnect()
	waitText(t, s, "second shell")
	if s.Term.Modes().AltScreen {
		t.Error("the new shell is on the alternate screen")
	}
}
