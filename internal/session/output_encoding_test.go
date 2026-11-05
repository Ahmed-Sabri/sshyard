package session

import (
	"context"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/Licoy/sshyard/internal/vt"
	"golang.org/x/crypto/ssh"
)

func TestSharedCancelDoesNotWaitForOutputDrain(t *testing.T) {
	srv := server(t)
	p := NewPool()
	var asked atomic.Int32
	cfg := pooled(t, p, srv.Addr, trusted(t, srv), &asked)
	a, b := New(cfg, Options{}, 80, 24), New(cfg, Options{}, 80, 24)
	defer a.Close()
	defer b.Close()
	blocked, resume := make(chan struct{}), make(chan struct{})
	defer close(resume)
	var gate atomic.Bool
	a.OnChange = func() {
		if strings.Contains(a.Term.ScreenText(), "you said: pause") && gate.CompareAndSwap(false, true) {
			close(blocked)
			<-resume
		}
	}
	a.Start()
	b.Start()
	waitState(t, a, Connected)
	waitState(t, b, Connected)
	a.Write([]byte("pause\r"))
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("output copy did not block")
	}
	a.Close()
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("shared-session cancellation waited for output drain")
	}
	b.Write([]byte("still alive\r"))
	waitText(t, b, "you said: still alive")
}

func TestLostConnectionDrainsOutputBeforeNextStream(t *testing.T) {
	srv := server(t)
	start, blocked, tail := make(chan struct{}), make(chan struct{}), make(chan struct{})
	resume := make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(resume) })
	srv.Shell = func(ch ssh.Channel, _ sshtest.PTY, _ <-chan sshtest.PTY) int {
		<-start
		io.WriteString(ch, "old")
		<-blocked
		io.WriteString(ch, "\xd6")
		close(tail)
		io.Copy(io.Discard, ch)
		return 0
	}
	s := New(Config{Addr: srv.Addr, User: "demo", Credentials: password("secret"), HostKey: trusted(t, srv).Callback()}, Options{}, 80, 24)
	defer s.Close()
	s.Term.SetOutputEncoding(vt.EncodingGBK)
	if err := s.connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	var gate atomic.Bool
	s.OnChange = func() {
		if s.Term.ScreenText() == "old" && gate.CompareAndSwap(false, true) {
			close(blocked)
			<-resume // Keep stdout copy alive with another old-stream byte queued.
		}
	}
	done := make(chan error, 1)
	go func() { done <- s.serve(context.Background()) }()
	close(start)
	<-tail
	srv.Drop()
	select {
	case err := <-done:
		t.Fatalf("serve ended before old output copy drained: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	unblock.Do(func() { close(resume) })
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("lost connection did not report an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old output did not drain")
	}
	s.Term.ResetOutputStream()
	s.Term.WriteString("@\xd6\xd0")
	if text := s.Term.ScreenText(); text != "old�@中" {
		t.Fatalf("old output crossed new stream boundary: %q", text)
	}
}

func TestReconnectOutputEncoding(t *testing.T) {
	for _, tc := range []struct {
		encoding vt.OutputEncoding
		fragment string
		text     string
	}{
		{vt.EncodingUTF8, "\xe4", "@中"},
		{vt.EncodingGBK, "\xd6", "@\xd6\xd0"},
	} {
		t.Run(string(tc.encoding), func(t *testing.T) {
			srv := server(t)
			var shells atomic.Int32
			srv.Shell = func(ch ssh.Channel, _ sshtest.PTY, _ <-chan sshtest.PTY) int {
				if shells.Add(1) == 1 {
					io.WriteString(ch, "before")
				} else {
					io.WriteString(ch, tc.text)
				}
				io.Copy(io.Discard, ch)
				return 0
			}
			s := New(Config{Addr: srv.Addr, User: "demo", Credentials: password("secret"), HostKey: trusted(t, srv).Callback()},
				Options{AutoReconnect: true, Backoff: 20 * time.Millisecond}, 80, 24)
			defer s.Close()
			if err := s.Term.SetOutputEncoding(tc.encoding); err != nil {
				t.Fatal(err)
			}
			s.Start()
			waitText(t, s, "before")
			// Buffer an unfinished old-stream character after all old output.
			s.Term.WriteString(tc.fragment)
			srv.Drop()
			waitFor(t, "reconnected shell", func() bool { return s.Status().Connects == 2 })
			waitText(t, s, "before�@中")
			if s.Term.OutputEncoding() != tc.encoding {
				t.Fatalf("selection changed to %s", s.Term.OutputEncoding())
			}
		})
	}
}
