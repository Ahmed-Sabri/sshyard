package app

import (
	"fmt"
	"image/color"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// Render the actual setting, host and monitor builders over the same content
// region used in the window; a clear card must reveal the background image.
func TestBackgroundInlineCards(t *testing.T) {
	for _, name := range []string{"settings", "host", "monitor"} {
		t.Run(name, func(t *testing.T) {
			a, _ := newTestApp(t)
			h, err := a.store.Add(assets.Host{Name: "pixel host", Address: "example.com", User: "test"})
			if err != nil {
				t.Fatal(err)
			}
			a.selected = h.ID
			renderBackgroundFixture(a)
			a.settings.Background.ContentOpacity = 0
			tok := theme.LightTokens()
			m := locales.In("en")
			tt := ui.NewTester(func(c *ui.Context) {
				a.drawBackground(c, tok)
				ui.Column(c).Fill().Background(a.backgroundContentFill(tok.Canvas)).Padding(20).Children(func() {
					switch name {
					case "settings":
						a.settingsSection(c, tok, icons.Appearance, tok.Accent, "", func(g *settingsGroup) { g.row("setting", "", func() {}) })
					case "host":
						a.hostPanel(c, m, tok)
					case "monitor":
						a.cpuCard(c, m, tok, "", session.Reading{CPUOK: true, CPU: 42})
					}
				})
			}, 400, 300)
			for _, dark := range []bool{false, true} {
				tok = theme.For(theme.Auto, dark)
				tt.SetDark(dark)
				for _, opacity := range []int{0, 50, 100} {
					a.settings.Background.ContentOpacity = opacity
					tt.Frame()
					got, outside := tt.Image().RGBAAt(300, 25), tt.Image().RGBAAt(300, 15)
					if got != outside {
						t.Errorf("%s dark=%v opacity=%d adds a second fill: inside %v outside %v", name, dark, opacity, got, outside)
					}
					if opacity == 0 && got != (color.RGBA{R: 20, G: 30, B: 240, A: 255}) {
						t.Errorf("clear %s card blocks background: %v", name, got)
					}
					if opacity == 100 && got != (color.RGBA{R: tok.Canvas.R, G: tok.Canvas.G, B: tok.Canvas.B, A: 255}) {
						t.Errorf("opaque %s card got %v want canvas %v", name, got, tok.Canvas)
					}
					saveBackgroundShot(t, tt, fmt.Sprintf("inline-%s-dark%v-opacity%d", name, dark, opacity))
				}
				a.settings.Background.Image = ""
				tt.Frame()
				if got := tt.Image().RGBAAt(300, 25); got != (color.RGBA{R: tok.Inset.R, G: tok.Inset.G, B: tok.Inset.B, A: 255}) {
					t.Errorf("no background %s changed fill: %v want %v", name, got, tok.Inset)
				}
				renderBackgroundFixture(a)
			}

		})
	}
}

// Native material and image transparency use the same inset-card seam;
// changing the content material only works when the native window supports it.
func TestBackgroundInlineNativeMaterial(t *testing.T) {
	a, tt := newTestApp(t)
	a.openSettings(prefAppearance)
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		for _, vibrant := range []bool{false, true} {
			tt.SetVibrancy(vibrant)
			for _, translucent := range []bool{false, true} {
				a.settings.ContentMaterial = settings.MaterialOpaque
				if translucent {
					a.settings.ContentMaterial = settings.MaterialTranslucent
				}
				tt.Frame()
				label := mustFind(t, tt, "Language")
				x, y := label.X-8, label.Y+label.H/2
				got := alphaAt(tt.Image(), x, y)
				if clear := got < 255; clear != (vibrant && translucent) {
					t.Errorf("dark=%v vibrancy=%v translucent=%v card alpha=%d", dark, vibrant, translucent, got)
				}
			}
		}
	}
}
