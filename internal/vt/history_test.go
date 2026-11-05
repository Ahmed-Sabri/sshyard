package vt

import (
	"fmt"
	"strings"
	"testing"
)

// TestScrollbackRing: once full, the scrollback drops its oldest line for
// each new one, keeping the order and the absolute line numbers.
func TestScrollbackRing(t *testing.T) {
	term := New(20, 3)
	term.SetScrollback(5)
	for i := range 20 {
		fmt.Fprintf(term, "line %d\r\n", i)
	}
	s := term.Snapshot(5)
	if s.Scrollback != 5 {
		t.Fatalf("scrollback %d, want 5", s.Scrollback)
	}
	// 20 lines and the prompt's: lines 0–12 went, 13–17 are above the
	// screen, which shows 18, 19 and the empty last.
	for i, l := range s.Lines {
		if got, want := l.String(), fmt.Sprintf("line %d", 13+i); got != want {
			t.Errorf("row %d of the top: %q, want %q", i, got, want)
		}
	}
	if s.First != 13 {
		t.Errorf("first line %d, want 13", s.First)
	}
	if got := term.Text(Pos{Line: 13}, Pos{Line: 14, Col: 19}); got != "line 13\nline 14" {
		t.Errorf("text %q", got)
	}
	if ms := term.Find("line 1", false); len(ms) == 0 || ms[0].Line != 13 {
		t.Errorf("find %v", ms)
	}
	// A longer limit keeps them, a shorter one trims the oldest.
	term.SetScrollback(10)
	fmt.Fprintf(term, "line 20\r\n")
	if s := term.Snapshot(100); s.Scrollback != 6 || s.Lines[0].String() != "line 13" {
		t.Errorf("after growing: %d lines, first %q", s.Scrollback, s.Lines[0].String())
	}
	term.SetScrollback(2)
	if s := term.Snapshot(100); s.Scrollback != 2 || s.Lines[0].String() != "line 17" || s.First != 17 {
		t.Errorf("after shrinking: %d lines, first %q at %d", s.Scrollback, s.Lines[0].String(), s.First)
	}
}

// TestScrollbackCompact: the scrollback keeps a short line without the
// blanks after it, and shows it as wide as the screen, with a colored
// erase kept.
func TestScrollbackCompact(t *testing.T) {
	term := New(80, 2)
	term.WriteString("short\r\n\x1b[41m\x1b[Kred\x1b[0m\r\n\r\n")
	term.mu.Lock()
	short, red := term.scrollback.at(0), term.scrollback.at(1)
	term.mu.Unlock()
	if len(short.Cells) != 5 {
		t.Errorf("short line kept %d cells, want 5", len(short.Cells))
	}
	if len(red.Cells) != 80 {
		t.Errorf("a line erased in red kept %d cells, want 80", len(red.Cells))
	}
	s := term.Snapshot(2)
	for i, l := range s.Lines {
		if len(l.Cells) != 80 {
			t.Errorf("row %d has %d cells, want 80", i, len(l.Cells))
		}
	}
	if s.Lines[0].String() != "short" {
		t.Errorf("row 0 %q", s.Lines[0].String())
	}
	// Coming back to the screen, the line is full again.
	term.Resize(80, 6)
	term.WriteString("\x1b[1;7HX")
	if got := term.Snapshot(0).Lines[0].String(); got != "short X" {
		t.Errorf("line back on the screen %q", got)
	}
}

// TestSnapshotIntoReuses: a snapshot taken into another reuses its cells.
func TestSnapshotIntoReuses(t *testing.T) {
	term := New(40, 4)
	term.WriteString("hello")
	var s Snapshot
	term.SnapshotInto(&s, 0)
	first := &s.Lines[0].Cells[0]
	term.WriteString(" world")
	term.SnapshotInto(&s, 0)
	if &s.Lines[0].Cells[0] != first {
		t.Error("the second snapshot did not reuse the cells")
	}
	if s.Lines[0].String() != "hello world" {
		t.Errorf("row 0 %q", s.Lines[0].String())
	}
	if allocs := testing.AllocsPerRun(10, func() { term.SnapshotInto(&s, 0) }); allocs != 0 {
		t.Errorf("%v allocations a snapshot, want 0", allocs)
	}
	if x, y := term.Cursor(0); x != s.CursorX || y != s.CursorY {
		t.Errorf("cursor (%d, %d), snapshot's (%d, %d)", x, y, s.CursorX, s.CursorY)
	}
}

// TestCombInterned: the same sequence is the same Comb, and a cell holds
// no pointer.
func TestCombInterned(t *testing.T) {
	a, b := CombOf("́"), CombOf("́")
	if a == 0 || a != b || a.String() != "́" {
		t.Fatalf("comb %d %d %q", a, b, a.String())
	}
	if CombOf("") != 0 || Comb(0).String() != "" {
		t.Error("the empty comb")
	}
	term := New(10, 1)
	term.WriteString(strings.Repeat("é", 3))
	if got := term.ScreenText(); got != strings.Repeat("é", 3) {
		t.Errorf("screen %q", got)
	}
}

func BenchmarkWriteFullScrollback(b *testing.B) {
	term := New(200, 60)
	line := "\x1b[32mINFO\x1b[0m 2026-11-05 12:00:00 request ok path=/api/v1/items latency=12ms\r\n"
	term.WriteString(strings.Repeat(line, DefaultScrollback+100))
	chunk := []byte(strings.Repeat(line, 100))
	b.SetBytes(int64(len(chunk)))
	b.ReportAllocs()
	for b.Loop() {
		term.Write(chunk)
	}
}

func BenchmarkSnapshotInto(b *testing.B) {
	term := New(200, 60)
	term.WriteString(strings.Repeat("\x1b[32mINFO\x1b[0m hello 中文\r\n", 200))
	var s Snapshot
	b.ReportAllocs()
	for b.Loop() {
		term.SnapshotInto(&s, 10)
	}
}
