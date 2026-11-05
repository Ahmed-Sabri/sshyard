// Package telnet is SSHYard's Telnet client (RFC 854/855) over any byte
// stream. It negotiates options with the RFC 1143 Q method, answers
// TERMINAL-TYPE and NAWS, measures round trips with TIMING-MARK, and hands
// the caller the server's data with the protocol stripped, ready for a
// terminal emulator.
package telnet

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// Commands.
const (
	cmdSE   = 240
	cmdBRK  = 243
	cmdSB   = 250
	cmdWILL = 251
	cmdWONT = 252
	cmdDO   = 253
	cmdDONT = 254
	cmdIAC  = 255
)

// maxSB bounds a subnegotiation; a longer one is read and dropped.
const maxSB = 4096

// ErrClosed reports a probe cut short by the end of the connection.
var ErrClosed = errors.New("telnet: connection closed")

// Options configure a Conn.
type Options struct {
	Term       string // answered to TERMINAL-TYPE (RFC 1091); "" is "xterm-256color"
	Cols, Rows int    // initial window size for NAWS (RFC 1073)
}

// Parser states.
const (
	sData  = iota
	sIAC   // after IAC
	sVerb  // after IAC WILL/WONT/DO/DONT
	sSB    // inside IAC SB
	sSBIAC // after IAC inside IAC SB
)

// Conn is a Telnet client session over rw. Read is meant for one
// goroutine; Write, Resize, Probe and Close may be called from others.
type Conn struct {
	rw   io.ReadWriteCloser
	term string

	// mu guards the negotiation state and everything the parser touches.
	// It is taken before wmu, and handed over to it by send so that writes
	// go out in the order of the state changes behind them.
	mu       sync.Mutex
	us, him  [256]uint8 // Q method state of each option, ours and the server's
	cols     int
	rows     int
	out      []byte // replies queued while parsing
	probe    chan time.Duration
	probeAt  time.Time
	staleTM  int // TIMING-MARK answers still owed to abandoned probes
	state    uint8
	verb     byte
	sb       []byte
	sbOver   bool // the subnegotiation outgrew maxSB
	cr       bool // the last data byte was CR
	wmu      sync.Mutex
	rbuf     []byte // reader only
	rerr     error  // sticky read error, reader only
	probeSem chan struct{}
	done     chan struct{}
	doneOnce sync.Once
}

// New starts a Telnet session on rw and sends the client's opening offers
// (WILL NAWS, WILL TTYPE, DO ECHO, WILL SGA, DO SGA). A failure to send
// them shows up on the next Read or Write.
func New(rw io.ReadWriteCloser, o Options) *Conn {
	term := o.Term
	if term == "" {
		term = "xterm-256color"
	}
	c := &Conn{
		rw: rw, term: term, cols: o.Cols, rows: o.Rows,
		rbuf:     make([]byte, 16<<10),
		probeSem: make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	c.mu.Lock()
	c.ask(&c.us[optNAWS], cmdWILL, optNAWS)
	c.ask(&c.us[optTTYPE], cmdWILL, optTTYPE)
	c.ask(&c.him[optEcho], cmdDO, optEcho)
	c.ask(&c.us[optSGA], cmdWILL, optSGA)
	c.ask(&c.him[optSGA], cmdDO, optSGA)
	_ = c.flush()
	return c
}

// Read returns the server's data, protocol stripped. It returns io.EOF
// itself when the server closes the connection; other errors are returned
// as the transport gave them.
func (c *Conn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for c.rerr == nil {
		n, err := c.rw.Read(c.rbuf[:min(len(p), len(c.rbuf))])
		c.mu.Lock()
		m := 0
		for _, b := range c.rbuf[:n] {
			m = c.step(p, m, b)
		}
		_ = c.flush() // a broken transport fails the next read too
		if err != nil {
			c.rerr = err
			c.shut()
		}
		if m > 0 {
			return m, nil
		}
	}
	return 0, c.rerr
}

// step feeds one received byte to the parser, appending data to out[n:].
func (c *Conn) step(out []byte, n int, b byte) int {
	switch c.state {
	case sData:
		if b == cmdIAC {
			c.state = sIAC
			return n
		}
		return c.data(out, n, b)
	case sIAC:
		c.state = sData
		switch b {
		case cmdIAC:
			return c.data(out, n, b)
		case cmdWILL, cmdWONT, cmdDO, cmdDONT:
			c.verb, c.state = b, sVerb
		case cmdSB:
			c.sb, c.sbOver, c.state = c.sb[:0], false, sSB
		} // NOP, GA and the other commands mean nothing here
	case sVerb:
		c.state = sData
		c.negotiate(c.verb, b)
	case sSB:
		if b == cmdIAC {
			c.state = sSBIAC
		} else {
			c.sbByte(b)
		}
	case sSBIAC:
		switch b {
		case cmdIAC:
			c.sbByte(b)
			c.state = sSB
		case cmdSE:
			c.state = sData
			if !c.sbOver {
				c.subneg()
			}
		default:
			// IAC x inside SB is malformed: drop the subnegotiation and
			// read IAC x as a command.
			c.state = sIAC
			return c.step(out, n, b)
		}
	}
	return n
}

// data appends a data byte, dropping the NUL of CR NUL unless the server
// sends binary.
func (c *Conn) data(out []byte, n int, b byte) int {
	if c.cr && b == 0 && c.him[optBinary] != qYes {
		c.cr = false
		return n
	}
	c.cr = b == '\r'
	out[n] = b
	return n + 1
}

func (c *Conn) sbByte(b byte) {
	if len(c.sb) < maxSB {
		c.sb = append(c.sb, b)
	} else {
		c.sbOver = true
	}
}

// Write sends user data, doubling IAC and, unless we send binary, turning
// a CR not followed by LF into CR NUL. It returns len(p) on success.
func (c *Conn) Write(p []byte) (int, error) {
	c.mu.Lock()
	bin := c.us[optBinary] == qYes
	buf := make([]byte, 0, len(p)+8)
	for i, b := range p {
		buf = append(buf, b)
		switch {
		case b == cmdIAC:
			buf = append(buf, cmdIAC)
		case b == '\r' && !bin && (i+1 == len(p) || p[i+1] != '\n'):
			buf = append(buf, 0)
		}
	}
	if err := c.send(buf); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Break sends BRK, Telnet's break: the line is not held, so d is not
// used.
func (c *Conn) Break(d time.Duration) error {
	c.mu.Lock()
	return c.send([]byte{cmdIAC, cmdBRK})
}

// Resize remembers the window size and sends it if NAWS is on. Sizes are
// clamped to 1..65535.
func (c *Conn) Resize(cols, rows int) error {
	c.mu.Lock()
	c.cols, c.rows = cols, rows
	if c.us[optNAWS] == qYes {
		c.naws()
	}
	return c.flush()
}

// Probe measures a round trip with TIMING-MARK (RFC 860): it sends DO
// TIMING-MARK and waits for the server's WILL or WONT, which the goroutine
// calling Read picks up, so Read must be running. Probes run one at a
// time; a concurrent Probe waits its turn. It returns ctx.Err() when ctx
// ends first and ErrClosed when the connection does.
func (c *Conn) Probe(ctx context.Context) (time.Duration, error) {
	select {
	case <-c.done:
		return 0, ErrClosed
	default:
	}
	select {
	case c.probeSem <- struct{}{}:
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-c.done:
		return 0, ErrClosed
	}
	defer func() { <-c.probeSem }()
	ch := make(chan time.Duration, 1)
	c.mu.Lock()
	c.probe, c.probeAt = ch, time.Now()
	c.cmd(cmdDO, optTimingMark)
	if err := c.flush(); err != nil {
		c.mu.Lock()
		c.probe = nil
		c.mu.Unlock()
		return 0, err
	}
	var err error
	select {
	case d := <-ch:
		return d, nil
	case <-ctx.Done():
		err = ctx.Err()
	case <-c.done:
		err = ErrClosed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.probe != ch { // answered meanwhile
		return <-ch, nil
	}
	// The answer may still come: keep it from completing the next probe.
	c.probe = nil
	c.staleTM++
	return 0, err
}

// Close closes the transport, ending any probe in flight.
func (c *Conn) Close() error {
	c.shut()
	return c.rw.Close()
}

func (c *Conn) shut() { c.doneOnce.Do(func() { close(c.done) }) }

// cmd queues IAC verb opt.
func (c *Conn) cmd(verb, opt byte) { c.out = append(c.out, cmdIAC, verb, opt) }

// queueSB queues IAC SB opt payload IAC SE, doubling IAC in the payload.
func (c *Conn) queueSB(opt byte, payload []byte) {
	c.out = append(c.out, cmdIAC, cmdSB, opt)
	for _, b := range payload {
		if b == cmdIAC {
			c.out = append(c.out, cmdIAC)
		}
		c.out = append(c.out, b)
	}
	c.out = append(c.out, cmdIAC, cmdSE)
}

// flush sends the queued replies; like send, it releases c.mu.
func (c *Conn) flush() error {
	out := c.out
	c.out = nil
	if len(out) == 0 {
		c.mu.Unlock()
		return nil
	}
	return c.send(out)
}

// send writes b in one piece. It is called with c.mu held and releases it
// once the write lock is taken.
func (c *Conn) send(b []byte) error {
	c.wmu.Lock()
	c.mu.Unlock()
	defer c.wmu.Unlock()
	_, err := c.rw.Write(b)
	return err
}
