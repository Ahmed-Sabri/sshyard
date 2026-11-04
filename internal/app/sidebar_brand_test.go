package app

import (
	"fmt"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestSidebarCounts(t *testing.T) {
	a, _ := newTestApp(t)
	for _, populated := range []bool{false, true} {
		if populated {
			if _, err := a.Store().Add(assets.Host{Name: "web", Address: "10.0.0.5", User: "root"}); err != nil {
				t.Fatal(err)
			}
		}
		for _, lang := range []string{"en", "zh-Hans", "zh-Hant", "ko", "ja"} {
			for _, dark := range []bool{false, true} {
				for _, width := range []float32{160, 240} {
					t.Run(fmt.Sprintf("hosts=%v/%s/dark=%v/width=%v", populated, lang, dark, width), func(t *testing.T) {
						tt := ui.NewTester(func(c *ui.Context) {
							a.sidebarView(c, locales.In(c.Locale()), currentTokens(c))
						}, int(width), 500)
						tt.SetLocale(lang)
						tt.SetDark(dark)
						tt.Frame()
						connections := mustFind(t, tt, locales.In(lang).ConnectionCount(0))
						count := mustFind(t, tt, locales.In(lang).HostCount(a.store.Len()))
						if count.X != 12 || count.Y < 500-statusBarHeight {
							t.Fatalf("host count is not at the sidebar bottom left: %v", count)
						}
						if connections.X+connections.W > width-11 || connections.X+connections.W < width-13 {
							t.Fatalf("connection count is not at the sidebar right: %v", connections)
						}
						if count.X+count.W > connections.X-11 {
							t.Fatalf("host count %v overlaps connections %v", count, connections)
						}
						if tt.HasText(locales.In(lang).AppName()) {
							t.Fatal("brand remains in the sidebar footer")
						}
						saveBrandScreenshot(t, tt, fmt.Sprintf("sidebar-brand-%v-%s-%v-%v", populated, lang, dark, width))
					})
				}
			}
		}
	}
}
