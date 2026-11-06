package app

import (
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestTerminalCursorStyleSettings(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(strconv.FormatBool(dark), func(t *testing.T) {
			a, tt := newTestApp(t)
			a.openSettings(prefTerminal)
			tt.SetDark(dark)
			for _, lang := range settings.Languages {
				tt.SetLocale(lang)
				tt.Frame()
				m := locales.In(lang)
				names := []string{m.CursorBlock(), m.CursorBar(), m.CursorUnderline()}
				codes := []string{settings.CursorBlock, settings.CursorBar, settings.CursorUnderline}
				if !tt.HasText(m.CursorShape()) || !tt.HasText(m.CursorShapeNote()) {
					t.Fatalf("%s missing cursor shape label", lang)
				}
				for _, next := range []int{1, 2, 0} {
					current := names[slices.Index(codes, a.Settings().TerminalCursorStyle)]
					scrollToCursorChoice(t, tt, current)
					mustClick(t, tt, current)
					tt.Frame()
					for _, name := range names {
						if !tt.HasText(name) {
							t.Fatalf("%s missing cursor option %q", lang, name)
						}
					}
					mustClick(t, tt, names[next])
					tt.Frame()
					if got := a.Settings().TerminalCursorStyle; got != codes[next] {
						t.Fatalf("%s live cursor = %q, want %q", lang, got, codes[next])
					}
					saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
					if err != nil || saved.TerminalCursorStyle != codes[next] {
						t.Fatalf("%s saved cursor = %q, want %q, error %v", lang, saved.TerminalCursorStyle, codes[next], err)
					}
				}
			}
		})
	}
}

func scrollToCursorChoice(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	r := mustFind(t, tt, label)
	// Keep the native select and its popup well inside the 1200 x 760 window.
	if r.Y < 160 || r.Y+r.H > 560 {
		tt.Scroll(900, 600, 0, r.Y-360)
		tt.Advance(300 * time.Millisecond)
		tt.Frame()
		r = mustFind(t, tt, label)
	}
	if r.X < 0 || r.X+r.W > 1200 || r.Y < 160 || r.Y+r.H > 560 {
		t.Fatalf("cursor control outside clickable settings region: %+v", r)
	}
}
