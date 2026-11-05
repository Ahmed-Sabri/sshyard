package app

import (
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/Licoy/sshyard/internal/vt"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

// scriptShell is a shell that writes what it is given and keeps what it
// reads, as a program that asked for the mouse.
type scriptShell struct {
	out  chan string
	mu   sync.Mutex
	read strings.Builder
}

func newScriptShell(first string) *scriptShell {
	s := &scriptShell{out: make(chan string, 8)}
	s.out <- first
	return s
}

func (s *scriptShell) run(ch ssh.Channel, _ sshtest.PTY, _ <-chan sshtest.PTY) int {
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 256)
		for {
			n, err := ch.Read(buf)
			s.mu.Lock()
			s.read.Write(buf[:n])
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case o := <-s.out:
			io.WriteString(ch, o)
		case <-done:
			return 0
		}
	}
}

// take returns what was read since the last take.
func (s *scriptShell) take() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.read.String()
	s.read.Reset()
	return r
}

func startScript(t *testing.T, first string) (*sshtest.Server, *scriptShell) {
	t.Helper()
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	sh := newScriptShell(first)
	srv.Shell = sh.run
	return srv, sh
}

// cellPoint is the window point in the middle of a cell of the terminal.
func cellPoint(t *testing.T, tt *ui.Tester, c *conn, col, row int) (float32, float32) {
	t.Helper()
	r, ok := tt.Find("Terminal box")
	if !ok {
		t.Fatalf("no terminal; texts %q", tt.Texts())
	}
	tv := c.tv
	return r.X + termPad + (float32(col)+0.5)*tv.cellW, r.Y + termPad + (float32(row)+0.5)*tv.cellH
}

func expectRead(t *testing.T, tt *ui.Tester, sh *scriptShell, want string) {
	t.Helper()
	var got string
	waitFor(t, tt, "the report "+want, func() bool {
		got += sh.take()
		return strings.Contains(got, want)
	})
}

func TestMouseReporting(t *testing.T) {
	srv, sh := startScript(t, "\x1b[?1049h\x1b[?1002h\x1b[?1006hprogram")
	a, tt := newTestApp(t)
	c := connected(t, a, tt, srv)
	waitFor(t, tt, "mouse mode", func() bool { return c.s.Term.Modes().Mouse == vt.MouseButton })
	tt.Frame()
	if _, ok := tt.Find("The program is receiving the mouse. Hold Shift to select text."); !ok {
		t.Error("no mouse icon in the pane header")
	}
	if tt.Cursor() == ui.CursorText {
		// not asserted strictly: the cursor follows the pointer
		t.Log("text cursor over a terminal that takes the mouse")
	}
	sh.take()

	// press, drag, release: SGR, 1-based
	x, y := cellPoint(t, tt, c, 2, 1)
	tt.Press(x, y)
	expectRead(t, tt, sh, "\x1b[<0;3;2M")
	x2, _ := cellPoint(t, tt, c, 5, 1)
	tt.Move(x2, y)
	expectRead(t, tt, sh, "\x1b[<32;6;2M")
	tt.Release(x2, y)
	expectRead(t, tt, sh, "\x1b[<0;6;2m")
	if c.tv.selected {
		t.Error("a reported drag selected text")
	}

	// the wheel: 64 up, 65 down, one report a notch
	tt.Wheel(x, y, 0, -1)
	expectRead(t, tt, sh, "\x1b[<64;3;2M")
	tt.Wheel(x, y, 0, 1)
	expectRead(t, tt, sh, "\x1b[<65;3;2M")
	if c.tv.offset != 0 {
		t.Errorf("the wheel scrolled the view to %d", c.tv.offset)
	}

	// the right button goes to the program, not to the context menu
	tt.RightClickAt(x, y)
	expectRead(t, tt, sh, "\x1b[<2;3;2m")
	if m := menuOf(a); m != nil {
		t.Errorf("menu %q opened on a reported right click", m)
	}

	// Shift keeps the mouse for selecting, and the context menu. The
	// Tester's drags carry no modifiers: a Shift click through it, and a
	// Shift drag and right click as the element gets them.
	tt.ClickAtWith(ui.Shift, x, y)
	tt.Frame()
	if got := sh.take(); strings.Contains(got, "\x1b[<") {
		t.Errorf("Shift+click reported %q", got)
	}
	r, _ := tt.Find("Terminal box")
	lx, ly := x-r.X, termPad+c.tv.cellH/2 // over "program"
	svc := fakeClipboard{}
	if !c.tv.input(ui.InputEvent{Kind: ui.InputPointerDown, X: lx, Y: ly, Mods: ui.Shift, Clicks: 1}, svc) ||
		!c.tv.input(ui.InputEvent{Kind: ui.InputPointerMove, X: lx + 3*c.tv.cellW, Y: ly, Mods: ui.Shift}, svc) ||
		!c.tv.input(ui.InputEvent{Kind: ui.InputPointerUp, X: lx + 3*c.tv.cellW, Y: ly, Mods: ui.Shift}, svc) {
		t.Error("Shift drag not taken")
	}
	if !c.tv.selected || c.tv.SelectedText() != "ogra" {
		t.Errorf("Shift+drag did not select: %q", c.tv.SelectedText())
	}
	if c.tv.input(ui.InputEvent{Kind: ui.InputPointerDown, Button: 1, X: lx, Y: ly, Mods: ui.Shift, Clicks: 1}, svc) {
		t.Error("Shift+right click taken: no context menu")
	}
	tt.Frame()
	if got := sh.take(); strings.Contains(got, "\x1b[<") {
		t.Errorf("Shift reported %q", got)
	}

	// the program turns the mouse off: the icon goes, clicks select
	sh.out <- "\x1b[?1002l\x1b[?1006l"
	waitFor(t, tt, "mouse off", func() bool { return !c.s.Term.Modes().MouseTracking })
	tt.Frame()
	if _, ok := tt.Find("The program is receiving the mouse. Hold Shift to select text."); ok {
		t.Error("mouse icon with the mouse off")
	}
	// on the alternate screen without reports the wheel is the arrows
	tt.Wheel(x, y, 0, 1)
	expectRead(t, tt, sh, "\x1b[B")
}

func TestMouseDefaultEncodingAndAny(t *testing.T) {
	srv, sh := startScript(t, "\x1b[?1003h")
	a, tt := newTestApp(t)
	c := connected(t, a, tt, srv)
	waitFor(t, tt, "mouse mode", func() bool { return c.s.Term.Modes().Mouse == vt.MouseAny })
	tt.Frame()
	sh.take()
	x, y := cellPoint(t, tt, c, 0, 0)
	tt.Move(x, y)
	tt.Move(x+c.tv.cellW, y) // a hover, no button
	expectRead(t, tt, sh, "\x1b[MC\"!")
	tt.Press(x, y)
	expectRead(t, tt, sh, "\x1b[M !!")
	tt.Release(x, y)
	expectRead(t, tt, sh, "\x1b[M#!!")
}

func TestFocusReports(t *testing.T) {
	srv, sh := startScript(t, "\x1b[?1004h")
	a, tt := newTestApp(t)
	c := connected(t, a, tt, srv)
	waitFor(t, tt, "focus mode", func() bool { return c.s.Term.Modes().FocusReport })
	tt.Frame()
	sh.take()
	// The terminal loses the keyboard to the find field, and gets it back.
	tt.Key(appMods, ui.KeyF)
	tt.Frame()
	expectRead(t, tt, sh, "\x1b[O")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	x, y := cellPoint(t, tt, c, 0, 0)
	tt.ClickAt(x, y)
	tt.Frame()
	expectRead(t, tt, sh, "\x1b[I")
}

func TestCombiningCopy(t *testing.T) {
	const line = "e\u0301 n\u0303 👍🏽"
	srv, _ := startScript(t, line+"\r\n")
	a, tt := newTestApp(t)
	c := connected(t, a, tt, srv)
	waitFor(t, tt, "output", func() bool { return strings.Contains(c.s.Term.ScreenText(), line) })
	tt.Frame()
	tt.Image() // draws, the marks on their letters
	tt.Command("selectAll")
	tt.Command("copy")
	if got := strings.TrimSpace(tt.Clipboard()); got != line {
		t.Errorf("copied %q (% x), want %q", got, got, line)
	}
	tt.Key(appMods, ui.KeyF)
	tt.Frame()
	tt.Type("ñ") // precomposed, as typed
	tt.Frame()
	tt.Frame()
	if len(c.tv.matches) != 1 {
		t.Errorf("%d matches for ñ", len(c.tv.matches))
	}
}

type fakeClipboard struct{}

func (fakeClipboard) ReadClipboard() string { return "" }
func (fakeClipboard) WriteClipboard(string) {}

func TestPlaceGlyphsKeepsMarksOnTheirBase(t *testing.T) {
	// "e" + U+0301, "x": the shaper put the mark 1 DIP right of the e's
	// start, with no advance, in the e's cluster.
	g := []ui.Glyph{
		{Cluster: 0, Runes: 2, X: 0, Advance: 6},
		{Cluster: 0, Runes: 2, X: 1, Advance: 0},
		{Cluster: 2, Runes: 1, X: 6, Advance: 6},
	}
	placed := append([]ui.Glyph(nil), g...)
	cells := []vt.Cell{{Rune: 'e', Comb: vt.CombOf("\u0301"), Width: 1}, {Rune: 'x', Width: 1}}
	placeGlyphs(placed, g, []int{0, 0, 1}, cells, 8)
	if placed[0].X != 1 || placed[1].X != 2 || placed[2].X != 9 {
		t.Errorf("x %v %v %v, want 1 2 9", placed[0].X, placed[1].X, placed[2].X)
	}
}
