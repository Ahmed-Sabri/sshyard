package vt

import (
	"strings"
	"testing"
)

func rows(t *Terminal) []string {
	s := t.Snapshot(0)
	out := make([]string, len(s.Lines))
	for i, l := range s.Lines {
		out[i] = l.String()
	}
	return out
}

func cell(t *Terminal, x, y int) Cell { return t.Snapshot(0).Lines[y].Cells[x] }

func cursorAt(tb testing.TB, t *Terminal, x, y int) {
	tb.Helper()
	s := t.Snapshot(0)
	if s.CursorX != x || s.CursorY != y {
		tb.Fatalf("cursor at %d,%d, want %d,%d", s.CursorX, s.CursorY, x, y)
	}
}

func TestPrintAndWrap(t *testing.T) {
	term := New(5, 3)
	term.WriteString("hello world")
	got := rows(term)
	if got[0] != "hello" || got[1] != " worl" || got[2] != "d" {
		t.Fatalf("%q", got)
	}
	if !term.Snapshot(0).Lines[0].Wrapped {
		t.Error("first line not marked wrapped")
	}
	cursorAt(t, term, 1, 2)
	// Writing at the last column defers the wrap.
	term = New(5, 2)
	term.WriteString("abcde")
	cursorAt(t, term, 4, 0)
	term.WriteString("\r\n")
	cursorAt(t, term, 0, 1)
	if rows(term)[1] != "" {
		t.Error("CR LF at the margin wrapped twice")
	}
}

func TestControlsAndCursor(t *testing.T) {
	term := New(20, 5)
	term.WriteString("abc\bX\r\nline2\tT")
	got := rows(term)
	if got[0] != "abX" || got[1] != "line2   T" {
		t.Fatalf("%q", got)
	}
	term.WriteString("\x1b[3;4HZ")
	if rows(term)[2] != "   Z" {
		t.Errorf("CUP: %q", rows(term)[2])
	}
	term.WriteString("\x1b[2A\x1b[3D")
	cursorAt(t, term, 1, 0)
	term.WriteString("\x1b[10C")
	cursorAt(t, term, 11, 0)
	term.WriteString("\x1b[99;99H")
	cursorAt(t, term, 19, 4)
	term.WriteString("\x1b[2;5f\x1b7\x1b[H\x1b8")
	cursorAt(t, term, 4, 1)
	term.WriteString("\x1b[G\x1b[3d")
	cursorAt(t, term, 0, 2)
}

func TestUTF8SplitAcrossWrites(t *testing.T) {
	term := New(10, 1)
	b := []byte("é中")
	for _, c := range b {
		term.Write([]byte{c})
	}
	if got := rows(term)[0]; got != "é中" {
		t.Fatalf("%q", got)
	}
}

func TestWideCharacters(t *testing.T) {
	term := New(5, 2)
	term.WriteString("中文字")
	got := rows(term)
	if got[0] != "中文" || got[1] != "字" {
		t.Fatalf("%q", got)
	}
	if c := cell(term, 0, 0); c.Width != 2 || c.Rune != '中' {
		t.Errorf("%+v", c)
	}
	if c := cell(term, 1, 0); c.Width != 0 {
		t.Errorf("continuation %+v", c)
	}
	// Overwriting half of a wide character erases the whole of it.
	term.WriteString("\x1b[1;2Hx")
	if got := rows(term)[0]; got != " x文" {
		t.Errorf("%q", got)
	}
	cursorAt(t, term, 2, 0)
}

func TestErase(t *testing.T) {
	term := New(6, 3)
	term.WriteString("aaaaaa\r\nbbbbbb\r\ncccccc")
	term.WriteString("\x1b[2;3H\x1b[K")
	if rows(term)[1] != "bb" {
		t.Errorf("EL0 %q", rows(term)[1])
	}
	term.WriteString("\x1b[1K")
	if rows(term)[1] != "" {
		t.Errorf("EL1 %q", rows(term)[1])
	}
	term.WriteString("\x1b[J")
	if got := rows(term); got[0] != "aaaaaa" || got[2] != "" {
		t.Errorf("ED0 %q", got)
	}
	term.WriteString("\x1b[2J")
	if strings.Join(rows(term), "") != "" {
		t.Error("ED2")
	}
	term.WriteString("\x1b[1;1Habcdef\x1b[1;2H\x1b[2P")
	if rows(term)[0] != "adef" {
		t.Errorf("DCH %q", rows(term)[0])
	}
	term.WriteString("\x1b[2@")
	if rows(term)[0] != "a  def" {
		t.Errorf("ICH %q", rows(term)[0])
	}
	term.WriteString("\x1b[3X")
	if rows(term)[0] != "a   ef" {
		t.Errorf("ECH %q", rows(term)[0])
	}
}

func TestBackgroundColorErase(t *testing.T) {
	term := New(4, 2)
	term.WriteString("\x1b[44m\x1b[2J")
	if c := cell(term, 3, 1); c.BG != Indexed(4) {
		t.Errorf("erase kept bg %v", c.BG)
	}
}

func TestSGR(t *testing.T) {
	term := New(20, 1)
	term.WriteString("\x1b[1;3;4;31;42ma\x1b[0mb\x1b[38;5;208;48;2;10;20;30mc\x1b[38:2::1:2:3md\x1b[94;7me\x1b[22;27;39mf")
	a, b, c, d, e, f := cell(term, 0, 0), cell(term, 1, 0), cell(term, 2, 0), cell(term, 3, 0), cell(term, 4, 0), cell(term, 5, 0)
	if a.Attr != Bold|Italic|Underline || a.FG != Indexed(1) || a.BG != Indexed(2) {
		t.Errorf("a %+v", a)
	}
	if b.Attr != 0 || !b.FG.IsDefault() || !b.BG.IsDefault() {
		t.Errorf("b %+v", b)
	}
	if c.FG != Indexed(208) || c.BG != RGB(10, 20, 30) {
		t.Errorf("c %+v", c)
	}
	if r, g, bb, ok := d.FG.RGB(); !ok || r != 1 || g != 2 || bb != 3 {
		t.Errorf("d colon truecolor %+v", d)
	}
	if e.FG != Indexed(12) || e.Attr&Inverse == 0 {
		t.Errorf("e %+v", e)
	}
	if f.Attr&(Inverse|Bold) != 0 || !f.FG.IsDefault() || f.BG != RGB(10, 20, 30) {
		t.Errorf("f %+v", f)
	}
}

func TestScrollRegion(t *testing.T) {
	term := New(4, 4)
	term.WriteString("1\r\n2\r\n3\r\n4")
	term.WriteString("\x1b[2;3r") // rows 2-3; DECSTBM homes the cursor
	cursorAt(t, term, 0, 0)
	term.WriteString("\x1b[3;1H\n")
	if got := rows(term); got[0] != "1" || got[1] != "3" || got[2] != "" || got[3] != "4" {
		t.Errorf("scroll in region %q", got)
	}
	if s := term.Snapshot(0); s.Scrollback != 0 {
		t.Error("region scroll went to scrollback")
	}
	term.WriteString("\x1b[2;1H\x1bM")
	if got := rows(term); got[1] != "" || got[2] != "3" {
		t.Errorf("reverse index %q", got)
	}
	term.WriteString("\x1b[r\x1b[1;1H\x1b[L")
	if got := rows(term); got[0] != "" || got[1] != "1" {
		t.Errorf("IL %q", got)
	}
	term.WriteString("\x1b[M")
	if got := rows(term); got[0] != "1" {
		t.Errorf("DL %q", got)
	}
}

func TestScrollback(t *testing.T) {
	term := New(10, 3)
	term.SetScrollback(5)
	for i := range 10 {
		term.WriteString("line" + string(rune('0'+i)) + "\r\n")
	}
	s := term.Snapshot(0)
	if s.Scrollback != 5 {
		t.Fatalf("scrollback %d", s.Scrollback)
	}
	up := term.Snapshot(2)
	if up.Lines[0].String() != "line6" || up.Offset != 2 || up.CursorVisible {
		t.Errorf("scrolled view %q %+v", up.Lines[0].String(), up.Offset)
	}
	if term.Snapshot(99).Offset != 5 {
		t.Error("offset not clamped")
	}
	top := term.Snapshot(5)
	if top.Lines[0].String() != "line3" || top.First != 3 {
		t.Errorf("top %q first %d", top.Lines[0].String(), top.First)
	}
	term.WriteString("\x1b[3J")
	if term.Snapshot(0).Scrollback != 0 {
		t.Error("ED3")
	}
}

func TestAltScreen(t *testing.T) {
	term := New(10, 3)
	term.WriteString("shell$ ")
	term.WriteString("\x1b[?1049h\x1b[Hvim")
	if !term.Modes().AltScreen || rows(term)[0] != "vim" {
		t.Fatalf("alt %q", rows(term))
	}
	for range 5 {
		term.WriteString("\r\n")
	}
	if term.Snapshot(0).Scrollback != 0 {
		t.Error("alt screen fed scrollback")
	}
	term.WriteString("\x1b[?1049l")
	if rows(term)[0] != "shell$" {
		t.Errorf("primary %q", rows(term))
	}
	cursorAt(t, term, 7, 0)
}

func TestModesAndReplies(t *testing.T) {
	term := New(10, 5)
	var replies []string
	term.Reply = func(b []byte) { replies = append(replies, string(b)) }
	term.WriteString("\x1b[?1h\x1b[?2004h\x1b[?25l\x1b[3;4H\x1b[6n\x1b[5n\x1b[c\x1b[5 q")
	m := term.Modes()
	if !m.AppCursorKeys || !m.BracketedPaste || m.CursorVisible {
		t.Errorf("%+v", m)
	}
	if len(replies) != 3 || replies[0] != "\x1b[3;4R" || replies[1] != "\x1b[0n" || !strings.HasPrefix(replies[2], "\x1b[?") {
		t.Errorf("%q", replies)
	}
	if term.Snapshot(0).CursorStyle != CursorBar {
		t.Error("DECSCUSR")
	}
	term.WriteString("\x1b]0;my title\x07\x1b]2;other\x1b\\")
	if term.Title() != "other" {
		t.Errorf("title %q", term.Title())
	}
	term.WriteString("\x1bP+q544e\x1b\\ok")
	if !strings.HasPrefix(rows(term)[2], "   ok") {
		t.Errorf("DCS leaked: %q", rows(term))
	}
}

func TestDECGraphics(t *testing.T) {
	term := New(10, 1)
	term.WriteString("\x1b(0lqk\x1b(Bq")
	if got := rows(term)[0]; got != "┌─┐q" {
		t.Errorf("%q", got)
	}
}

func TestResize(t *testing.T) {
	term := New(10, 4)
	term.WriteString("a\r\nb\r\nc\r\nd")
	term.Resize(5, 2)
	if got := rows(term); got[0] != "c" || got[1] != "d" {
		t.Errorf("shrink %q", got)
	}
	cursorAt(t, term, 1, 1)
	if term.Snapshot(0).Scrollback != 2 {
		t.Error("shrunk lines not in scrollback")
	}
	term.Resize(12, 4)
	if got := rows(term); got[0] != "a" || got[3] != "d" {
		t.Errorf("grow %q", got)
	}
	cursorAt(t, term, 1, 3)
	if c, r := term.Size(); c != 12 || r != 4 {
		t.Error("size")
	}
	if len(term.Snapshot(0).Lines[0].Cells) != 12 {
		t.Error("lines not padded")
	}
}

func TestTextAndFind(t *testing.T) {
	term := New(6, 4)
	term.WriteString("Hello World\r\nfoo hello")
	s := term.Snapshot(0)
	if got := term.Text(Pos{s.First, 0}, Pos{s.First + 1, 4}); got != "Hello World" {
		t.Errorf("wrapped text %q", got)
	}
	if got := term.Text(Pos{s.First + 2, 0}, Pos{s.First, 0}); !strings.HasPrefix(got, "H") {
		t.Errorf("reversed %q", got)
	}
	ms := term.Find("hello", false)
	if len(ms) != 1 { // the second hello wraps: "foo he" / "llo"
		t.Errorf("matches %+v", ms)
	}
	term2 := New(20, 2)
	term2.WriteString("中文 abc ABC")
	ms = term2.Find("abc", false)
	if len(ms) != 2 || ms[0].Col != 5 || ms[0].Len != 3 {
		t.Errorf("%+v", ms)
	}
	if ms := term2.Find("abc", true); len(ms) != 1 {
		t.Errorf("case sensitive %+v", ms)
	}
	if ms := term2.Find("文", false); len(ms) != 1 || ms[0].Col != 2 || ms[0].Len != 2 {
		t.Errorf("wide %+v", ms)
	}
	a, b := term2.WordAt(Pos{term2.Snapshot(0).First, 6})
	if a.Col != 5 || b.Col != 7 {
		t.Errorf("word %v %v", a, b)
	}
}

func TestOffsetFor(t *testing.T) {
	term := New(10, 4)
	for range 20 {
		term.WriteString("x\r\n")
	}
	s := term.Snapshot(0)
	if term.OffsetFor(s.First+1, 0) != 0 {
		t.Error("visible line moved the view")
	}
	off := term.OffsetFor(2, 0)
	v := term.Snapshot(off)
	if 2 < v.First || 2 >= v.First+v.Rows {
		t.Errorf("line 2 not in view at offset %d (first %d)", off, v.First)
	}
}

func TestGarbageDoesNotPanic(t *testing.T) {
	term := New(7, 3)
	inputs := []string{
		"\x1b[", "\x1b[?", "\x1b[99999999999999m", "\x1b[;;;;;H", "\x1b]", "\xff\xfe",
		"\x1b[38;2m", "\x1b[38;5m", "\x1b[0;0r", "\x1b[5;2r", "\x1b(", "\x1b#8", "\x1b[200@",
		"\x1b[200P", "\x1b[200L", "\x1b[200M", "\x1b[200S", "\x1b[200T", "\x1b[999b",
	}
	for _, in := range inputs {
		term.WriteString(in)
		term.WriteString("ok中")
	}
	for range 50 {
		term.Resize(1+len(inputs)%7, 2)
		term.WriteString("中文 wraps\r\n")
		term.Resize(9, 4)
	}
}

func BenchmarkWrite(b *testing.B) {
	term := New(200, 60)
	chunk := []byte(strings.Repeat("\x1b[32mgreen\x1b[0m text 中文 \x1b[38;2;10;20;30mrgb\x1b[0m\r\n", 100))
	b.SetBytes(int64(len(chunk)))
	for b.Loop() {
		term.Write(chunk)
	}
}

func TestCwdReports(t *testing.T) {
	term := New(20, 3)
	if term.Cwd() != "" {
		t.Fatal("cwd before a report")
	}
	for _, c := range []struct{ in, want, host string }{
		{"\x1b]7;file://box/var/log\x07", "/var/log", "box"},
		{"\x1b]7;file:///srv/My%20Files\x1b\\", "/srv/My Files", ""},
		{"\x1b]7;file://box/tmp/50%\x07", "/tmp/50%", "box"}, // raw, not encoded
		{"\x1b]7;not a url\x07", "/tmp/50%", "box"},
		{"\x1b]1337;CurrentDir=/home/demo\x07", "/home/demo", ""},
		{"\x1b]1337;RemoteHost=demo@web1\x07", "/home/demo", ""},
		{"\x1b]1337;CurrentDir=/srv\x07", "/srv", "web1"},
		{"\x1bc", "/srv", "web1"}, // survives a reset
	} {
		term.WriteString(c.in)
		if got, host := term.Cwd(), term.CwdHost(); got != c.want || host != c.host {
			t.Errorf("after %q: cwd %q on %q, want %q on %q", c.in, got, host, c.want, c.host)
		}
	}
}
