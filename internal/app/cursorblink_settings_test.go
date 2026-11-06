package app

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestTerminalCursorBlinkSettings(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(strconv.FormatBool(dark), func(t *testing.T) {
			a, tt := newTestApp(t)
			a.openSettings(prefTerminal)
			tt.SetDark(dark)
			if !a.Settings().TerminalCursorBlink {
				t.Fatal("cursor blinking must default on")
			}
			for _, lang := range settings.Languages {
				tt.SetLocale(lang)
				tt.Frame()
				m := locales.In(lang)
				scrollToCursorChoice(t, tt, m.CursorBlock())
				if !tt.HasText(m.TerminalCursorBlinkNote()) {
					t.Fatalf("%s missing cursor blink explanation", lang)
				}
				for _, on := range []bool{false, true} {
					r := cursorBlinkSwitchBounds(t, tt, m)
					tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
					tt.Frame()
					if got := a.Settings().TerminalCursorBlink; got != on {
						t.Fatalf("%s live cursor blink = %v, want %v", lang, got, on)
					}
					saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
					if err != nil || saved.TerminalCursorBlink != on {
						t.Fatalf("%s saved cursor blink = %v, want %v, error %v", lang, saved.TerminalCursorBlink, on, err)
					}
				}
			}
		})
	}
}

func cursorBlinkSwitchBounds(t *testing.T, tt *ui.Tester, m locales.Messages) ui.Rect {
	t.Helper()
	label := mustFind(t, tt, m.TerminalCursorBlink())
	note := mustFind(t, tt, m.TerminalCursorBlinkNote())
	shape := mustFind(t, tt, m.CursorBlock())
	// Both controls align to the row's right edge, the shape select is 180 wide
	// with 14 DIP horizontal padding; Switch is 36 x 20 and centered in its row.
	r := ui.Rect{X: shape.X - theme.Space(3.5) + 180 - theme.Space(9), Y: (label.Y+note.Y+note.H)/2 - theme.Space(2.5), W: theme.Space(9), H: theme.Space(5)}
	if r.X < 0 || r.X+r.W > 1200 || r.Y < 160 || r.Y+r.H > 560 {
		t.Fatalf("cursor switch outside clickable settings region: %+v", r)
	}
	return r
}
