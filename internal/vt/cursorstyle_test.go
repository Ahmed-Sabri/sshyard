package vt

import "testing"

func TestTerminalCursorStyleSequences(t *testing.T) {
	for _, tc := range []struct {
		param string
		want  CursorStyle
		blink bool
	}{
		{"", CursorBlock, true}, {"0", CursorBlock, true}, {"1", CursorBlock, true}, {"2", CursorBlock, false},
		{"3", CursorUnderline, true}, {"4", CursorUnderline, false}, {"5", CursorBar, true}, {"6", CursorBar, false},
	} {
		t.Run("param="+tc.param, func(t *testing.T) {
			term := New(10, 2)
			if s := term.Snapshot(0); s.CursorStyleSet || !s.CursorBlink {
				t.Fatal("new terminal has an explicit cursor style")
			}
			term.WriteString("\x1b[6 q\x1b[" + tc.param + " q")
			snap := term.Snapshot(0)
			if snap.CursorStyle != tc.want || !snap.CursorStyleSet || snap.CursorBlink != tc.blink {
				t.Fatalf("cursor style = %v, explicit = %v, blink = %v; want %v, explicit, blink = %v", snap.CursorStyle, snap.CursorStyleSet, snap.CursorBlink, tc.want, tc.blink)
			}
		})
	}
}

func TestTerminalCursorStyleInvalidAndReset(t *testing.T) {
	term := New(10, 2)
	term.WriteString("\x1b[99 q")
	snap := term.Snapshot(0)
	if snap.CursorStyle != CursorBlock || snap.CursorStyleSet || !snap.CursorBlink {
		t.Fatalf("invalid sequence selected a cursor style: %+v", snap)
	}
	term.WriteString("\x1b[5 q\x1b[99 q")
	term.SnapshotInto(&snap, 0)
	if snap.CursorStyle != CursorBar || !snap.CursorStyleSet || !snap.CursorBlink {
		t.Fatalf("invalid sequence changed the selected cursor: %+v", snap)
	}
	term.WriteString("\x1b[6 q\x1b[99 q")
	term.SnapshotInto(&snap, 0)
	if snap.CursorBlink {
		t.Fatal("invalid sequence changed a steady cursor to blinking")
	}
	term.WriteString("\x1bc")
	term.SnapshotInto(&snap, 0)
	if snap.CursorStyle != CursorBlock || snap.CursorStyleSet || !snap.CursorBlink {
		t.Fatalf("RIS did not clear the explicit cursor style: %+v", snap)
	}
}
