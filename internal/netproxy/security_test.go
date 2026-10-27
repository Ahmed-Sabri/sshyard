package netproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestServerBoundedHandshakes(t *testing.T) {
	tests := []struct {
		name    string
		server  *Server
		payload string
		want    error
	}{
		{"http line", &Server{}, "CONNECT x:1 HTTP/1.1\r\nX: " + strings.Repeat("a", maxHTTPHeaderBytes), ErrHandshakeTooLarge},
		{"http many headers", &Server{}, "CONNECT x:1 HTTP/1.1\r\n" + strings.Repeat("X: a\r\n", maxHTTPHeaderBytes/6), ErrHandshakeTooLarge},
		{"socks4 userid", &Server{}, "\x04\x01\x00\x01\x7f\x00\x00\x01" + strings.Repeat("a", 256), ErrHandshakeTooLarge},
		{"socks4a domain", &Server{}, "\x04\x01\x00\x01\x00\x00\x00\x01\x00" + strings.Repeat("a", 256), ErrHandshakeTooLarge},
		{"socks4 requires auth before userid", &Server{User: "u", Password: "p"}, "\x04\x01\x00\x01\x7f\x00\x00\x01", ErrAuth},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := test.server
			s.Dial = func(context.Context, string, string) (net.Conn, error) {
				t.Error("rejected handshake dialed target")
				return nil, errors.New("unexpected dial")
			}
			server, client := net.Pipe()
			defer client.Close()
			client.SetDeadline(time.Now().Add(3 * time.Second))
			done := make(chan error, 1)
			go func() { done <- s.ServeConn(server) }()
			go io.Copy(io.Discard, client)
			// Fragment the input to cover handshakes spanning reader fills.
			for start := 0; start < len(test.payload); start += 113 {
				if _, err := io.WriteString(client, test.payload[start:min(start+113, len(test.payload))]); err != nil {
					break
				}
			}
			select {
			case err := <-done:
				if !errors.Is(err, test.want) {
					t.Fatalf("got %v, want %v", err, test.want)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("handshake did not reject")
			}
		})
	}
}

func TestOutboundHTTPHeaderLimit(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	go func() {
		defer server.Close()
		http.ReadRequest(bufio.NewReader(server))
		io.WriteString(server, "HTTP/1.1 200 OK\r\nX: "+strings.Repeat("a", maxHTTPHeaderBytes))
	}()
	client.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := httpConnect(client, "example.test:22", "", ""); !errors.Is(err, ErrHandshakeTooLarge) {
		t.Fatalf("got %v", err)
	}
}

func TestPlainHTTPBodyNotLimitedByHeaderBudget(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		t.Run(fmt.Sprint(chunked), func(t *testing.T) {
			payload := strings.Repeat("body", maxHTTPHeaderBytes)
			web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if string(b) != payload {
					t.Errorf("body length %d, want %d", len(b), len(payload))
				}
				fmt.Fprint(w, "ok")
			}))
			defer web.Close()
			addr := serve(t, &Server{}, "")
			proxy, _ := url.Parse("http://" + addr)
			transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
			defer transport.CloseIdleConnections()
			cl := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			req, _ := http.NewRequest("POST", web.URL, strings.NewReader(payload))
			if chunked {
				req.ContentLength = -1
			}
			resp, err := cl.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, err := io.ReadAll(resp.Body)
			if err != nil || string(b) != "ok" {
				t.Fatalf("response %q %v", b, err)
			}
		})
	}
}

func TestServerClientLimitAndShutdown(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	rejected := make(chan error, 1)
	s := &Server{OnError: func(err error) {
		if errors.Is(err, ErrClientLimit) {
			rejected <- err
		}
	}}
	done := make(chan error, 1)
	go func() { done <- s.Serve(l) }()
	clients := make([]net.Conn, 0, maxActiveClients)
	defer func() {
		for _, c := range clients {
			c.Close()
		}
	}()
	for range maxActiveClients {
		c, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, c)
	}
	deadline := time.Now().Add(3 * time.Second)
	for s.active.Load() != maxActiveClients && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.active.Load() != maxActiveClients {
		t.Fatalf("active %d", s.active.Load())
	}
	extra, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	extra.SetReadDeadline(deadline)
	if _, err := extra.Read(make([]byte, 1)); err == nil || isTimeout(err) {
		t.Fatalf("extra client not closed: %v", err)
	}
	select {
	case <-rejected:
	case <-time.After(3 * time.Second):
		t.Fatal("missing limit error")
	}
	l.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not stop handshakes")
	}
	if s.active.Load() != 0 {
		t.Fatalf("active after shutdown %d", s.active.Load())
	}
}

func TestServerShutdownCancelsDial(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	entered := make(chan struct{})
	s := &Server{Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	done := make(chan error, 1)
	go func() { done <- s.Serve(l) }()
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	io.WriteString(c, "CONNECT example.test:22 HTTP/1.1\r\nHost: example.test:22\r\n\r\n")
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("dial did not start")
	}
	l.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not cancel dial")
	}
}

func TestServerShutdownClosesTunnel(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	target, up := net.Pipe()
	defer target.Close()
	s := &Server{Dial: func(context.Context, string, string) (net.Conn, error) { return up, nil }}
	done := make(chan error, 1)
	go func() { done <- s.Serve(l) }()
	c, err := (&Dialer{Kind: HTTP, Addr: l.Addr().String()}).DialContext(context.Background(), "tcp", "example.test:22")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	l.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not stop tunnel")
	}
	target.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := target.Read(make([]byte, 1)); err == nil || isTimeout(err) {
		t.Fatalf("upstream not closed: %v", err)
	}
}

type noDeadlineConn struct{ net.Conn }

func (c noDeadlineConn) SetDeadline(time.Time) error      { return errors.ErrUnsupported }
func (c noDeadlineConn) SetReadDeadline(time.Time) error  { return errors.ErrUnsupported }
func (c noDeadlineConn) SetWriteDeadline(time.Time) error { return errors.ErrUnsupported }

func TestPlainHTTPBlockedUpstreamWriteTimesOutWithoutDeadlineSupport(t *testing.T) {
	up, target := net.Pipe()
	defer target.Close()
	req, _ := http.NewRequest("POST", "http://example.test/upload", strings.NewReader("body"))
	done := make(chan error, 1)
	go func() { done <- writeProxyRequest(noDeadlineConn{up}, req, time.Now().Add(20*time.Millisecond)) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		up.Close()
		t.Fatal("blocked upstream write did not time out")
	}
}

func TestPlainHTTPUpstreamTimerStopsBeforeRelay(t *testing.T) {
	up, target := net.Pipe()
	defer target.Close()
	defer up.Close()
	req, _ := http.NewRequest("GET", "http://example.test/", nil)
	readDone := make(chan error, 1)
	go func() { _, err := http.ReadRequest(bufio.NewReader(target)); readDone <- err }()
	if err := writeProxyRequest(noDeadlineConn{up}, req, time.Now().Add(100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	// The old handshake timeout must no longer close an established tunnel.
	time.Sleep(120 * time.Millisecond)
	target.SetDeadline(time.Now().Add(time.Second))
	go func() { io.WriteString(target, "ok") }()
	b := make([]byte, 2)
	if _, err := io.ReadFull(up, b); err != nil || string(b) != "ok" {
		t.Fatalf("tunnel after handshake: %q %v", b, err)
	}
}
