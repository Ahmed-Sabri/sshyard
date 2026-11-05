package telnet

import "time"

// Option codes.
const (
	optBinary     = 0  // RFC 856
	optEcho       = 1  // RFC 857
	optSGA        = 3  // RFC 858
	optTimingMark = 6  // RFC 860
	optTTYPE      = 24 // RFC 1091
	optNAWS       = 31 // RFC 1073
)

// option is what SSHYard does with one Telnet option. Options missing
// from the table are refused both ways.
type option struct {
	local  bool // we perform it when the server asks (DO -> WILL)
	remote bool // the server may perform it (WILL -> DO)
	// enabled runs, with c.mu held, when our side turns on.
	enabled func(c *Conn)
	// sub handles IAC SB <opt> data IAC SE while either side is on.
	sub func(c *Conn, data []byte)
}

var options = map[byte]option{
	optBinary: {local: true, remote: true},
	optEcho:   {remote: true},
	optSGA:    {local: true, remote: true},
	optTTYPE:  {local: true, sub: (*Conn).ttype},
	optNAWS:   {local: true, enabled: (*Conn).naws},
}

// Q method states (RFC 1143), one per option and side. The client only
// ever asks to enable an option, never to disable one, so WANTNO and the
// queue bit cannot arise.
const (
	qNo uint8 = iota
	qYes
	qWantYes
)

// ask requests one side of opt to turn on: verb is WILL for ours, DO for
// the server's.
func (c *Conn) ask(q *uint8, verb, opt byte) {
	if *q == qNo {
		*q = qWantYes
		c.cmd(verb, opt)
	}
}

// recv applies the server's WILL/DO (on) or WONT/DONT (!on) to one side
// of opt. ok is whether we agree to that side being on; yes and no are the
// verbs that answer (DO/DONT or WILL/WONT). It reports whether the side
// just turned on. Answers to our own requests are never answered again,
// which is what keeps negotiation from looping.
func (c *Conn) recv(q *uint8, on, ok bool, opt, yes, no byte) bool {
	switch {
	case on && *q == qNo:
		if !ok {
			c.cmd(no, opt)
			return false
		}
		*q = qYes
		c.cmd(yes, opt)
		return true
	case on && *q == qWantYes:
		*q = qYes
		return true
	case !on && *q == qYes:
		*q = qNo
		c.cmd(no, opt)
	case !on && *q == qWantYes:
		*q = qNo
	}
	return false
}

// negotiate handles IAC verb opt.
func (c *Conn) negotiate(verb, opt byte) {
	if opt == optTimingMark {
		c.timingMark(verb)
		return
	}
	o := options[opt]
	switch verb {
	case cmdWILL, cmdWONT:
		c.recv(&c.him[opt], verb == cmdWILL, o.remote, opt, cmdDO, cmdDONT)
	case cmdDO, cmdDONT:
		if c.recv(&c.us[opt], verb == cmdDO, o.local, opt, cmdWILL, cmdWONT) && o.enabled != nil {
			o.enabled(c)
		}
	}
}

// timingMark answers DO TIMING-MARK and completes a probe on WILL or WONT,
// outside the Q method: the option is a one-shot marker, never a state.
func (c *Conn) timingMark(verb byte) {
	switch verb {
	case cmdDO:
		c.cmd(cmdWILL, optTimingMark)
	case cmdWILL, cmdWONT:
		switch {
		case c.staleTM > 0:
			c.staleTM--
		case c.probe != nil:
			c.probe <- time.Since(c.probeAt)
			c.probe = nil
		}
	}
}

// subneg dispatches the subnegotiation in c.sb.
func (c *Conn) subneg() {
	if len(c.sb) == 0 {
		return
	}
	opt := c.sb[0]
	if o := options[opt]; o.sub != nil && (c.us[opt] == qYes || c.him[opt] == qYes) {
		o.sub(c, c.sb[1:])
	}
}

// ttype answers TERMINAL-TYPE SEND with IS and the terminal name.
func (c *Conn) ttype(data []byte) {
	const send, is = 1, 0
	if len(data) > 0 && data[0] == send {
		c.queueSB(optTTYPE, append([]byte{is}, c.term...))
	}
}

// naws queues the window size.
func (c *Conn) naws() {
	w, h := clamp(c.cols), clamp(c.rows)
	c.queueSB(optNAWS, []byte{byte(w >> 8), byte(w), byte(h >> 8), byte(h)})
}

func clamp(n int) int { return max(1, min(n, 0xffff)) }
