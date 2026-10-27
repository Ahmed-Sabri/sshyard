package forward

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/netproxy"
)

type proxyLogCapture struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (c *proxyLogCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buffer.Write(p)
}
func (c *proxyLogCapture) text() string { c.mu.Lock(); defer c.mu.Unlock(); return c.buffer.String() }
func captureProxyLog(t *testing.T) *proxyLogCapture {
	t.Helper()
	previous := log.Writer()
	capture := &proxyLogCapture{}
	log.SetOutput(capture)
	t.Cleanup(func() { log.SetOutput(previous) })
	return capture
}

func TestProxyResourceLogsAreBoundedAndRedacted(t *testing.T) {
	capture := captureProxyLog(t)
	f := &Forward{}
	f.proxyError(errors.New("not a resource error: sensitive detail"))
	if capture.text() != "" {
		t.Fatal("unrelated proxy error logged")
	}
	f.proxyError(fmt.Errorf("sensitive detail: %w", netproxy.ErrHandshakeTooLarge))
	for range 100 {
		f.proxyError(netproxy.ErrClientLimit)
	}
	text := capture.text()
	if strings.Contains(text, "sensitive detail") || strings.Count(text, "dynamic proxy rejected client") != 1 || !strings.Contains(text, netproxy.ErrHandshakeTooLarge.Error()) {
		t.Fatalf("unexpected log: %q", text)
	}
	f.mu.Lock()
	f.proxyLimitAt = time.Now().Add(-2 * time.Minute)
	f.mu.Unlock()
	f.proxyError(netproxy.ErrClientLimit)
	if strings.Count(capture.text(), "dynamic proxy rejected client") != 2 || !strings.Contains(capture.text(), netproxy.ErrClientLimit.Error()) {
		t.Fatalf("limit did not rearm: %q", capture.text())
	}
}

func TestDynamicProxyResourceRejectionLogged(t *testing.T) {
	capture := captureProxyLog(t)
	srv := server(t)
	listen := freePort(t)
	start(t, Rule{Kind: Dynamic, Listen: listen}, config(t, srv))
	c, err := net.Dial("tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	io.WriteString(c, "CONNECT example.test:22 HTTP/1.1\r\nX: "+strings.Repeat("a", 40<<10))
	waitFor(t, "resource rejection log", func() bool { return strings.Contains(capture.text(), netproxy.ErrHandshakeTooLarge.Error()) })
	if strings.Contains(capture.text(), "example.test") {
		t.Fatalf("peer data in log: %q", capture.text())
	}
}
