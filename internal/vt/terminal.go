package vt

import (
	"sync"
	"unicode/utf8"
)

// DefaultScrollback is how many lines scroll back by default.
const DefaultScrollback = 10000

// CursorStyle is the cursor's shape, as DECSCUSR sets it.
type CursorStyle int

const (
	CursorBlock CursorStyle = iota
	CursorUnderline
	CursorBar
)

// Modes are the terminal modes the UI needs to know about.
type Modes struct {
	AppCursorKeys  bool // DECCKM: arrows send ESC O A
	AppKeypad      bool
	BracketedPaste bool
	CursorVisible  bool
	AltScreen      bool
	AutoWrap       bool
	Origin         bool
	Insert         bool
	MouseTracking  bool // a mouse mode is on (9/1000/1002/1003)
	Mouse          MouseMode
	MouseSGR       bool // 1006: SGR encoding of mouse reports
	FocusReport    bool // 1004
}

type cursor struct {
	x, y        int
	pen         Cell
	origin      bool
	wrapPending bool
	graphics    bool
}

type screen struct {
	lines []Line
}

// Terminal is a terminal emulator. Write feeds it the output of a
// program; the UI reads it with Snapshot. It is safe for concurrent use.
type Terminal struct {
	mu sync.Mutex

	cols, rows int
	primary    screen
	alt        screen
	altActive  bool

	scrollback    []Line
	maxScrollback int
	// dropped counts the lines that fell off the top of the scrollback,
	// so that absolute line numbers stay stable as it is trimmed.
	dropped int

	cur        cursor
	saved      cursor
	savedAlt   cursor
	top, bot   int // scroll region, inclusive
	tabs       []bool
	modes      Modes
	style      CursorStyle
	title      string
	cwd        string
	cwdHost    string // the host the cwd report named
	remoteHost string // OSC 1337 RemoteHost's host
	g0Graphics bool
	g1Graphics bool
	shiftOut   bool
	lastRune   rune
	joinNext   bool // the last rune printed was ZWJ

	// parser
	csiBytes int
	state    pstate
	params   []int
	sub      []bool // sub[i]: params[i] followed a colon
	inter    []byte
	private  byte
	paramCur int
	nextSub  bool
	hasParam bool
	osc      []byte
	osc52    bool // this OSC started with "52;"
	oscDrop  bool // OSC 52 over the limit: swallow, keep nothing
	oscPd    int  // base64 digits of the OSC 52 payload, -1 until it starts
	escInter byte
	utf8buf  []byte
	// onlyOSC is set while a tmux passthrough payload is parsed: OSC 52
	// and OSC 0/2 apply, and nothing else changes the screen.
	onlyOSC bool
	// DCS, for tmux passthrough (ESC P tmux ; … ST). Other DCS is ignored.
	dcs        []byte
	dcsTmux    bool
	dcsSkip    bool // swallow to ST, do not keep
	dcsTooBig  bool // the swallowed OSC 52 was over the limit
	dcsChecked bool
	dcsIs52    bool
	dcsPd      int // base64 digits of a passed-through OSC 52, -1 until it starts

	// Reply, when set, receives what the terminal answers the program
	// (device status and attributes reports). It is called with the lock
	// held, so it must not call back into the terminal.
	Reply func([]byte)
	// OnClipboard reports an OSC 52. sel is the selection parameter
	// (c, p, s or 0–7); each of those is the system clipboard. data is
	// the text. query is true when the program asked to read the
	// clipboard (Pd is "?"); data is then empty and nothing is answered.
	// Called with the lock held, like Reply.
	OnClipboard func(sel, data string, query bool)
	// OnClipboardDrop reports an OSC 52 bigger than the size limit. The
	// whole sequence is discarded. Called with the lock held.
	OnClipboardDrop func()
	// Bell, when set, is called on BEL.
	Bell func()

	// osc52Max is the largest decoded OSC 52, or 0 for the default.
	osc52Max int

	gen uint64
}

// New returns a terminal of cols×rows cells.
func New(cols, rows int) *Terminal {
	t := &Terminal{maxScrollback: DefaultScrollback}
	t.cols, t.rows = max(cols, 1), max(rows, 1)
	t.reset()
	return t
}

func (t *Terminal) reset() {
	t.primary = screen{lines: t.blankLines(t.rows)}
	t.alt = screen{lines: t.blankLines(t.rows)}
	t.altActive = false
	t.cur = cursor{}
	t.saved = cursor{}
	t.savedAlt = cursor{}
	t.top, t.bot = 0, t.rows-1
	t.modes = Modes{CursorVisible: true, AutoWrap: true}
	t.style = CursorBlock
	t.g0Graphics, t.g1Graphics, t.shiftOut = false, false, false
	t.resetTabs()
	t.onlyOSC = false
	t.osc52, t.oscDrop = false, false
	t.resetDCS()
	t.state = sGround
}

func (t *Terminal) blankLines(n int) []Line {
	ls := make([]Line, n)
	for i := range ls {
		ls[i] = newLine(t.cols, Cell{})
	}
	return ls
}

func (t *Terminal) resetTabs() {
	t.tabs = make([]bool, t.cols)
	for i := 8; i < t.cols; i += 8 {
		t.tabs[i] = true
	}
}

func (t *Terminal) lines() []Line {
	if t.altActive {
		return t.alt.lines
	}
	return t.primary.lines
}

// SetScrollback sets how many lines scroll back.
func (t *Terminal) SetScrollback(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.maxScrollback = max(n, 0)
	t.trimScrollback()
}

func (t *Terminal) trimScrollback() {
	if extra := len(t.scrollback) - t.maxScrollback; extra > 0 {
		t.scrollback = append([]Line(nil), t.scrollback[extra:]...)
		t.dropped += extra
	}
}

// Size returns the terminal's size in cells.
func (t *Terminal) Size() (cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cols, t.rows
}

// Title is the window title the program set (OSC 0 or 2).
func (t *Terminal) Title() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.title
}

// Cwd is the working directory the shell last reported (OSC 7
// file://host/path or OSC 1337 CurrentDir=), or "" if it never did.
func (t *Terminal) Cwd() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cwd
}

// CwdHost is the host the last directory report named: OSC 7's
// file://host/, or for OSC 1337 CurrentDir the host of the last OSC 1337
// RemoteHost=user@host; "" when it named none.
func (t *Terminal) CwdHost() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cwdHost
}

// Modes returns the terminal's modes.
func (t *Terminal) Modes() Modes {
	t.mu.Lock()
	defer t.mu.Unlock()
	m := t.modes
	m.AltScreen = t.altActive
	return m
}

// Generation increases every time the terminal changes.
func (t *Terminal) Generation() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.gen
}

// Resize changes the terminal's size. Lines are cut or padded, not
// reflowed; when the screen shrinks, the lines above the cursor go to the
// scrollback, and when it grows they come back from it.
func (t *Terminal) Resize(cols, rows int) {
	cols, rows = max(cols, 1), max(rows, 1)
	t.mu.Lock()
	defer t.mu.Unlock()
	if cols == t.cols && rows == t.rows {
		return
	}
	t.gen++
	fit := func(ls []Line) []Line {
		for i := range ls {
			ls[i] = fitLine(ls[i], cols)
		}
		return ls
	}
	t.scrollback = fit(t.scrollback)
	resizeScreen := func(s *screen, c *cursor, history bool) {
		ls := fit(s.lines)
		switch {
		case rows < len(ls):
			// Drop blank lines below the cursor first, then push lines
			// off the top.
			for len(ls) > rows && len(ls)-1 > c.y && ls[len(ls)-1].String() == "" {
				ls = ls[:len(ls)-1]
			}
			if n := len(ls) - rows; n > 0 {
				if history {
					t.scrollback = append(t.scrollback, ls[:n]...)
				}
				ls = ls[n:]
				c.y -= n
			}
		case rows > len(ls):
			add := rows - len(ls)
			if history {
				n := min(add, len(t.scrollback))
				back := t.scrollback[len(t.scrollback)-n:]
				t.scrollback = t.scrollback[:len(t.scrollback)-n]
				ls = append(append([]Line(nil), back...), ls...)
				c.y += n
				add -= n
			}
			for range add {
				ls = append(ls, newLine(cols, Cell{}))
			}
		}
		s.lines = ls
		c.x = min(c.x, cols-1)
		c.y = max(0, min(c.y, rows-1))
		c.wrapPending = false
	}
	if t.altActive {
		resizeScreen(&t.primary, &t.saved, true)
		resizeScreen(&t.alt, &t.cur, false)
	} else {
		resizeScreen(&t.primary, &t.cur, true)
		resizeScreen(&t.alt, &t.savedAlt, false)
	}
	t.cols, t.rows = cols, rows
	t.top, t.bot = 0, rows-1
	t.resetTabs()
	t.trimScrollback()
}

func fitLine(l Line, cols int) Line {
	if len(l.Cells) > cols {
		l.Cells = l.Cells[:cols]
		if cols > 0 && l.Cells[cols-1].Width == 2 {
			l.Cells[cols-1] = blank(Cell{})
		}
		l.Wrapped = false
	}
	for len(l.Cells) < cols {
		l.Cells = append(l.Cells, blank(Cell{}))
	}
	return l
}

// Write feeds the terminal a program's output.
func (t *Terminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gen++
	for i := 0; i < len(p); i++ {
		b := p[i]
		if len(t.utf8buf) > 0 || (b >= 0x80 && t.state != sOSC && t.state != sString && t.state != sDCS && t.state != sDCSEsc) {
			if b < 0x80 || (len(t.utf8buf) > 0 && b&0xC0 != 0x80) {
				// An invalid sequence: drop what we had.
				t.utf8buf = t.utf8buf[:0]
				if b >= 0x80 {
					t.utf8buf = append(t.utf8buf, b)
					continue
				}
			} else {
				t.utf8buf = append(t.utf8buf, b)
				if !utf8.FullRune(t.utf8buf) {
					continue
				}
				r, _ := utf8.DecodeRune(t.utf8buf)
				t.utf8buf = t.utf8buf[:0]
				t.rune(r)
				continue
			}
		}
		t.byte(b)
	}
	return len(p), nil
}

// WriteString is Write for a string.
func (t *Terminal) WriteString(s string) { t.Write([]byte(s)) }

type pstate int

const (
	sGround pstate = iota
	sEscape
	sCSI
	sCSIDrop
	sOSC
	sOSCEsc
	sString // SOS, PM, APC, and a DCS that is not tmux: ignored up to ST
	sStringEsc
	sDCS // ESC P, possibly tmux passthrough
	sDCSEsc
	sCharset
)

// rune handles a decoded non-ASCII rune.
func (t *Terminal) rune(r rune) {
	switch t.state {
	case sGround:
		t.print(r)
	case sCSIDrop:
		// An oversized CSI stays discarded even across UTF-8 input.
	case sOSC:
		t.osc = utf8.AppendRune(t.osc, r)
	default:
		// Not part of any sequence: abort it and print.
		t.state = sGround
		t.print(r)
	}
}

func (t *Terminal) byte(b byte) {
	if t.state == sCSIDrop {
		if b >= 0x40 && b <= 0x7e {
			t.state = sGround
		}
		return
	}
	// C0 controls act in the middle of most sequences.
	if b < 0x20 && t.state != sOSC && t.state != sString && t.state != sOSCEsc && t.state != sStringEsc && t.state != sDCS && t.state != sDCSEsc {
		if b == 0x1b {
			t.state = sEscape
			t.escInter = 0
			return
		}
		t.control(b)
		return
	}
	switch t.state {
	case sGround:
		if b == 0x7f {
			return
		}
		t.print(rune(b))
	case sEscape:
		t.escape(b)
	case sCharset:
		g := b == '0'
		if t.escInter == '(' {
			t.g0Graphics = g
		} else if t.escInter == ')' {
			t.g1Graphics = g
		}
		t.state = sGround
	case sCSI:
		t.csiByte(b)
	case sOSC:
		switch b {
		case 0x07:
			t.oscDone()
		case 0x1b:
			t.state = sOSCEsc
		default:
			t.oscPush(b)
		}
	case sOSCEsc:
		if b == '\\' {
			t.oscDone()
		} else {
			t.state = sGround
		}
	case sDCS:
		if b == 0x1b {
			t.state = sDCSEsc
			return
		}
		// BEL ends an ordinary DCS. Inside tmux passthrough it belongs
		// to the wrapped sequence (OSC 52 often ends with BEL).
		if !t.dcsTmux && b == 0x07 {
			t.resetDCS()
			t.state = sGround
			return
		}
		t.dcsPush(b)
	case sDCSEsc:
		switch b {
		case '\\':
			t.finishDCS()
		case 0x1b:
			// tmux writes ESC as ESC ESC, so this is one escaped ESC
			// and not the start of ST.
			if t.dcsTmux {
				t.dcsPush(0x1b)
				t.state = sDCS
			} else {
				t.resetDCS()
				t.state = sString
			}
		default:
			if t.dcsTmux {
				t.dcsPush(0x1b)
				t.state = sDCS
				t.dcsPush(b)
			} else {
				t.resetDCS()
				t.state = sString
			}
		}
	case sString:
		if b == 0x1b {
			t.state = sStringEsc
		} else if b == 0x07 {
			t.state = sGround
		}
	case sStringEsc:
		if b == '\\' {
			t.state = sGround
		} else {
			t.state = sString
		}
	}
}

func (t *Terminal) control(b byte) {
	if t.onlyOSC {
		return
	}
	switch b {
	case 0x07:
		if t.Bell != nil {
			t.Bell()
		}
	case 0x08:
		if t.cur.x > 0 {
			t.cur.x--
		}
		t.cur.wrapPending = false
	case 0x09:
		t.tab(1)
	case 0x0a, 0x0b, 0x0c:
		t.index()
	case 0x0d:
		t.cur.x = 0
		t.cur.wrapPending = false
	case 0x0e:
		t.shiftOut = true
	case 0x0f:
		t.shiftOut = false
	case 0x18, 0x1a:
		t.state = sGround
	}
}

func (t *Terminal) escape(b byte) {
	if t.onlyOSC && b != ']' {
		t.state = sGround
		return
	}
	t.state = sGround
	switch b {
	case '[':
		t.state = sCSI
		t.csiBytes = 0
		t.params = t.params[:0]
		t.sub = t.sub[:0]
		t.inter = t.inter[:0]
		t.private = 0
		t.paramCur = 0
		t.hasParam = false
		t.nextSub = false
	case ']':
		t.state = sOSC
		t.osc = t.osc[:0]
		t.osc52, t.oscDrop = false, false
		t.oscPd = -1
	case 'P':
		t.resetDCS()
		t.state = sDCS
	case 'X', '^', '_':
		t.state = sString
	case '(', ')', '*', '+':
		t.escInter = b
		t.state = sCharset
	case '7':
		t.saveCursor()
	case '8':
		t.restoreCursor()
	case 'D':
		t.index()
	case 'E':
		t.cur.x = 0
		t.index()
	case 'M':
		t.reverseIndex()
	case 'H':
		if t.cur.x < t.cols {
			t.tabs[t.cur.x] = true
		}
	case 'c':
		sb, dropped, title, cwd, cwdHost, remoteHost := t.scrollback, t.dropped, t.title, t.cwd, t.cwdHost, t.remoteHost
		t.reset()
		t.scrollback, t.dropped, t.title, t.cwd, t.cwdHost, t.remoteHost = sb, dropped, title, cwd, cwdHost, remoteHost
	case '=':
		t.modes.AppKeypad = true
	case '>':
		t.modes.AppKeypad = false
	case '#', ' ', '%':
		// ESC # 8 (DECALN) and friends take one more byte.
		t.escInter = b
		t.state = sCharset
	}
}

const (
	maxCSIBytes         = 4096
	maxCSIParams        = 64
	maxCSIIntermediates = 4
)

func (t *Terminal) csiByte(b byte) {
	t.csiBytes++
	if t.csiBytes > maxCSIBytes ||
		((b == ';' || b == ':') && len(t.params) >= maxCSIParams-1) ||
		(b >= 0x20 && b <= 0x2f && len(t.inter) >= maxCSIIntermediates) {
		t.params, t.sub, t.inter = t.params[:0], t.sub[:0], t.inter[:0]
		t.state = sCSIDrop
		if b >= 0x40 && b <= 0x7e {
			t.state = sGround
		}
		return
	}
	switch {
	case b >= '0' && b <= '9':
		t.paramCur = t.paramCur*10 + int(b-'0')
		if t.paramCur > 65535 {
			t.paramCur = 65535
		}
		t.hasParam = true
	case b == ';' || b == ':':
		t.params = append(t.params, t.paramValue())
		t.sub = append(t.sub, t.nextSub)
		t.nextSub = b == ':'
		t.paramCur, t.hasParam = 0, false
	case b >= '<' && b <= '?':
		if len(t.params) == 0 && !t.hasParam {
			t.private = b
		}
	case b >= 0x20 && b <= 0x2f:
		t.inter = append(t.inter, b)
	case b >= 0x40 && b <= 0x7e:
		if t.hasParam || len(t.params) > 0 {
			t.params = append(t.params, t.paramValue())
			t.sub = append(t.sub, t.nextSub)
		}
		t.state = sGround
		t.csi(b)
	default:
		t.state = sGround
	}
}

// paramValue is the parameter being parsed, -1 when it was left empty.
func (t *Terminal) paramValue() int {
	if !t.hasParam {
		return -1
	}
	return t.paramCur
}
