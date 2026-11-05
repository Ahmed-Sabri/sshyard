package telnet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

var opening = []byte{
	cmdIAC, cmdWILL, optNAWS,
	cmdIAC, cmdWILL, optTTYPE,
	cmdIAC, cmdDO, optEcho,
	cmdIAC, cmdWILL, optSGA,
	cmdIAC, cmdDO, optSGA,
}

// harness is a fake server on the far end of a net.Pipe, with a goroutine
// copying the client's Read output.
type harness struct {
	t   *testing.T
	c   *Conn
	srv net.Conn

	mu       sync.Mutex
	wire     []byte // bytes the client sent
	wpos     int
	data     []byte // bytes Read returned
	dpos     int
	rerr     error
	readDone chan struct{}
}

// start dials a client whose reader uses a buffer of readSize, and
// consumes its opening offers.
func start(t *testing.T, o Options, readSize int) *harness {
	t.Helper()
	cli, srv := net.Pipe()
	h := &harness{t: t, srv: srv, readDone: make(chan struct{})}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := srv.Read(buf)
			h.mu.Lock()
			h.wire = append(h.wire, buf[:n]...)
			h.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	h.c = New(cli, o)
	go func() {
		defer close(h.readDone)
		buf := make([]byte, readSize)
		for {
			n, err := h.c.Read(buf)
			if n == 0 && err == nil {
				t.Error("Read returned 0, nil")
			}
			h.mu.Lock()
			h.data = append(h.data, buf[:n]...)
			h.rerr = err
			h.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		h.c.Close()
		srv.Close()
		<-h.readDone
	})
	h.expectWire(opening...)
	return h
}

func (h *harness) send(b ...byte) {
	h.t.Helper()
	if _, err := h.srv.Write(b); err != nil {
		h.t.Fatal(err)
	}
}

// sendSplit writes b one byte per write.
func (h *harness) sendSplit(b ...byte) {
	h.t.Helper()
	for i := range b {
		h.send(b[i])
	}
}

// waitFor polls until cond holds, with h.mu held while it runs.
func (h *harness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.mu.Lock()
		ok := cond()
		h.mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// expectWire checks that the client's next bytes are want.
func (h *harness) expectWire(want ...byte) {
	h.t.Helper()
	h.waitFor("client bytes", func() bool { return len(h.wire)-h.wpos >= len(want) })
	h.mu.Lock()
	got := h.wire[h.wpos : h.wpos+len(want)]
	h.wpos += len(want)
	h.mu.Unlock()
	if !bytes.Equal(got, want) {
		h.t.Fatalf("client sent %v, want %v", got, want)
	}
}

// expectData checks that Read's next output is want.
func (h *harness) expectData(want string) {
	h.t.Helper()
	h.waitFor("data", func() bool { return len(h.data)-h.dpos >= len(want) })
	h.mu.Lock()
	got := string(h.data[h.dpos : h.dpos+len(want)])
	h.dpos += len(want)
	h.mu.Unlock()
	if got != want {
		h.t.Fatalf("Read gave %q, want %q", got, want)
	}
}

// fence proves the client sent nothing else so far: an unknown option is
// refused, and that refusal must be its very next output.
func (h *harness) fence() {
	h.t.Helper()
	h.send(cmdIAC, cmdDO, 99)
	h.expectWire(cmdIAC, cmdWONT, 99)
}

func (h *harness) states(opt byte) (us, him uint8) {
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	return h.c.us[opt], h.c.him[opt]
}

func naws(w0, w1, h0, h1 byte) []byte {
	return []byte{cmdIAC, cmdSB, optNAWS, w0, w1, h0, h1, cmdIAC, cmdSE}
}

func TestOpeningOffersAgreed(t *testing.T) {
	h := start(t, Options{Cols: 80, Rows: 24}, 4096)
	h.fence()
	// The server agrees to every offer: only NAWS has anything to say.
	h.send(cmdIAC, cmdDO, optNAWS, cmdIAC, cmdDO, optTTYPE, cmdIAC, cmdWILL, optEcho,
		cmdIAC, cmdDO, optSGA, cmdIAC, cmdWILL, optSGA)
	h.expectWire(naws(0, 80, 0, 24)...)
	h.fence()
	for _, opt := range []byte{optNAWS, optTTYPE, optSGA} {
		if us, _ := h.states(opt); us != qYes {
			t.Errorf("us[%d] = %d, want yes", opt, us)
		}
	}
	for _, opt := range []byte{optEcho, optSGA} {
		if _, him := h.states(opt); him != qYes {
			t.Errorf("him[%d] = %d, want yes", opt, him)
		}
	}
}

func TestNegotiationAcceptRefuse(t *testing.T) {
	h := start(t, Options{Cols: 80, Rows: 24}, 4096)
	cases := []struct {
		name string
		in   []byte
		want []byte // nil: no answer
	}{
		{"accept remote binary", []byte{cmdWILL, optBinary}, []byte{cmdIAC, cmdDO, optBinary}},
		{"accept local binary", []byte{cmdDO, optBinary}, []byte{cmdIAC, cmdWILL, optBinary}},
		{"refuse remote new-environ", []byte{cmdWILL, 39}, []byte{cmdIAC, cmdDONT, 39}},
		{"refuse local new-environ", []byte{cmdDO, 39}, []byte{cmdIAC, cmdWONT, 39}},
		{"refuse remote status", []byte{cmdWILL, 5}, []byte{cmdIAC, cmdDONT, 5}},
		{"refuse remote ttype", []byte{cmdWILL, optTTYPE}, []byte{cmdIAC, cmdDONT, optTTYPE}},
		{"refuse local echo", []byte{cmdDO, optEcho}, []byte{cmdIAC, cmdWONT, optEcho}},
		{"refuse local 200", []byte{cmdDO, 200}, []byte{cmdIAC, cmdWONT, 200}},
		{"echo declined", []byte{cmdWONT, optEcho}, nil},
		{"echo offered later", []byte{cmdWILL, optEcho}, []byte{cmdIAC, cmdDO, optEcho}},
		{"remote sga declined", []byte{cmdWONT, optSGA}, nil},
		{"remote sga offered later", []byte{cmdWILL, optSGA}, []byte{cmdIAC, cmdDO, optSGA}},
		{"local sga declined", []byte{cmdDONT, optSGA}, nil},
		{"local sga asked later", []byte{cmdDO, optSGA}, []byte{cmdIAC, cmdWILL, optSGA}},
		{"ttype declined", []byte{cmdDONT, optTTYPE}, nil},
		{"ttype asked later", []byte{cmdDO, optTTYPE}, []byte{cmdIAC, cmdWILL, optTTYPE}},
		{"naws declined", []byte{cmdDONT, optNAWS}, nil},
		{"naws asked later", []byte{cmdDO, optNAWS}, append([]byte{cmdIAC, cmdWILL, optNAWS}, naws(0, 80, 0, 24)...)},
		{"unasked refusal ignored", []byte{cmdWONT, 77}, nil},
	}
	for _, tc := range cases {
		h.send(append([]byte{cmdIAC}, tc.in...)...)
		if tc.want != nil {
			h.expectWire(tc.want...)
		}
		h.fence()
	}
}

func TestQMethodDoesNotLoop(t *testing.T) {
	h := start(t, Options{Cols: 80, Rows: 24}, 4096)
	// The server echoes our offers back, twice over.
	for range 2 {
		h.send(cmdIAC, cmdDO, optNAWS, cmdIAC, cmdWILL, optEcho, cmdIAC, cmdDO, optSGA, cmdIAC, cmdWILL, optSGA)
	}
	h.expectWire(naws(0, 80, 0, 24)...)
	h.fence()
	// Flip-flops: each change of mind gets one answer, repeats get none.
	steps := []struct{ in, want []byte }{
		{[]byte{cmdWONT, optEcho}, []byte{cmdIAC, cmdDONT, optEcho}},
		{[]byte{cmdWONT, optEcho}, nil},
		{[]byte{cmdWILL, optEcho}, []byte{cmdIAC, cmdDO, optEcho}},
		{[]byte{cmdWILL, optEcho}, nil},
		{[]byte{cmdDONT, optNAWS}, []byte{cmdIAC, cmdWONT, optNAWS}},
		{[]byte{cmdDONT, optNAWS}, nil},
		{[]byte{cmdDO, optNAWS}, append([]byte{cmdIAC, cmdWILL, optNAWS}, naws(0, 80, 0, 24)...)},
		{[]byte{cmdDO, optNAWS}, nil},
	}
	for _, s := range steps {
		h.send(append([]byte{cmdIAC}, s.in...)...)
		if s.want != nil {
			h.expectWire(s.want...)
		}
		h.fence()
	}
}

func TestNAWS(t *testing.T) {
	h := start(t, Options{Cols: 80, Rows: 24}, 4096)
	if err := h.c.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	h.fence() // not enabled yet: nothing sent
	h.send(cmdIAC, cmdDO, optNAWS)
	h.expectWire(naws(0, 100, 0, 30)...)
	if err := h.c.Resize(255, 511); err != nil {
		t.Fatal(err)
	}
	h.expectWire(cmdIAC, cmdSB, optNAWS, 0, 0xff, 0xff, 1, 0xff, 0xff, cmdIAC, cmdSE)
	if err := h.c.Resize(0, 70000); err != nil {
		t.Fatal(err)
	}
	h.expectWire(cmdIAC, cmdSB, optNAWS, 0, 1, 0xff, 0xff, 0xff, 0xff, cmdIAC, cmdSE)
	h.fence()
}

func TestNAWSRefused(t *testing.T) {
	h := start(t, Options{Cols: 80, Rows: 24}, 4096)
	h.send(cmdIAC, cmdDONT, optNAWS)
	if err := h.c.Resize(90, 40); err != nil {
		t.Fatal(err)
	}
	h.fence()
}

func TestTerminalType(t *testing.T) {
	sendReq := []byte{cmdIAC, cmdSB, optTTYPE, 1, cmdIAC, cmdSE}
	reply := func(term string) []byte {
		b := append([]byte{cmdIAC, cmdSB, optTTYPE, 0}, term...)
		return append(b, cmdIAC, cmdSE)
	}
	t.Run("default", func(t *testing.T) {
		h := start(t, Options{}, 4096)
		h.send(sendReq...)
		h.fence() // TTYPE not agreed yet: ignored
		h.send(cmdIAC, cmdDO, optTTYPE)
		h.send(sendReq...)
		h.expectWire(reply("xterm-256color")...)
		h.send(sendReq...) // asked again, same answer
		h.expectWire(reply("xterm-256color")...)
		h.fence()
	})
	t.Run("custom", func(t *testing.T) {
		h := start(t, Options{Term: "vt100"}, 4096)
		h.send(cmdIAC, cmdDO, optTTYPE)
		h.send(sendReq...)
		h.expectWire(reply("vt100")...)
	})
}

func TestDataCommandsStripped(t *testing.T) {
	h := start(t, Options{}, 4096)
	const nop, ga, ayt = 241, 249, 246
	h.send('a', cmdIAC, cmdIAC, 'b', cmdIAC, nop, cmdIAC, ga, cmdIAC, ayt, 'c')
	h.expectData("a\xffbc")
	// An unknown subnegotiation, with a doubled IAC inside, is dropped.
	h.send(cmdIAC, cmdSB, 99, 1, cmdIAC, cmdIAC, 2, cmdIAC, cmdSE, 'd')
	h.expectData("d")
	h.fence()
}

func TestSplitByteByByte(t *testing.T) {
	h := start(t, Options{Cols: 80, Rows: 24}, 1)
	h.sendSplit('a', '\r', 0, 'b', cmdIAC, cmdIAC,
		cmdIAC, cmdDO, optNAWS,
		cmdIAC, cmdDO, optTTYPE,
		cmdIAC, cmdSB, optTTYPE, 1, cmdIAC, cmdSE,
		cmdIAC, cmdSB, 99, cmdIAC, cmdIAC, cmdIAC, cmdSE,
		cmdIAC, 241, 'z')
	h.expectData("a\rb\xffz")
	h.expectWire(naws(0, 80, 0, 24)...)
	h.expectWire(append(append([]byte{cmdIAC, cmdSB, optTTYPE, 0}, "xterm-256color"...), cmdIAC, cmdSE)...)
	h.fence()
}

func TestCRNUL(t *testing.T) {
	h := start(t, Options{}, 4096)
	h.send([]byte("a\r\x00b\r\nc\r\r\x00")...)
	h.expectData("a\rb\r\nc\r\r")
	h.send('x', '\r')
	h.send(0, 'y')
	h.expectData("x\ry")
	h.send(cmdIAC, cmdWILL, optBinary)
	h.expectWire(cmdIAC, cmdDO, optBinary)
	h.send('\r', 0, '!')
	h.expectData("\r\x00!")
}

func TestWrite(t *testing.T) {
	h := start(t, Options{}, 4096)
	p := []byte{'a', 0xff, '\r', 'b', '\r', '\n', '\r'}
	n, err := h.c.Write(p)
	if err != nil || n != len(p) {
		t.Fatalf("Write = %d, %v; want %d", n, err, len(p))
	}
	h.expectWire('a', 0xff, 0xff, '\r', 0, 'b', '\r', '\n', '\r', 0)

	h.send(cmdIAC, cmdDO, optBinary)
	h.expectWire(cmdIAC, cmdWILL, optBinary)
	p = []byte{'\r', 0xff, 'c'}
	if n, err := h.c.Write(p); err != nil || n != len(p) {
		t.Fatalf("binary Write = %d, %v; want %d", n, err, len(p))
	}
	h.expectWire('\r', 0xff, 0xff, 'c')
}

// TestBreak: a break is IAC BRK, and fails once the connection is
// closed.
func TestBreak(t *testing.T) {
	h := start(t, Options{}, 4096)
	if err := h.c.Break(250 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	h.expectWire(cmdIAC, cmdBRK)
	h.c.Close()
	if err := h.c.Break(0); err == nil {
		t.Fatal("a closed connection sent a break")
	}
}

func TestWriteDoesNotInterleaveReplies(t *testing.T) {
	h := start(t, Options{}, 4096)
	const writes = 200
	done := make(chan error, 1)
	go func() {
		for range writes {
			if _, err := h.c.Write([]byte("ab")); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := range 100 {
		h.send(cmdIAC, cmdDO, byte(100+i))
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	h.waitFor("all bytes", func() bool { return len(h.wire)-h.wpos == 2*writes+3*100 })
	h.mu.Lock()
	wire := h.wire[h.wpos:]
	h.mu.Unlock()
	var data []byte
	for i := 0; i < len(wire); {
		if wire[i] == cmdIAC {
			if i+2 >= len(wire) || wire[i+1] != cmdWONT {
				t.Fatalf("broken command at %d: %v", i, wire[i:min(i+3, len(wire))])
			}
			i += 3
			continue
		}
		data = append(data, wire[i])
		i++
	}
	if string(data) != strings.Repeat("ab", writes) {
		t.Fatalf("user data mangled: %q", data)
	}
}

func TestProbe(t *testing.T) {
	h := start(t, Options{}, 4096)
	for _, answer := range []byte{cmdWILL, cmdWONT} {
		res := make(chan error, 1)
		var rtt time.Duration
		go func() {
			var err error
			rtt, err = h.c.Probe(context.Background())
			res <- err
		}()
		h.expectWire(cmdIAC, cmdDO, optTimingMark)
		time.Sleep(20 * time.Millisecond)
		h.send(cmdIAC, answer, optTimingMark)
		if err := <-res; err != nil {
			t.Fatal(err)
		}
		if rtt < 20*time.Millisecond {
			t.Errorf("rtt %v, want >= 20ms", rtt)
		}
		h.fence() // the answer is not answered
	}

	// The server asking for a timing mark gets one, every time.
	for range 2 {
		h.send(cmdIAC, cmdDO, optTimingMark)
		h.expectWire(cmdIAC, cmdWILL, optTimingMark)
	}
	h.send(cmdIAC, cmdDONT, optTimingMark)
	h.fence()
}

func TestProbeCancelAndStaleAnswer(t *testing.T) {
	h := start(t, Options{}, 4096)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := h.c.Probe(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Probe = %v, want deadline exceeded", err)
	}
	h.expectWire(cmdIAC, cmdDO, optTimingMark)

	// The late answer to the abandoned probe must not complete the next one.
	h.send(cmdIAC, cmdWILL, optTimingMark)
	res := make(chan error, 1)
	var rtt time.Duration
	go func() {
		var err error
		rtt, err = h.c.Probe(context.Background())
		res <- err
	}()
	h.expectWire(cmdIAC, cmdDO, optTimingMark)
	time.Sleep(20 * time.Millisecond)
	select {
	case err := <-res:
		t.Fatalf("probe completed by a stale answer: %v", err)
	default:
	}
	h.send(cmdIAC, cmdWILL, optTimingMark)
	if err := <-res; err != nil {
		t.Fatal(err)
	}
	if rtt < 20*time.Millisecond {
		t.Errorf("rtt %v, want >= 20ms", rtt)
	}
}

func TestEOF(t *testing.T) {
	h := start(t, Options{}, 4096)
	res := make(chan error, 1)
	go func() {
		_, err := h.c.Probe(context.Background())
		res <- err
	}()
	h.expectWire(cmdIAC, cmdDO, optTimingMark)
	h.send([]byte("bye")...)
	h.expectData("bye")
	h.srv.Close()
	<-h.readDone
	if h.rerr != io.EOF {
		t.Fatalf("Read error %v, want io.EOF", h.rerr)
	}
	if err := <-res; !errors.Is(err, ErrClosed) {
		t.Fatalf("Probe = %v, want ErrClosed", err)
	}
	if _, err := h.c.Probe(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Probe after close = %v, want ErrClosed", err)
	}
	if n, err := h.c.Read(make([]byte, 8)); n != 0 || err != io.EOF {
		t.Fatalf("Read after EOF = %d, %v", n, err)
	}
}

type failRW struct{ err error }

func (f failRW) Read([]byte) (int, error)    { return 0, f.err }
func (f failRW) Write(p []byte) (int, error) { return len(p), nil }
func (f failRW) Close() error                { return nil }

func TestReadErrorAsIs(t *testing.T) {
	boom := errors.New("boom")
	c := New(failRW{boom}, Options{})
	if _, err := c.Read(make([]byte, 8)); err != boom {
		t.Fatalf("Read = %v, want boom", err)
	}
}

func TestSubnegotiationBound(t *testing.T) {
	h := start(t, Options{}, 4096)
	h.send(cmdIAC, cmdDO, optTTYPE)
	// An oversized TTYPE SEND is dropped, not answered.
	big := append([]byte{cmdIAC, cmdSB, optTTYPE, 1}, bytes.Repeat([]byte{'x'}, 10000)...)
	h.send(append(big, cmdIAC, cmdSE, 'o', 'k')...)
	h.expectData("ok")
	h.fence()
	h.c.mu.Lock()
	n := len(h.c.sb)
	h.c.mu.Unlock()
	if n > maxSB {
		t.Fatalf("subnegotiation buffer grew to %d", n)
	}
	// A subnegotiation cut by another command is dropped, and the command runs.
	h.send(cmdIAC, cmdSB, optTTYPE, 1, cmdIAC, cmdDO, 98, 'z')
	h.expectWire(cmdIAC, cmdWONT, 98)
	h.expectData("z")
	h.fence()
}
