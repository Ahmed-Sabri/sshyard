package vt

import (
	"bytes"
	"encoding/base64"
	"strings"
)

const (
	// oscCap is how much of an ordinary OSC is kept. OSC 52 is capped
	// by its decoded size instead, once "52;" has been seen.
	oscCap = 4096
	// OSC52MaxDefault is the decoded size of an OSC 52 paste when no
	// limit was set: 1 MiB.
	OSC52MaxDefault = 1 << 20
	// osc52MaxCeiling is the most the parser will buffer, decoded.
	osc52MaxCeiling = 8 << 20
)

// SetOSC52Max sets the largest decoded OSC 52 paste to keep. Zero uses
// the default (1 MiB). Anything above 8 MiB is capped there.
func (t *Terminal) SetOSC52Max(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.osc52Max = n
}

func (t *Terminal) osc52Limit() int {
	n := t.osc52Max
	if n <= 0 {
		n = OSC52MaxDefault
	}
	if n > osc52MaxCeiling {
		n = osc52MaxCeiling
	}
	return n
}

// osc52Sel maps Pc onto the system clipboard. c, p, s and 0–7 all do;
// an empty Pc is that clipboard too. Anything else is refused.
func osc52Sel(pc string) (string, bool) {
	if pc == "" {
		return "c", true
	}
	for i := 0; i < len(pc); i++ {
		c := pc[i]
		if c != 'c' && c != 'p' && c != 's' && (c < '0' || c > '7') {
			return "", false
		}
	}
	return pc, true
}

// b64DecodedLen is the decoded size of n base64 digits (padding not
// counted). It is what the payload would become, and an upper bound
// when the digits are not valid base64.
func b64DecodedLen(n int) int {
	if n <= 0 {
		return 0
	}
	full, rem := n/4, n%4
	out := full * 3
	if rem > 1 {
		out += rem - 1
	}
	return out
}

// decodeOSC52 decodes Pd. ok is false when the base64 is not valid.
// raw is the decoded byte count, before NULs are removed. The text is
// UTF-8 with invalid bytes replaced by U+FFFD and NULs stripped.
// shortcut: whitespace in the base64 is not stripped, so a wrapped payload is dropped; strip it if a real program wraps OSC 52.
func decodeOSC52(s string) (text string, raw int, ok bool) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(s)
	}
	if err != nil {
		return "", 0, false
	}
	raw = len(b)
	text = strings.ToValidUTF8(string(b), "\uFFFD")
	text = strings.ReplaceAll(text, "\x00", "")
	return text, raw, true
}

// osc52Done handles the body of OSC 52 (everything after the first
// semicolon). A read (Pd == "?") is reported and never answered.
func (t *Terminal) osc52Done(rest string) {
	pc, pd, ok := strings.Cut(rest, ";")
	if !ok {
		return
	}
	sel, ok := osc52Sel(pc)
	if !ok {
		return
	}
	if pd == "?" {
		if t.OnClipboard != nil {
			t.OnClipboard(sel, "", true)
		}
		return
	}
	if pd == "" {
		return
	}
	text, raw, ok := decodeOSC52(pd)
	if !ok {
		return
	}
	if raw > t.osc52Limit() {
		if t.OnClipboardDrop != nil {
			t.OnClipboardDrop()
		}
		return
	}
	if text == "" || t.OnClipboard == nil {
		return
	}
	t.OnClipboard(sel, text, false)
}

// oscPush buffers one OSC byte. An OSC 52 past the decoded limit is
// marked dropped and not kept; the caller still reads to the end.
func (t *Terminal) oscPush(b byte) {
	if t.oscDrop {
		return
	}
	if !t.osc52 {
		if len(t.osc) >= oscCap {
			return
		}
		t.osc = append(t.osc, b)
		if bytes.HasPrefix(t.osc, []byte("52;")) {
			t.osc52 = true
			t.oscPd = -1
		}
		return
	}
	if t.oscPd < 0 {
		// Still in Pc. A huge Pc is not a clipboard write.
		if len(t.osc) > 64 {
			t.oscDrop = true
			t.osc = nil
			return
		}
		t.osc = append(t.osc, b)
		if b == ';' {
			t.oscPd = 0
		}
		return
	}
	add := 0
	if b != '=' {
		add = 1
	}
	if b64DecodedLen(t.oscPd+add) > t.osc52Limit() {
		t.oscDrop = true
		t.osc = nil
		return
	}
	t.oscPd += add
	t.osc = append(t.osc, b)
}

// dcsOSCKind is the OSC number at the start of a tmux passthrough
// payload, once enough bytes have arrived to know it.
func dcsOSCKind(b []byte) (kind string, done bool) {
	if len(b) == 0 {
		return "", false
	}
	if b[0] != 0x1b {
		return "", true
	}
	if len(b) < 2 {
		return "", false
	}
	if b[1] != ']' {
		return "", true
	}
	i := 2
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		i++
	}
	if i == len(b) {
		return "", i > 8
	}
	if i == 2 || (b[i] != ';' && b[i] != 0x07) {
		return "", true
	}
	return string(b[2:i]), true
}

func (t *Terminal) dcsPush(b byte) {
	if t.dcsSkip {
		return
	}
	if !t.dcsTmux {
		t.dcs = append(t.dcs, b)
		if len(t.dcs) < 5 {
			return
		}
		if bytes.Equal(t.dcs[:5], []byte("tmux;")) {
			t.dcsTmux = true
			t.dcs = t.dcs[:0]
			return
		}
		// Not tmux: ignore the rest, the way every DCS was ignored.
		t.dcsSkip = true
		t.dcs = nil
		t.state = sString
		return
	}
	t.dcs = append(t.dcs, b)
	if !t.dcsChecked {
		kind, done := dcsOSCKind(t.dcs)
		if !done {
			if len(t.dcs) > 64 {
				t.dcsSkip = true
				t.dcs = nil
			}
			return
		}
		t.dcsChecked = true
		switch kind {
		case "52":
			t.dcsIs52 = true
			t.dcsPd = -1
		case "0", "2":
		default:
			t.dcsSkip = true
			t.dcs = nil
			return
		}
	}
	if !t.dcsIs52 {
		if len(t.dcs) > oscCap {
			t.dcsSkip = true
			t.dcs = nil
		}
		return
	}
	if t.dcsPd < 0 {
		// The header is short; the second ';' starts the base64.
		if b == ';' && bytes.Count(t.dcs, []byte{';'}) >= 2 {
			t.dcsPd = 0
		}
		return
	}
	if b != '=' {
		t.dcsPd++
	}
	if b64DecodedLen(t.dcsPd) > t.osc52Limit() {
		t.dcsSkip = true
		t.dcsTooBig = true
		t.dcs = nil
	}
}

func (t *Terminal) resetDCS() {
	t.dcs = nil
	t.dcsTmux = false
	t.dcsSkip = false
	t.dcsTooBig = false
	t.dcsChecked = false
	t.dcsIs52 = false
	t.dcsPd = -1
}

func (t *Terminal) finishDCS() {
	inner := t.dcs
	tmux, skip, tooBig := t.dcsTmux, t.dcsSkip, t.dcsTooBig
	t.resetDCS()
	t.state = sGround
	if !tmux {
		return
	}
	if tooBig {
		if t.OnClipboardDrop != nil {
			t.OnClipboardDrop()
		}
		return
	}
	if skip || len(inner) == 0 {
		return
	}
	t.feedPassthrough(inner)
}

// feedPassthrough parses a tmux passthrough payload. Only OSC 52 and
// OSC 0/2 take effect; anything else is consumed and ignored.
func (t *Terminal) feedPassthrough(p []byte) {
	t.onlyOSC = true
	t.state = sGround
	for _, b := range p {
		t.byte(b)
	}
	t.onlyOSC = false
	t.state = sGround
}
