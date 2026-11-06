package app

import (
	"fmt"
	"image"
	"image/color"
	"testing"

	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/egoist/mygo/ui"
)

func backgroundGapWorkspace(t *testing.T) (*App, *ui.Tester, []*conn) {
	t.Helper()
	a, tt := newTestApp(t)
	renderBackgroundFixture(a)
	if _, err := a.OpenLocalTerminal(); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	panes := []*conn{a.focused()}
	for _, edge := range []layout.Edge{layout.Right, layout.Bottom} {
		a.SplitFocused(edge)
		tt.Frame()
		panes = append(panes, a.focused())
	}
	for _, cn := range panes {
		waitForIn(t, tt, slowWait, "gap test shell", func() bool { return cn.s.Status().State == session.Connected })
		cn.s.Term.WriteString("\x1b[2J\x1b[H")
	}
	tt.Frame()
	tt.Frame()
	return a, tt, panes
}

// Reference pixels are a single full-window image plus one tint, independently
// of the App's content and pane builders, including alpha images and contain bars.
func backgroundGapReference(a *App, tok theme.Tokens, fill ui.Color, w, h int, overlays ...func(*ui.Painter)) *image.RGBA {
	tt := ui.NewTester(func(c *ui.Context) {
		c.Root().Background(tok.Canvas).Draw(func(p *ui.Painter, r ui.Rect) {
			fit := ui.Cover
			if a.effectiveBackground().Fit == "contain" {
				fit = ui.Contain
			}
			p.Image(a.background.bmp, r, fit)
			p.Fill(r, fill, 0)
			for _, draw := range overlays {
				draw(p)
			}
		})
	}, w, h)
	tt.Frame()
	return tt.Image()
}

func assertBackgroundGapPixel(t *testing.T, got, want color.RGBA, label string, tolerance int) {
	t.Helper()
	for _, pair := range [][2]uint8{{got.R, want.R}, {got.G, want.G}, {got.B, want.B}} {
		if d := int(pair[0]) - int(pair[1]); d < -tolerance || d > tolerance {
			t.Errorf("%s got %v want %v (tolerance %d)", label, got, want, tolerance)
			return
		}
	}
}

func TestBackgroundWorkspaceGaps(t *testing.T) {
	a, tt, panes := backgroundGapWorkspace(t)
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tok := theme.For(a.settings.Theme, dark)
		for _, opacity := range []int{0, 50, 80, 100} {
			a.settings.Background.ContentOpacity = opacity
			tt.Frame()
			tt.Frame()
			want := backgroundGapReference(a, tok, a.backgroundContentFill(tok.Canvas), 1200, 760, func(p *ui.Painter) {
				sh := tok.Card
				for _, cn := range panes {
					r := a.paneBounds[cn.id]
					cell := ui.Rect{X: r.X - paneGap/2, Y: r.Y - paneGap/2, W: r.W + paneGap, H: r.H + paneGap}
					p.Clip(cell, 0, func() { p.Shadow(r, theme.RadiusCard, sh.X, sh.Y, sh.Blur, sh.Spread, sh.Color) })
				}
			})
			p1, p2 := a.paneBounds[panes[0].id], a.paneBounds[panes[1].id]
			points := map[string]image.Point{
				"outside":               {int(p1.X - 4), int(p1.Y + p1.H/2)},
				"horizontal split":      {int(p1.X + p1.W + 1), int(p1.Y + p1.H/2)},
				"vertical split":        {int(p2.X + p2.W/2), int(p2.Y + p2.H + 1)},
				"behind rounded corner": {int(p1.X + 1), int(p1.Y + 1)},
			}
			for label, point := range points {

				assertBackgroundGapPixel(t, tt.Image().RGBAAt(point.X, point.Y), want.RGBAAt(point.X, point.Y), fmt.Sprintf("dark=%v opacity=%d %s at %v", dark, opacity, label, point), 2)
			}
			saveBackgroundShot(t, tt, fmt.Sprintf("gaps-dark%v-opacity%d", dark, opacity))
		}
	}
}

func TestBackgroundPaneSingleTint(t *testing.T) {
	a, tt, panes := backgroundGapWorkspace(t)
	// A contrasting imported terminal palette must remain distinct from the canvas.
	sc := theme.Schemes[0]
	sc.ID, sc.Background = "custom:gap-regression", "#315B47"
	a.settings.TerminalSchemes = []theme.Scheme{sc}
	a.settings.TerminalScheme = sc.ID
	for _, translucent := range []bool{false, true} {
		if translucent {
			backgroundGapImage(a, color.NRGBA{R: 240, G: 30, B: 20, A: 128}, color.NRGBA{R: 20, G: 30, B: 240, A: 128})
		}
		for _, dark := range []bool{false, true} {
			tt.SetDark(dark)
			tok := theme.For(a.settings.Theme, dark)
			for _, fit := range []string{"cover", "contain"} {
				a.settings.Background.Fit = fit
				checkBackgroundPaneSizes(t, a, tt, panes, tok, fmt.Sprintf("alpha=%v dark=%v fit=%s", translucent, dark, fit))
			}
		}
	}
	// Removing the image must restore opaque terminal fills even after all image modes.
	a.settings.Background.Image = ""
	tt.Frame()
	tt.Frame()
	pal := a.termPalette(theme.For(a.settings.Theme, true))
	r := a.paneBounds[panes[0].id]
	got := tt.Image().RGBAAt(int(r.X+20), int(r.Y+r.H-45))
	assertBackgroundGapPixel(t, got, color.RGBA{R: pal.Background.R, G: pal.Background.G, B: pal.Background.B, A: 255}, "removed background", 0)
}

func TestBackgroundMixedPanePalette(t *testing.T) {
	a, tt, _ := backgroundGapWorkspace(t)
	sc := theme.Schemes[0]
	sc.ID, sc.Background = "custom:mixed-gap", "#102030"
	a.settings.TerminalSchemes = []theme.Scheme{sc}
	a.settings.TerminalScheme = sc.ID
	tab := a.ws.ActiveTab()
	h := savedHost(t, a, "palette test", "example.com")
	id, err := a.OpenAI(h.ID, openAt{pane: tab.Focus, edge: layout.Right}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A bright source makes untinted cracks particularly visible in screenshots.
	backgroundGapImage(a, color.NRGBA{R: 255, G: 249, B: 225, A: 255}, color.NRGBA{R: 245, G: 245, B: 245, A: 255})
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tok := theme.For(a.settings.Theme, dark)
		for _, opacity := range []int{50, 80, 100} {
			a.settings.Background.ContentOpacity = opacity
			tt.Frame()
			tt.Frame()
			want := backgroundGapReference(a, tok, a.backgroundContentFill(tok.Inset), 1200, 760)
			r := a.paneBounds[id]
			x, y := int(r.X+20), int(r.Y+r.H/2)
			assertBackgroundGapPixel(t, tt.Image().RGBAAt(x, y), want.RGBAAt(x, y), fmt.Sprintf("mixed AI dark=%v opacity=%d", dark, opacity), 1)
			if opacity == 80 {
				saveBackgroundShot(t, tt, fmt.Sprintf("gaps-bright-mixed-dark%v", dark))
			}
		}
	}
}

func backgroundGapImage(a *App, left, right color.NRGBA) {
	img := image.NewNRGBA(image.Rect(0, 0, 200, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 200; x++ {
			col := left
			if x >= 100 {
				col = right
			}
			img.SetNRGBA(x, y, col)
		}
	}
	a.background.bmp = ui.NewBitmap(img)
}

func checkBackgroundPaneSizes(t *testing.T, a *App, tt *ui.Tester, panes []*conn, tok theme.Tokens, label string) {
	t.Helper()
	for _, size := range []image.Point{{1200, 760}, {1000, 650}} {
		tt.SetSize(size.X, size.Y)
		tt.Frame()
		pal := a.termPalette(tok)
		firstFrame := backgroundGapReference(a, tok, a.backgroundContentFill(pal.Background), size.X, size.Y)
		assertBackgroundGapPixel(t, tt.Image().RGBAAt(270, 200), firstFrame.RGBAAt(270, 200), fmt.Sprintf("resize first frame %s size=%v", label, size), 1)
		for _, opacity := range []int{0, 50, 80, 100} {
			a.settings.Background.ContentOpacity = opacity
			tt.Frame()
			tt.Frame()
			want := backgroundGapReference(a, tok, a.backgroundContentFill(pal.Background), size.X, size.Y)
			checkBackgroundPanePixels(t, tt, panes, a.paneBounds, want, fmt.Sprintf("%s size=%v opacity=%d", label, size, opacity))
		}
	}
}

func checkBackgroundPanePixels(t *testing.T, tt *ui.Tester, panes []*conn, bounds map[int]ui.Rect, want *image.RGBA, label string) {
	t.Helper()
	for _, cn := range panes {
		r := bounds[cn.id]
		for _, offset := range []float32{20, r.W - 20} {
			x, y := int(r.X+offset), int(r.Y+r.H-45)
			assertBackgroundGapPixel(t, tt.Image().RGBAAt(x, y), want.RGBAAt(x, y), fmt.Sprintf("%s pane=%d at %d,%d", label, cn.id, x, y), 1)
		}
	}
}

func TestBackgroundBandSeparators(t *testing.T) {
	a, tt, _ := backgroundGapWorkspace(t)
	a.panels = []inspectorKind{inspectSnippets}
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tok := theme.For(a.settings.Theme, dark)
		for _, opacity := range []int{0, 50, 80, 100} {
			a.settings.Background.ContentOpacity = opacity
			a.settings.Background.ChromeOpacity = opacity
			tt.Frame()
			tt.Frame()
			for _, sample := range []struct {
				name       string
				point      image.Point
				fill, edge ui.Color
			}{
				{"status", image.Pt(400, 729), a.chromeFill(tok.Glass), tok.GlassEdge},
				{"rail", image.Pt(1169, 600), a.chromeFill(tok.Glass), tok.GlassEdge},
				{"inspector", image.Pt(1168-int(panelWidth(inspectSnippets)), 600), a.backgroundInspectorFill(tok.Canvas), tok.Line},
			} {
				want := backgroundGapReference(a, tok, sample.fill, 1200, 760, func(p *ui.Painter) { p.Fill(ui.Rect{W: 1200, H: 760}, sample.edge, 0) })
				x, y := sample.point.X, sample.point.Y
				assertBackgroundGapPixel(t, tt.Image().RGBAAt(x, y), want.RGBAAt(x, y), fmt.Sprintf("%s separator dark=%v opacity=%d", sample.name, dark, opacity), 1)
			}
		}
	}
}

func TestBackgroundEditorSeparator(t *testing.T) {
	a, tt, _, _ := localPane(t, "separator.txt")
	renderBackgroundFixture(a)
	wins := fakeWindows(a)
	waitFor(t, tt, "local file", func() bool { return tt.HasText("separator.txt") })
	mustClick(t, tt, "separator.txt")
	mustClick(t, tt, "separator.txt")
	_, et, _ := openedWindow(t, a, tt, wins, 1)
	et.SetSize(1000, 680)
	for _, dark := range []bool{false, true} {
		et.SetDark(dark)
		tok := theme.For(a.settings.Theme, dark)
		for _, opacity := range []int{0, 50, 80, 100} {
			a.settings.Background.ChromeOpacity = opacity
			et.Frame()
			want := backgroundGapReference(a, tok, a.editorChromeFill(tok.Sidebar), 1000, 680, func(p *ui.Painter) { p.Fill(ui.Rect{W: 1000, H: 680}, tok.Line, 0) })
			assertBackgroundGapPixel(t, et.Image().RGBAAt(700, 38), want.RGBAAt(700, 38), fmt.Sprintf("editor separator dark=%v opacity=%d", dark, opacity), 1)
			if opacity == 80 {
				saveBackgroundShot(t, et, fmt.Sprintf("editor-separator-dark%v", dark))
			}
		}
	}
}
