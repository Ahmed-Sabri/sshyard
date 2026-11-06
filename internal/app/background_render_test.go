package app

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/vt"
	"github.com/egoist/mygo/ui"
)

func renderBackgroundFixture(a *App) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 200; x++ {
			if x < 100 {
				img.SetRGBA(x, y, color.RGBA{R: 240, G: 30, B: 20, A: 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{R: 20, G: 30, B: 240, A: 255})
			}
		}
	}
	a.settings.Background = settings.DefaultBackground()
	a.settings.Background.Image = "background-render.png"
	a.background.active, a.background.requested = a.settings.Background.Image, a.settings.Background.Image
	a.background.bmp = ui.NewBitmap(img)
}

func TestBackgroundImageFitAndResize(t *testing.T) {
	a := &App{}
	renderBackgroundFixture(a)
	tok := theme.LightTokens()
	tt := ui.NewTester(func(c *ui.Context) { a.drawBackground(c, tok) }, 200, 200)
	tt.Frame()
	img := tt.Image()
	if p := img.RGBAAt(10, 10); p.R < 220 || p.B > 40 {
		t.Fatalf("cover top-left: %v", p)
	}
	if p := img.RGBAAt(190, 190); p.B < 220 || p.R > 40 {
		t.Fatalf("cover bottom-right: %v", p)
	}
	a.settings.Background.Fit = "contain"
	tt.Frame()
	img = tt.Image()
	if p := img.RGBAAt(10, 10); p.R != tok.Canvas.R || p.B != tok.Canvas.B {
		t.Fatalf("contain letterbox: %v want %v", p, tok.Canvas)
	}
	if p := img.RGBAAt(10, 100); p.R < 220 {
		t.Fatalf("contain image: %v", p)
	}
	tt.SetSize(400, 100)
	tt.Frame()
	img = tt.Image()
	if p := img.RGBAAt(10, 50); p.R != tok.Canvas.R || p.B != tok.Canvas.B {
		t.Fatalf("resized letterbox: %v", p)
	}
	if p := img.RGBAAt(110, 50); p.R < 220 {
		t.Fatalf("resized image: %v", p)
	}
}

func TestBackgroundMainWithoutVibrancy(t *testing.T) {
	a, tt := newTestApp(t)
	renderBackgroundFixture(a)
	a.settings.Background.ChromeOpacity = 0
	a.settings.Background.ContentOpacity = 0
	tt.SetVibrancy(false)
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		img := tt.Image()
		for _, xy := range []image.Point{{40, 300}, {700, 650}, {100, 10}} {
			p := img.RGBAAt(xy.X, xy.Y)
			if p.R < 220 && p.B < 220 {
				t.Errorf("dark %v region %v blocks background: %v", dark, xy, p)
			}
		}
		name := "light"
		if dark {
			name = "dark"
		}
		saveBackgroundShot(t, tt, name)
		if !a.clearContent {
			t.Error("image did not clear default terminal fill without vibrancy")
		}
	}
	a.settings.Background.ChromeOpacity = 100
	tt.Frame()
	tok := theme.For(a.settings.Theme, true)
	if p := tt.Image().RGBAAt(40, 300); p.R != tok.Sidebar.R || p.B != tok.Sidebar.B {
		t.Fatalf("opaque chrome %v want %v", p, tok.Sidebar)
	}
	a.openSettings(prefAppearance)
	tt.Frame()
	saveBackgroundShot(t, tt, "settings")
	// Image rendering retains the root painter's device scale observation.
	a.zoom.desktop = 1.5
	a.settings.UIZoom = 100
	tt.SetScale(2)
	tt.Frame()
	if a.zoom.device != 2 {
		t.Fatalf("device scale observation lost: %v", a.zoom.device)
	}
}

func TestBackgroundTerminalExplicitColors(t *testing.T) {
	a := &App{}
	renderBackgroundFixture(a)
	term := vt.New(30, 4)
	term.WriteString("\x1b[41m  \x1b[0m  ")
	v := newTermView(paintTarget{term}, 13)
	v.clear = true
	pal := theme.DarkTokens().Terminal
	var bounds ui.Rect
	tt := ui.NewTester(func(c *ui.Context) {
		a.drawBackground(c, theme.DarkTokens())
		ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) { bounds = r; v.paint(p, r, &pal, false) })
	}, 300, 120)
	tt.Frame()
	cell := func(col int) color.RGBA {
		return tt.Image().RGBAAt(int(bounds.X+termPad+float32(col)*v.cellW+v.cellW/2), int(bounds.Y+termPad+v.cellH/2))
	}
	saveBackgroundShot(t, tt, "terminal")
	ansi := cell(0)
	if ansi.R != pal.ANSI[1].R || ansi.G != pal.ANSI[1].G || ansi.B != pal.ANSI[1].B {
		t.Fatalf("ANSI background altered: %v want %v", ansi, pal.ANSI[1])
	}
	if p := cell(3); p == ansi || p.R != 240 {
		t.Fatalf("default cell did not reveal image: %v", p)
	}
	v.selA, v.selB, v.selected = vt.Pos{Line: 0, Col: 0}, vt.Pos{Line: 0, Col: 0}, true
	tt.Frame()
	if p := cell(0); p.R == ansi.R && p.G == ansi.G && p.B == ansi.B {
		t.Fatalf("selection did not paint over ANSI: %v", p)
	}
}

func TestBackgroundEditorWindow(t *testing.T) {
	a, tt, _, _ := localPane(t, "background.txt")
	renderBackgroundFixture(a)
	a.settings.Background.ChromeOpacity = 0
	a.settings.Background.ContentOpacity = 0
	wins := fakeWindows(a)
	waitFor(t, tt, "local file", func() bool { return tt.HasText("background.txt") })
	mustClick(t, tt, "background.txt")
	mustClick(t, tt, "background.txt")
	_, et, ec := openedWindow(t, a, tt, wins, 1)
	et.SetVibrancy(false)
	et.Frame()
	ed := shown(ec)
	if ed.st.Theme == nil || ed.st.Theme.Background.A != 0 {
		t.Fatal("editor default background stayed opaque")
	}
	img := et.Image()
	x, y := img.Bounds().Dx()-80, img.Bounds().Dy()/2
	if p := img.RGBAAt(x, y); p.B < 220 || p.R > 40 {
		t.Fatalf("editor content blocks background at %d,%d: %v", x, y, p)
	}
	saveBackgroundShot(t, et, "editor")
	a.settings.Background.Image = ""
	et.Frame()
	if ed.st.Theme != nil {
		t.Fatal("removing image did not restore default editor theme")
	}
}

func saveBackgroundShot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("SSHYARD_BACKGROUND_SHOTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, tt.Image()); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBackgroundSplitContinuity(t *testing.T) {
	a, tt := newTestApp(t)
	renderBackgroundFixture(a)
	if _, err := a.OpenLocalTerminal(); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	first := a.focused()
	waitForIn(t, tt, slowWait, "local shell", func() bool { return first.s.Status().State == session.Connected })
	a.SplitFocused(layout.Right)
	tt.Frame()
	tt.Frame()
	second := a.focused()
	waitForIn(t, tt, slowWait, "split local shell", func() bool { return second.s.Status().State == session.Connected })
	for _, cn := range []*conn{first, second} {
		cn.tv.target.Terminal().WriteString("\x1b[2J\x1b[HSSHYard background readability\r\n\x1b[32mConnected\x1b[0m  \x1b[41m ANSI background \x1b[0m\r\n$ echo hello\r\nhello\r\n")
	}
	a.settings.Background.ChromeOpacity = 0
	a.settings.Background.ContentOpacity = 0
	tt.Frame()
	p1, p2 := a.paneBounds[first.id], a.paneBounds[second.id]
	img := tt.Image()
	// The first pane crosses the window's image boundary; the second
	// continues in blue instead of restarting its image in red.
	for _, sample := range []struct {
		x, y float32
		red  bool
	}{
		{p1.X + 10, p1.Y + p1.H - 40, true},
		{p1.X + p1.W - 10, p1.Y + p1.H - 40, false},
		{p2.X + 10, p2.Y + p2.H - 40, false},
	} {
		p := img.RGBAAt(int(sample.x), int(sample.y))
		if sample.red && (p.R < 220 || p.B > 40) || !sample.red && (p.B < 220 || p.R > 40) {
			t.Errorf("pane image discontinuity at %v: %v", sample, p)
		}
	}
	saveBackgroundShot(t, tt, "split-clear")
	a.settings.Background.ChromeOpacity = 70
	a.settings.Background.ContentOpacity = 80
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		name := "workspace-default-light"
		if dark {
			name = "workspace-default-dark"
		}
		saveBackgroundShot(t, tt, name)
	}
	a.openSettings(prefAppearance)
	tt.Frame()
	saveBackgroundShot(t, tt, "settings-default-dark")
}
