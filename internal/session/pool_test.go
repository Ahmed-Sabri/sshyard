package session

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// pooled is a config on a pool, counting the times it was asked for
// credentials.
func pooled(t *testing.T, p *Pool, addr string, kh *KnownHosts, asked *atomic.Int32) Config {
	t.Helper()
	return Config{Addr: addr, User: "demo", HostKey: kh.Callback(), Pool: p, Key: "demo@" + addr,
		Credentials: func(context.Context) (Credentials, error) {
			asked.Add(1)
			return Credentials{Password: "secret"}, nil
		}}
}

func TestPoolSharesOneConnection(t *testing.T) {
	srv := server(t)
	kh := trusted(t, srv)
	p := NewPool()
	var asked atomic.Int32
	cfg := pooled(t, p, srv.Addr, kh, &asked)
	a := New(cfg, Options{}, 80, 24)
	a.Start()
	waitState(t, a, Connected)
	// A split, and a files pane: channels on the same connection.
	b := New(cfg, Options{}, 80, 24)
	b.Start()
	f := New(cfg, Options{NoShell: true}, 80, 24)
	f.Start()
	waitState(t, b, Connected)
	waitState(t, f, Connected)
	if logins(t, srv, 1) != 1 || asked.Load() != 1 {
		t.Fatalf("%d logins, %d credential asks; want 1 and 1", srv.Logins(), asked.Load())
	}
	if a.Client() != b.Client() || b.Client() != f.Client() {
		t.Error("not the same client")
	}
	if n := p.Shared(cfg.Key); n != 3 {
		t.Errorf("%d refs, want 3", n)
	}
	b.Write([]byte("hi\r"))
	waitText(t, b, "you said: hi")

	// Closing one pane leaves the others connected.
	b.Close()
	waitFor(t, "b's release", func() bool { return p.Shared(cfg.Key) == 2 })
	a.Write([]byte("still\r"))
	waitText(t, a, "you said: still")
	if f.Status().State != Connected {
		t.Error("files pane lost its connection")
	}
	// Its shell exiting lets go of it too.
	a.Write([]byte("exit\r"))
	waitState(t, a, Exited)
	waitFor(t, "a's release", func() bool { return p.Shared(cfg.Key) == 1 })
	f.Close()
	waitFor(t, "the last release", func() bool { return p.Shared(cfg.Key) == 0 })
}

func TestPoolReconnectsOnceForAll(t *testing.T) {
	srv := server(t)
	kh := trusted(t, srv)
	p := NewPool()
	var asked atomic.Int32
	cfg := pooled(t, p, srv.Addr, kh, &asked)
	opts := Options{AutoReconnect: true, Backoff: 20 * time.Millisecond}
	var ss []*Session
	for i := 0; i < 3; i++ {
		o := opts
		o.NoShell = i == 2
		s := New(cfg, o, 80, 24)
		defer s.Close()
		s.Start()
		waitState(t, s, Connected)
		ss = append(ss, s)
	}
	srv.Drop()
	for _, s := range ss {
		waitFor(t, "a reconnect", func() bool { st := s.Status(); return st.State == Connected && st.Connects == 2 })
	}
	if logins(t, srv, 2) != 2 || asked.Load() != 2 {
		t.Errorf("%d logins, %d credential asks after a drop; want 2 and 2", srv.Logins(), asked.Load())
	}
	if ss[0].Client() != ss[1].Client() || ss[1].Client() != ss[2].Client() {
		t.Error("not one new client")
	}
	ss[1].Write([]byte("back\r"))
	waitText(t, ss[1], "you said: back")
}

func TestPoolDialErrorIsShared(t *testing.T) {
	srv := server(t)
	kh := trusted(t, srv)
	p := NewPool()
	cfg := Config{Addr: srv.Addr, User: "demo", HostKey: kh.Callback(), Pool: p, Key: "k",
		Credentials: func(context.Context) (Credentials, error) { return Credentials{}, ErrNoCredentials }}
	s := New(cfg, Options{}, 80, 24)
	defer s.Close()
	s.Start()
	st := waitState(t, s, Failed)
	if Classify(st.Err) != KindNoCredentials {
		t.Errorf("err %v", st.Err)
	}
	if p.Shared("k") != 0 {
		t.Error("a failed dial stays in the pool")
	}
}

func TestPoolWaitersShareOneDial(t *testing.T) {
	srv := server(t)
	kh := trusted(t, srv)
	p := NewPool()
	var asked atomic.Int32
	cfg := pooled(t, p, srv.Addr, kh, &asked)
	release := make(chan struct{})
	p.dial = func(ctx context.Context, c Config) (_ *ssh.Client, err error) {
		<-release
		return Dial(ctx, c)
	}
	type res struct {
		c   *ssh.Client
		rel func()
		err error
	}
	out := make(chan res, 4)
	for i := 0; i < 4; i++ {
		go func() {
			c, rel, err := p.Acquire(context.Background(), cfg.Key, cfg)
			out <- res{c, rel, err}
		}()
	}
	waitFor(t, "four waiting", func() bool { return p.Shared(cfg.Key) == 4 })
	close(release)
	var first *ssh.Client
	for i := 0; i < 4; i++ {
		r := <-out
		if r.err != nil {
			t.Fatal(r.err)
		}
		if first == nil {
			first = r.c
		} else if r.c != first {
			t.Error("two clients")
		}
		defer r.rel()
	}
	if logins(t, srv, 1) != 1 || asked.Load() != 1 {
		t.Errorf("%d logins, %d asks", srv.Logins(), asked.Load())
	}
}
