package vt

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Pos is a position in the terminal's history: Line is absolute, counting
// every line that ever scrolled into the scrollback, so it stays put as
// output arrives; Col is a cell column.
type Pos struct{ Line, Col int }

// Less orders positions.
func (p Pos) Less(q Pos) bool { return p.Line < q.Line || (p.Line == q.Line && p.Col < q.Col) }

// Snapshot is a copy of what the terminal shows, for drawing.
type Snapshot struct {
	Cols, Rows int
	// Lines are Rows lines from line First of the history.
	Lines []Line
	First int
	// Cursor is where the cursor is on the screen, in Lines' coordinates;
	// CursorVisible is false when the program hid it or it is scrolled
	// out of view.
	CursorX, CursorY int
	CursorVisible    bool
	CursorStyle      CursorStyle
	// Scrollback is how many lines are above the screen; Offset how many
	// of them the view is scrolled up.
	Scrollback, Offset int
	Modes              Modes
}

// Snapshot returns the screen, scrolled up offset lines into the
// scrollback (0 is the live screen).
func (t *Terminal) Snapshot(offset int) Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	sb := 0
	if !t.altActive {
		sb = len(t.scrollback)
	}
	offset = max(0, min(offset, sb))
	s := Snapshot{
		Cols: t.cols, Rows: t.rows,
		Scrollback: sb, Offset: offset,
		CursorStyle: t.style,
		Modes:       t.modes,
	}
	s.Modes.AltScreen = t.altActive
	s.First = t.firstLine() + t.historyLen() - t.rows - offset
	s.Lines = make([]Line, t.rows)
	for y := range t.rows {
		s.Lines[y] = t.lineAt(s.First + y).clone()
	}
	s.CursorX, s.CursorY = t.cur.x, t.cur.y+offset
	s.CursorVisible = t.modes.CursorVisible && s.CursorY < t.rows
	return s
}

// historyLen is the number of lines of history, scrollback and screen.
func (t *Terminal) historyLen() int {
	if t.altActive {
		return t.rows
	}
	return len(t.scrollback) + t.rows
}

// lineAt returns the absolute line n, or an empty line.
func (t *Terminal) lineAt(n int) Line {
	i := n - t.dropped
	if t.altActive {
		i = n - t.dropped - len(t.scrollback)
		if i >= 0 && i < t.rows {
			return t.alt.lines[i]
		}
		return Line{}
	}
	switch {
	case i < 0:
		return Line{}
	case i < len(t.scrollback):
		return t.scrollback[i]
	case i-len(t.scrollback) < t.rows:
		return t.primary.lines[i-len(t.scrollback)]
	}
	return Line{}
}

// firstLine is the absolute number of the oldest line kept.
func (t *Terminal) firstLine() int {
	if t.altActive {
		return t.dropped + len(t.scrollback)
	}
	return t.dropped
}

// Text returns the text from a to b, inclusive of b's cell. Lines that
// wrapped are joined; others end with a newline.
func (t *Terminal) Text(a, b Pos) string {
	if b.Less(a) {
		a, b = b, a
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var sb strings.Builder
	a.Line = max(a.Line, t.firstLine())
	last := t.firstLine() + t.historyLen() - 1
	b.Line = min(b.Line, last)
	for n := a.Line; n <= b.Line; n++ {
		l := t.lineAt(n)
		from, to := 0, len(l.Cells)
		if n == a.Line {
			from = min(a.Col, to)
		}
		if n == b.Line {
			to = min(b.Col+1, to)
		} else if l.Wrapped && l.widePad {
			to = max(from, to-1) // the blank a wide character left
		}
		seg := Line{Cells: l.Cells[from:to]}.text(n == b.Line || !l.Wrapped)
		sb.WriteString(seg)
		if n < b.Line && !l.Wrapped {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// ScreenText returns the text of the screen, one line per row, without
// trailing blank lines: for tests and for copying all.
func (t *Terminal) ScreenText() string {
	s := t.Snapshot(0)
	out := make([]string, len(s.Lines))
	for i, l := range s.Lines {
		out[i] = l.String()
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// Match is a search result: Len cells from Pos.
type Match struct {
	Pos
	Len int
}

// Find returns every match of query in the history, oldest first. It
// ignores case unless caseSensitive. A match does not span lines.
func (t *Terminal) Find(query string, caseSensitive bool) []Match {
	if query == "" {
		return nil
	}
	// Decomposed on both sides, so é typed matches e + U+0301 on screen
	// and the other way round.
	q := []rune(norm.NFD.String(query))
	if !caseSensitive {
		for i := range q {
			q[i] = unicode.ToLower(q[i])
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []Match
	first := t.firstLine()
	for n := first; n < first+t.historyLen(); n++ {
		l := t.lineAt(n)
		// The runes of the line, with the cells each is in.
		rs := make([]rune, 0, len(l.Cells))
		cols := make([]int, 0, len(l.Cells))
		ends := make([]int, 0, len(l.Cells))
		for x, c := range l.Cells {
			if c.Width == 0 {
				continue
			}
			cell := []rune{c.Rune}
			if c.Comb != "" || c.Rune >= 0x80 {
				cell = []rune(norm.NFD.String(c.Text()))
			}
			for _, r := range cell {
				if !caseSensitive {
					r = unicode.ToLower(r)
				}
				rs = append(rs, r)
				cols = append(cols, x)
				ends = append(ends, x+int(c.Width))
			}
		}
		for i := 0; i+len(q) <= len(rs); i++ {
			ok := true
			for j := range q {
				if rs[i+j] != q[j] {
					ok = false
					break
				}
			}
			if ok {
				out = append(out, Match{Pos{n, cols[i]}, ends[i+len(q)-1] - cols[i]})
				i += len(q) - 1
			}
		}
	}
	return out
}

// OffsetFor returns the scroll offset that brings absolute line n into
// view, keeping the view still when it already is.
func (t *Terminal) OffsetFor(n, current int) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.altActive {
		return 0
	}
	sb := len(t.scrollback)
	first := t.dropped + sb - current
	if n >= first && n < first+t.rows {
		return current
	}
	off := t.dropped + sb - n + t.rows/2
	return max(0, min(off, sb))
}

// WordAt returns the word around p, for double-click selection.
func (t *Terminal) WordAt(p Pos) (Pos, Pos) {
	t.mu.Lock()
	defer t.mu.Unlock()
	l := t.lineAt(p.Line)
	if p.Col < 0 || p.Col >= len(l.Cells) {
		return p, p
	}
	isWord := func(c Cell) bool {
		return c.Width == 0 || (c.Rune != ' ' && c.Rune != 0 && !strings.ContainsRune("\"'`()[]{}<>|;,", c.Rune))
	}
	if !isWord(l.Cells[p.Col]) {
		return p, p
	}
	a, b := p.Col, p.Col
	for a > 0 && isWord(l.Cells[a-1]) {
		a--
	}
	for b < len(l.Cells)-1 && isWord(l.Cells[b+1]) {
		b++
	}
	return Pos{p.Line, a}, Pos{p.Line, b}
}
