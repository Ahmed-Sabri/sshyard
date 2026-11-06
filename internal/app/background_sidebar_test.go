package app

import (
	"fmt"
	"image/color"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/theme"
)

func TestBackgroundSidebarHostList(t *testing.T) {
	a, tt := newTestApp(t)
	if _, err := a.store.Add(assets.Host{Name: "saved background host", Address: "example.com", User: "test"}); err != nil {
		t.Fatal(err)
	}
	renderBackgroundFixture(a)
	tt.SetVibrancy(false)
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tok := theme.For(theme.Auto, dark)
		for _, opacity := range []int{0, 50, 100} {
			a.settings.Background.ChromeOpacity = opacity
			tt.Frame()
			if !tt.HasText("saved background host") {
				t.Fatal("saved host absent from sidebar")
			}
			img := tt.Image()
			got, outer := img.RGBAAt(40, 300), img.RGBAAt(4, 60)
			if got != outer {
				t.Errorf("dark=%v opacity=%d host list adds opaque fill: list %v outer %v", dark, opacity, got, outer)
			}
			if opacity == 0 && got != (color.RGBA{R: 240, G: 30, B: 20, A: 255}) {
				t.Errorf("clear sidebar dark=%v blocks image: %v", dark, got)
			}
			if opacity == 100 && got != (color.RGBA{R: tok.Sidebar.R, G: tok.Sidebar.G, B: tok.Sidebar.B, A: 255}) {
				t.Errorf("opaque sidebar dark=%v got %v want %v", dark, got, tok.Sidebar)
			}
			saveBackgroundShot(t, tt, fmt.Sprintf("sidebar-host-dark%v-opacity%d", dark, opacity))
		}
	}
}
