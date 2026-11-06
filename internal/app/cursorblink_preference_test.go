package app

import (
	"runtime"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/vt"
	"github.com/egoist/mygo/ui"
)

func TestTerminalCursorBlinkPreferencePaint(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, tc := range []struct {
			style vt.CursorStyle
			seq   string
		}{
			{vt.CursorBlock, "\x1b[0 q"},
			{vt.CursorBlock, "\x1b[1 q"},
			{vt.CursorBar, "\x1b[5 q"},
			{vt.CursorUnderline, "\x1b[3 q"},
		} {
			f := newCursorBlinkFixture(dark)
			f.v.cursorStyle, f.v.cursorBlink = tc.style, false
			f.tt.Frame()
			f.assert(t, tc.style)
			f.term.WriteString(tc.seq)
			f.tt.Frame()
			f.tt.Advance(750 * time.Millisecond)
			f.assert(t, tc.style)
			paints := f.paints
			f.tt.Advance(750 * time.Millisecond)
			f.assert(t, tc.style)
			if f.paints != paints || !f.v.blink.at.IsZero() {
				t.Fatal("disabled cursor blinking kept scheduling repaints")
			}
			f.v.cursorBlink = true
			f.tt.Frame()
			f.assert(t, tc.style)
			f.tt.Advance(500 * time.Millisecond)
			f.assert(t, -1)
		}
	}
}

func TestTerminalCursorBlinkPreferenceWorkspace(t *testing.T) {
	srv, _ := startScript(t, "cursor-ready")
	a, tt := newTestApp(t)
	cn := connected(t, a, tt, srv)
	shownText(t, tt, cn, "cursor-ready")
	original := cn.s
	applyCursorSetting(t, a, tt, settings.CursorBar)
	assertWorkspaceCursor(t, a, tt, cn, vt.CursorBar)
	r := mustFind(t, tt, "Terminal "+cn.host.Label())
	pal := a.termPalette(theme.LightTokens())
	tt.Advance(500 * time.Millisecond)
	assertCursorPixels(t, tt.Image(), r, cn.tv, pal, -1)
	applyBlinkPreference(t, a, tt, false)
	assertCursorPixels(t, tt.Image(), r, cn.tv, pal, vt.CursorBar)
	cn.s.Term.WriteString("\x1b[5 q")
	tt.Frame()
	tt.Advance(750 * time.Millisecond)
	assertCursorPixels(t, tt.Image(), r, cn.tv, pal, vt.CursorBar)
	tt.Advance(750 * time.Millisecond)
	assertCursorPixels(t, tt.Image(), r, cn.tv, pal, vt.CursorBar)
	applyBlinkPreference(t, a, tt, true)
	assertCursorPixels(t, tt.Image(), r, cn.tv, pal, vt.CursorBar)
	tt.Advance(500 * time.Millisecond)
	assertCursorPixels(t, tt.Image(), r, cn.tv, pal, -1)
	if cn.s != original || cn.s.Status().State != session.Connected {
		t.Fatal("changing cursor blinking replaced or disconnected the SSH session")
	}
}

func TestTerminalCursorBlinkPreferenceLocalAndSplit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the controlled local shell fixture uses POSIX sh and cat")
	}
	withShells(t, session.Shell{ID: "test", Name: "Test shell", Path: "/bin/sh", Args: []string{"-c", "printf cursor-ready; exec cat"}})
	a, tt := newTestApp(t)
	applyCursorSetting(t, a, tt, settings.CursorBar)
	applyBlinkPreference(t, a, tt, false)
	if _, err := a.OpenLocalTerminal(); err != nil {
		t.Fatal(err)
	}
	local := a.focused()
	waitFor(t, tt, "local shell output", func() bool { return local.s.Term.ScreenText() == "cursor-ready" })
	a.SplitFocused(layout.Right)
	split := a.focused()
	waitFor(t, tt, "split shell output", func() bool { return split.s.Term.ScreenText() == "cursor-ready" })
	for _, cn := range []*conn{local, split} {
		assertWorkspaceCursor(t, a, tt, cn, vt.CursorBar)
		r := a.paneBounds[cn.id]
		r.X++
		r.Y += paneHeadHeight + 2
		tt.Advance(750 * time.Millisecond)
		assertCursorPixels(t, tt.Image(), r, cn.tv, a.termPalette(theme.LightTokens()), vt.CursorBar)
	}
	applyBlinkPreference(t, a, tt, true)
	for _, cn := range []*conn{local, split} {
		assertWorkspaceCursor(t, a, tt, cn, vt.CursorBar)
		r := a.paneBounds[cn.id]
		r.X++
		r.Y += paneHeadHeight + 2
		tt.Advance(500 * time.Millisecond)
		assertCursorPixels(t, tt.Image(), r, cn.tv, a.termPalette(theme.LightTokens()), -1)
	}
}

func TestTerminalCursorBlinkPreferenceProgramSteady(t *testing.T) {
	for _, seq := range []string{"\x1b[2 q", "\x1b[4 q", "\x1b[6 q"} {
		f := newCursorBlinkFixture(false)
		f.term.WriteString(seq)
		f.v.cursorBlink = false
		f.tt.Frame()
		f.tt.Advance(750 * time.Millisecond)
		f.v.cursorBlink = true
		f.tt.Frame()
		f.tt.Advance(750 * time.Millisecond)
		f.assert(t, f.term.Snapshot(0).CursorStyle)
		if f.term.Snapshot(0).CursorBlink || !f.v.blink.at.IsZero() {
			t.Fatal("enabling blinking overrode the program's steady cursor")
		}
	}
}

func applyBlinkPreference(t *testing.T, a *App, tt *ui.Tester, blink bool) {
	t.Helper()
	s := a.Settings()
	s.TerminalCursorBlink = blink
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}
