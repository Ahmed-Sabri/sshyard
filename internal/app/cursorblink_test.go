package app

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/vt"
	"github.com/egoist/mygo/ui"
)

type cursorBlinkFixture struct {
	term    *vt.Terminal
	v       *termView
	tt      *ui.Tester
	bounds  ui.Rect
	pal     theme.Terminal
	focused bool
	paints  int
}

func newCursorBlinkFixture(dark bool) *cursorBlinkFixture {
	f := &cursorBlinkFixture{term: vt.New(20, 4), pal: theme.For(theme.Auto, dark).Terminal, focused: true}
	f.v = newTermView(paintTarget{f.term}, 13)
	f.tt = ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
			f.paints++
			f.bounds = r
			f.v.paint(p, r, &f.pal, f.focused)
		})
	}, 300, 120)
	return f
}

func (f *cursorBlinkFixture) assert(t *testing.T, style vt.CursorStyle) {
	t.Helper()
	assertCursorPixels(t, f.tt.Image(), f.bounds, f.v, f.pal, style)
}

func TestTerminalCursorBlinkPaint(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, style := range []vt.CursorStyle{vt.CursorBlock, vt.CursorBar, vt.CursorUnderline} {
			f := newCursorBlinkFixture(dark)
			f.v.cursorStyle = style
			f.tt.Frame()
			f.assert(t, style)
			f.tt.Advance(499 * time.Millisecond)
			f.assert(t, style)
			f.tt.Advance(time.Millisecond)
			f.assert(t, -1)
			f.tt.Advance(500 * time.Millisecond)
			f.assert(t, style)
		}
	}
}

func TestTerminalCursorBlinkInput(t *testing.T) {
	f := newCursorBlinkFixture(false)
	f.tt.Frame()
	for _, input := range []func(){
		func() { f.v.input(ui.InputEvent{Kind: ui.InputText, Text: "x"}, &keptClipboard{}) },
		func() { f.v.input(ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyLeft}, &keptClipboard{}) },
		func() { f.v.Paste("pasted") },
	} {
		f.tt.Advance(500 * time.Millisecond)
		f.assert(t, -1)
		input()
		f.tt.Frame()
		f.assert(t, vt.CursorBlock)
		if f.term.ScreenText() != "" {
			t.Fatal("input fixture unexpectedly echoed")
		}
	}
}

func TestTerminalCursorBlinkVisibility(t *testing.T) {
	f := newCursorBlinkFixture(false)
	f.term.WriteString("\x1b[?25l")
	f.tt.Frame()
	f.tt.Advance(1500 * time.Millisecond)
	f.assert(t, -1)
	f.term.WriteString("\x1b[?25h" + strings.Repeat("\r\n", f.v.rows+2))
	f.v.offset = f.v.rows
	f.tt.Frame()
	f.tt.Advance(1500 * time.Millisecond)
	if f.v.snap.CursorVisible || !f.v.blink.at.IsZero() {
		t.Fatal("scrolled-out cursor became visible or kept blinking")
	}
	f.v.offset = 0
	f.term.WriteString("\x1b[H")
	f.tt.Frame()
	f.assert(t, vt.CursorBlock)
	f.tt.Advance(500 * time.Millisecond)
	f.assert(t, -1)
	f.v.input(ui.InputEvent{Kind: ui.InputCompose, Text: "a"}, &keptClipboard{})
	f.tt.Frame()
	f.tt.Advance(1500 * time.Millisecond)
	if !f.v.blink.at.IsZero() {
		t.Error("IME composition kept the terminal cursor blinking")
	}
	f.v.input(ui.InputEvent{Kind: ui.InputCompose}, &keptClipboard{})
	f.tt.Frame()
	f.assert(t, vt.CursorBlock)
}

func TestTerminalCursorBlinkProgramControl(t *testing.T) {
	for _, seq := range []string{"\x1b[2 q", "\x1b[4 q", "\x1b[6 q"} {
		f := newCursorBlinkFixture(false)
		f.term.WriteString(seq)
		f.tt.Frame()
		style := f.term.Snapshot(0).CursorStyle
		f.tt.Advance(750 * time.Millisecond)
		f.assert(t, style)
		if !f.v.blink.at.IsZero() {
			t.Error("steady program cursor scheduled blinking")
		}
		f.term.WriteString("\x1bc")
		f.tt.Frame()
		f.tt.Advance(500 * time.Millisecond)
		f.assert(t, -1)
	}
}

func TestTerminalCursorBlinkFocusAndMovement(t *testing.T) {
	f := newCursorBlinkFixture(false)
	f.tt.Advance(500 * time.Millisecond)
	f.assert(t, -1)
	f.focused = false
	f.tt.Frame()
	outline := append([]byte(nil), f.tt.Image().Pix...)
	edge := f.tt.Image().RGBAAt(termPad, int(termPad+f.v.cellH/2))
	if edge.R == f.pal.Background.R && edge.G == f.pal.Background.G && edge.B == f.pal.Background.B {
		t.Fatal("unfocused cursor outline is absent")
	}
	f.tt.Advance(1500 * time.Millisecond)
	if !bytes.Equal(outline, f.tt.Image().Pix) || !f.v.blink.at.IsZero() {
		t.Fatal("unfocused outline changed while time advanced")
	}
	f.focused = true
	f.tt.Frame()
	f.assert(t, vt.CursorBlock)
	f.tt.Advance(500 * time.Millisecond)
	f.assert(t, -1)
	f.term.WriteString("\x1b[C")
	f.tt.Frame()
	r := f.bounds
	r.X += f.v.cellW
	assertCursorPixels(t, f.tt.Image(), r, f.v, f.pal, vt.CursorBlock)
}

func TestTerminalCursorBlinkWorkspace(t *testing.T) {
	srv, _ := startScript(t, "cursor-ready")
	a, tt := newTestApp(t)
	cn := connected(t, a, tt, srv)
	shownText(t, tt, cn, "cursor-ready")
	applyCursorSetting(t, a, tt, settings.CursorBar)
	assertWorkspaceCursor(t, a, tt, cn, vt.CursorBar)
	r := mustFind(t, tt, "Terminal "+cn.host.Label())
	pal := a.termPalette(theme.LightTokens())
	tt.Advance(500 * time.Millisecond)
	assertCursorPixels(t, tt.Image(), r, cn.tv, pal, -1)
	tt.Advance(500 * time.Millisecond)
	assertCursorPixels(t, tt.Image(), r, cn.tv, pal, vt.CursorBar)
	tt.SetFocused(false)
	tt.Frame()
	tt.Advance(1500 * time.Millisecond)
	if !cn.tv.blink.at.IsZero() {
		t.Fatal("window blur did not stop blinking")
	}
	tt.SetFocused(true)
	tt.Frame()
	assertCursorPixels(t, tt.Image(), r, cn.tv, pal, vt.CursorBar)
}

func TestTerminalCursorBlinkReturnsFromSettings(t *testing.T) {
	srv, _ := startScript(t, "cursor-ready")
	a, tt := newTestApp(t)
	cn := connected(t, a, tt, srv)
	shownText(t, tt, cn, "cursor-ready")
	applyCursorSetting(t, a, tt, settings.CursorBar)
	assertWorkspaceCursor(t, a, tt, cn, vt.CursorBar)
	r := mustFind(t, tt, "Terminal "+cn.host.Label())
	pal := a.termPalette(theme.LightTokens())
	tt.Advance(500 * time.Millisecond)
	assertCursorPixels(t, tt.Image(), r, cn.tv, pal, -1)
	a.openSettings(prefTerminal)
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	a.closeSettings()
	tt.Frame()
	assertCursorPixels(t, tt.Image(), r, cn.tv, pal, vt.CursorBar)
	tt.Advance(499 * time.Millisecond)
	assertCursorPixels(t, tt.Image(), r, cn.tv, pal, vt.CursorBar)
	tt.Advance(time.Millisecond)
	assertCursorPixels(t, tt.Image(), r, cn.tv, pal, -1)
}
