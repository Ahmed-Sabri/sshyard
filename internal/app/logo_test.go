package app

import (
	"testing"

	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestAppLogo(t *testing.T) {
	a, _ := newTestApp(t)
	for _, page := range []struct {
		name string
		size float32
	}{
		{"welcome", 64},
		{"about", 60},
	} {
		for _, dark := range []bool{false, true} {
			t.Run(page.name+map[bool]string{false: "/light", true: "/dark"}[dark], func(t *testing.T) {
				tt := ui.NewTester(func(c *ui.Context) {
					m, tok := locales.In("en"), currentTokens(c)
					ui.Column(c).Fill().Background(tok.Background).Children(func() {
						if page.name == "welcome" {
							a.terminalPage(c, m, tok)
						} else {
							changed := false
							a.aboutPage(c, m, tok, &a.settings, &changed)
						}
					})
				}, 900, 700)
				tt.SetDark(dark)
				r := mustFind(t, tt, locales.In("en").AppName())
				if r.W != page.size || r.H != page.size {
					t.Fatalf("logo size = %v, want %v square", r, page.size)
				}
				img := tt.Image()
				white := img.RGBAAt(int(r.X+r.W/2), int(r.Y+r.H/10))
				blue := img.RGBAAt(int(r.X+r.W/2), int(r.Y+r.H/4))
				if white.R < 220 || white.G < 220 || white.B < 220 || blue.R > 120 || blue.G < 80 || blue.B < 180 {
					t.Fatalf("application artwork missing: white background %v, blue arch %v", white, blue)
				}
			})
		}
	}
}
