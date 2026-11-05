package app

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/highlight"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/vt"
	"github.com/egoist/mygo/ui"
)

// paintTarget is a terminal that takes no input.
type paintTarget struct{ term *vt.Terminal }

func (p paintTarget) Terminal() *vt.Terminal    { return p.term }
func (p paintTarget) Resize(cols, rows int)     { p.term.Resize(cols, rows) }
func (paintTarget) Write(b []byte) (int, error) { return len(b), nil }

// TestRowMarks: the selection and the matches mark the columns of each
// row as they did cell by cell.
func TestRowMarks(t *testing.T) {
	term := vt.New(20, 4)
	term.WriteString("foo bar foo\r\nfoo\r\n")
	v := newTermView(paintTarget{term}, 13)
	v.updateFind("foo", true, time.Time{})
	v.showMatch(1)
	if got := v.rowMatches(0, 20); !slices.Equal(got[:12], []uint8{1, 1, 1, 0, 0, 0, 0, 0, 2, 2, 2, 0}) {
		t.Errorf("row 0 marks %v", got[:12])
	}
	if got := v.rowMatches(1, 20); !slices.Equal(got[:4], []uint8{1, 1, 1, 0}) {
		t.Errorf("row 1 marks %v", got[:4])
	}
	if v.rowMatches(2, 20) != nil {
		t.Error("marks on a row without matches")
	}
	// Output streaming in is searched again at most every findEvery.
	now := time.Now()
	term.WriteString("foo")
	if v.updateFind("foo", true, now); len(v.matches) != 4 {
		t.Errorf("new output not searched: %d matches", len(v.matches))
	}
	term.WriteString(" foo")
	if wait := v.updateFind("foo", true, now.Add(findEvery/3)); wait <= 0 || len(v.matches) != 4 {
		t.Errorf("searched again at once: wait %v, %d matches", wait, len(v.matches))
	}
	if wait := v.updateFind("foo", true, now.Add(findEvery)); wait != 0 || len(v.matches) != 5 {
		t.Errorf("not searched again: wait %v, %d matches", wait, len(v.matches))
	}
	if v.updateFind("bar", true, now.Add(findEvery)); len(v.matches) != 1 {
		t.Errorf("a new query not searched at once: %d matches", len(v.matches))
	}
	v.updateFind("foo", true, now.Add(2*findEvery))

	v.selA, v.selB, v.selected = vt.Pos{Line: 2, Col: 5}, vt.Pos{Line: 0, Col: 4}, true
	for _, c := range []struct{ line, from, to int }{{0, 4, -1}, {1, 0, -1}, {2, 0, 5}} {
		from, to := v.selectedCols(c.line)
		if c.to == -1 && to >= 19 {
			to = -1
		}
		if from != c.from || to != c.to {
			t.Errorf("line %d selects %d–%d, want %d–%d", c.line, from, to, c.from, c.to)
		}
	}
	if from, to := v.selectedCols(3); from <= to {
		t.Errorf("line 3 selects %d–%d", from, to)
	}
}

// BenchmarkTerminalPaint is a frame of a full, colored terminal with a
// selection, a search and the keywords highlighted, as output streams
// into it.
func BenchmarkTerminalPaint(b *testing.B) {
	term := vt.New(150, 50)
	for i := range 200 {
		fmt.Fprintf(term, "\x1b[3%dm2026-11-05 12:00:%02d\x1b[0m INFO request id=%d path=/api/v1/items 中文 status=200\r\n", i%8, i%60, i)
	}
	v := newTermView(paintTarget{term}, 13)
	v.selA, v.selB, v.selected = vt.Pos{Line: 160, Col: 3}, vt.Pos{Line: 170, Col: 40}, true
	v.hl = highlight.New(nil, highlight.PresetIDs())
	tok := theme.DarkTokens()
	pal := &tok.Terminal
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c.Key("t")).Grow(1).FillWidth().Draw(func(p *ui.Painter, r ui.Rect) { v.paint(p, r, pal, true) })
	}, 1400, 900)
	tt.SetScale(2)
	tt.Frame()
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		fmt.Fprintf(term, "x%d", i)
		v.updateFind("request", true, time.Time{}.Add(time.Duration(i)*findEvery))
		tt.Frame()
		i++
	}
}
