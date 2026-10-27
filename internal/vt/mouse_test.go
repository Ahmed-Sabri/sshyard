package vt

import (
	"strings"
	"testing"
)

func modesAfter(seq string) Modes {
	t := New(80, 24)
	t.WriteString(seq)
	return t.Modes()
}

func TestMouseModes(t *testing.T) {
	for _, c := range []struct {
		seq  string
		want MouseMode
	}{
		{"\x1b[?9h", MouseX10},
		{"\x1b[?1000h", MouseNormal},
		{"\x1b[?1002h", MouseButton},
		{"\x1b[?1003h", MouseAny},
		{"\x1b[?1000h\x1b[?1002h", MouseButton},
		{"\x1b[?1002h\x1b[?1002l", MouseOff},
		{"\x1b[?1003h\x1b[?1000l", MouseOff}, // any reset turns tracking off
		{"\x1b[?1000h\x1b[?1003l", MouseOff},
		{"\x1b[?1002h\x1b[?9l", MouseOff},
		{"\x1b[?9h\x1b[?1002l", MouseOff},
		{"\x1b[?1003h\x1b[?1006l", MouseAny}, // 1006 is the encoding, not tracking
		{"\x1b[?1000;1006h", MouseNormal},
		{"\x1b[?1003h\x1bc", MouseOff}, // RIS
	} {
		m := modesAfter(c.seq)
		if m.Mouse != c.want || m.MouseTracking != (c.want != MouseOff) {
			t.Errorf("%q: mouse %v tracking %v, want %v", c.seq, m.Mouse, m.MouseTracking, c.want)
		}
	}
	if m := modesAfter("\x1b[?1006h\x1b[?1004h"); !m.MouseSGR || !m.FocusReport {
		t.Errorf("1006/1004: %+v", m)
	}
	if m := modesAfter("\x1b[?1006h\x1b[?1004h\x1bc"); m.MouseSGR || m.FocusReport {
		t.Errorf("RIS kept %+v", m)
	}
	// 1005 and 1015 are not supported and change nothing.
	if m := modesAfter("\x1b[?1000h\x1b[?1005h\x1b[?1015h"); m.MouseSGR || m.Mouse != MouseNormal {
		t.Errorf("1005/1015: %+v", m)
	}
}

func TestEncodeMouse(t *testing.T) {
	x10 := Modes{Mouse: MouseX10}
	normal := Modes{Mouse: MouseNormal}
	button := Modes{Mouse: MouseButton}
	anyMove := Modes{Mouse: MouseAny}
	sgr := Modes{Mouse: MouseButton, MouseSGR: true}
	press := func(b MouseBtn, col, row int) MouseEvent {
		return MouseEvent{Button: b, Action: MousePress, Col: col, Row: row}
	}
	for i, c := range []struct {
		m    Modes
		ev   MouseEvent
		want string
	}{
		{Modes{}, press(MouseLeft, 0, 0), ""},
		// default encoding: ESC [ M, button+32, col+33, row+33
		{normal, press(MouseLeft, 0, 0), "\x1b[M !!"},
		{normal, press(MouseMiddle, 9, 4), "\x1b[M!*%"},
		{normal, press(MouseRight, 1, 1), "\x1b[M\"\"\""},
		{normal, MouseEvent{Button: MouseRight, Action: MouseRelease, Col: 1, Row: 1}, "\x1b[M#\"\""},
		{normal, MouseEvent{Button: MouseLeft, Action: MouseMotion, Col: 1, Row: 1}, ""},
		{normal, press(MouseWheelUp, 0, 0), "\x1b[M`!!"},
		{normal, press(MouseWheelDown, 0, 0), "\x1b[Ma!!"},
		{normal, MouseEvent{Button: MouseLeft, Action: MousePress, Shift: true, Alt: true, Ctrl: true}, "\x1b[M< !!"[:3] + string(rune(32+4+8+16)) + "!!"},
		// X10: presses only, no modifiers
		{x10, press(MouseLeft, 0, 0), "\x1b[M !!"},
		{x10, MouseEvent{Button: MouseLeft, Action: MouseRelease}, ""},
		{x10, MouseEvent{Button: MouseLeft, Action: MousePress, Ctrl: true}, "\x1b[M !!"},
		// 1002: moves with a button held, not without
		{button, MouseEvent{Button: MouseLeft, Action: MouseMotion, Col: 2, Row: 0}, "\x1b[M@#!"},
		{button, MouseEvent{Button: MouseNone, Action: MouseMotion, Col: 2, Row: 0}, ""},
		// 1003: every move
		{anyMove, MouseEvent{Button: MouseNone, Action: MouseMotion, Col: 2, Row: 0}, "\x1b[MC#!"},
		// SGR
		{sgr, press(MouseLeft, 0, 0), "\x1b[<0;1;1M"},
		{sgr, MouseEvent{Button: MouseRight, Action: MouseRelease, Col: 4, Row: 2}, "\x1b[<2;5;3m"},
		{sgr, MouseEvent{Button: MouseLeft, Action: MouseMotion, Col: 4, Row: 2, Ctrl: true}, "\x1b[<48;5;3M"},
		{sgr, press(MouseWheelUp, 0, 0), "\x1b[<64;1;1M"},
		{sgr, press(MouseWheelDown, 0, 0), "\x1b[<65;1;1M"},
		{sgr, MouseEvent{Button: MouseWheelUp, Action: MouseRelease}, ""},
		// past column 223: SGR says it, the default encoding cannot
		{sgr, press(MouseLeft, 299, 0), "\x1b[<0;300;1M"},
		{normal, press(MouseLeft, 222, 0), "\x1b[M \xff!"},
		{normal, press(MouseLeft, 223, 0), ""},
	} {
		if got := string(EncodeMouse(c.m, c.ev)); got != c.want {
			t.Errorf("%d: %+v under %+v: %q, want %q", i, c.ev, c.m, got, c.want)
		}
	}
}

func TestEncodeFocus(t *testing.T) {
	if EncodeFocus(Modes{}, true) != nil {
		t.Error("focus reported without 1004")
	}
	m := Modes{FocusReport: true}
	if string(EncodeFocus(m, true)) != "\x1b[I" || string(EncodeFocus(m, false)) != "\x1b[O" {
		t.Error("focus sequences")
	}
}

func TestResetInputModes(t *testing.T) {
	term := New(20, 4)
	term.WriteString("\x1b[?1049h\x1b[?1003;1006;1004;2004;1h\x1b=vim")
	term.ResetInputModes(false)
	m := term.Modes()
	if m.Mouse != MouseOff || m.MouseTracking || m.MouseSGR || m.FocusReport || m.BracketedPaste || m.AppCursorKeys || m.AppKeypad {
		t.Errorf("not reset: %+v", m)
	}
	if !m.AltScreen {
		t.Error("left the alternate screen without leaveAlt")
	}
	term.ResetInputModes(true)
	if term.Modes().AltScreen {
		t.Error("still on the alternate screen")
	}
}

func TestCombiningIntoCell(t *testing.T) {
	term := New(20, 3)
	in := "e\u0301 n\u0303 👍🏽\r\n"
	term.WriteString(in)
	if c := cell(term, 0, 0); c.Rune != 'e' || c.Comb != "\u0301" {
		t.Errorf("é: %+v", c)
	}
	if c := cell(term, 2, 0); c.Rune != 'n' || c.Comb != "\u0303" {
		t.Errorf("ñ: %+v", c)
	}
	if c := cell(term, 4, 0); c.Rune != '👍' || c.Comb != "🏽" || c.Width != 2 {
		t.Errorf("👍🏽: %+v", c)
	}
	// nothing went past where it belongs
	if c := cell(term, 6, 0); c.Rune != 0 && c.Rune != ' ' {
		t.Errorf("cell after the emoji: %+v", c)
	}
	cursorAt(t, term, 0, 1)
	if got := rows(term)[0]; got != strings.TrimSuffix(in, "\r\n") {
		t.Errorf("line %q", got)
	}
	got := term.Text(Pos{Line: 0, Col: 0}, Pos{Line: 0, Col: 19})
	if got != "e\u0301 n\u0303 👍🏽" {
		t.Errorf("copied %q, % x", got, got)
	}
}

func TestCombiningKinds(t *testing.T) {
	term := New(20, 6)
	// variation selector 16, a ZWJ family, a mark at the last column, a
	// mark with nothing before it.
	term.WriteString("❤\ufe0f\r\n👨\u200d👩\u200d👧x\r\n\u0301a\r\n")
	if c := cell(term, 0, 0); c.Comb != "\ufe0f" {
		t.Errorf("VS16: %+v", c)
	}
	if c := cell(term, 0, 1); c.Rune != '👨' || c.Comb != "\u200d👩\u200d👧" {
		t.Errorf("ZWJ: %+v", c)
	}
	if c := cell(term, 2, 1); c.Rune != 'x' {
		t.Errorf("after the family: %+v", c)
	}
	if c := cell(term, 0, 2); c.Rune != 'a' || c.Comb != "" {
		t.Errorf("a lone mark: %+v", c)
	}
	term = New(3, 2)
	term.WriteString("abc\u0301d")
	if c := cell(term, 2, 0); c.Rune != 'c' || c.Comb != "\u0301" {
		t.Errorf("mark at the last column: %+v", c)
	}
	if c := cell(term, 0, 1); c.Rune != 'd' {
		t.Errorf("wrapped: %+v", c)
	}
	// a skin tone after a letter is a character of its own
	term = New(10, 2)
	term.WriteString("a🏽")
	if c := cell(term, 1, 0); c.Rune != '🏽' {
		t.Errorf("lone modifier: %+v", c)
	}
	// regional indicators pair into one wide flag cell over the two
	// columns the program counts; a third stands alone in one column.
	term = New(10, 2)
	term.WriteString("\U0001F1EF\U0001F1F5\U0001F1FAx")
	if c := cell(term, 0, 0); c.Rune != 0x1f1ef || c.Comb != "\U0001F1F5" || c.Width != 2 {
		t.Errorf("flag: %+v", c)
	}
	if c := cell(term, 2, 0); c.Rune != 0x1f1fa || c.Comb != "" || c.Width != 1 {
		t.Errorf("third indicator: %+v", c)
	}
	if c := cell(term, 3, 0); c.Rune != 'x' {
		t.Errorf("after the flags: %+v", c)
	}
	// a pair split by the right margin is not joined across lines
	term = New(3, 2)
	term.WriteString("ab\U0001F1EF\U0001F1F5")
	if c := cell(term, 2, 0); c.Comb != "" || c.Width != 1 {
		t.Errorf("at the margin: %+v", c)
	}
	if c := cell(term, 0, 1); c.Rune != 0x1f1f5 {
		t.Errorf("wrapped letter: %+v", c)
	}
}

func TestFindCombining(t *testing.T) {
	term := New(30, 3)
	term.WriteString("caf\u00e9 and cafe\u0301\r\n")
	for _, q := range []string{"café", "cafe\u0301", "CAFÉ"} {
		ms := term.Find(q, false)
		if len(ms) != 2 {
			t.Fatalf("%q: %v", q, ms)
		}
		if ms[0].Col != 0 || ms[0].Len != 4 || ms[1].Col != 9 || ms[1].Len != 4 {
			t.Errorf("%q: %v", q, ms)
		}
	}
	if ms := term.Find("e", true); len(ms) != 2 { // the e of both cafés
		t.Errorf("e: %v", ms)
	}
}
