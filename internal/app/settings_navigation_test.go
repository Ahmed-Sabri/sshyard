package app

import (
	"fmt"
	"testing"

	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestSettingsExitNavigation(t *testing.T) {
	for _, lang := range []string{"en", "zh-Hans", "zh-Hant", "ko", "ja"} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dark=%v", lang, dark), func(t *testing.T) {
				a, tt := newTestApp(t)
				tt.SetLocale(lang)
				tt.SetDark(dark)
				tt.Frame()
				m := locales.In(lang)
				mustClick(t, tt, m.Settings())
				tt.Frame()
				back := mustFind(t, tt, m.Back())
				if back.H > 24 {
					t.Fatalf("back button is too tall: %v", back)
				}
				if back.Y < 0 || back.Y+back.H > toolbarHeight || back.X+back.W > a.sbWidth {
					t.Fatalf("back %v is not in the title bar above the sidebar", back)
				}
				mustClick(t, tt, m.TerminalSection())
				tt.Move(600, 700)
				tt.Frame()
				img := tt.Image()
				inside := img.RGBAAt(int(back.X+3), int(back.Y+back.H/2))
				outside := img.RGBAAt(int(back.X-2), int(back.Y+back.H/2))
				if inside == outside {
					t.Fatal("back button has no visible background at rest")
				}
				close := mustFind(t, tt, m.CloseSettings())
				if close.Y+close.H > toolbarHeight || close.X < a.sbWidth {
					t.Fatalf("close %v is not in the title bar", close)
				}
				assertSettingsCloseRed(t, tt, close, theme.For(a.settings.Theme, dark).Danger)
				saveBrandScreenshot(t, tt, fmt.Sprintf("settings-navigation-%s-%v", lang, dark))
				mustClick(t, tt, m.Back())
				if a.page != pageWorkspace || !tt.HasText(m.Settings()) {
					t.Fatal("back did not restore workspace and settings button")
				}
				mustClick(t, tt, m.Settings())
				if a.prefNow() != prefTerminal {
					t.Fatal("reopening settings lost the selected page")
				}
				mustClick(t, tt, m.CloseSettings())
				if a.page != pageWorkspace || tt.HasText(m.Back()) {
					t.Fatal("close did not restore workspace")
				}
			})
		}
	}
}

func assertSettingsCloseRed(t *testing.T, tt *ui.Tester, r ui.Rect, want ui.Color) {
	t.Helper()
	img := tt.Image()
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			p := img.RGBAAt(x, y)
			if nearColor(p.R, p.G, p.B, want.R, want.G, want.B) {
				return
			}
		}
	}
	t.Fatal("settings close icon is not red at rest")
}

func TestSettingsBackTitleBarInsets(t *testing.T) {
	for _, bar := range []ui.TitleBar{{Height: 52, Left: 80}, {Height: 40, Right: 138}, {}} {
		t.Run(fmt.Sprintf("left=%v/right=%v", bar.Left, bar.Right), func(t *testing.T) {
			a, tt := newTestApp(t)
			tt.SetTitleBar(bar)
			tt.Frame()
			brand := mustFind(t, tt, "SSHYard")
			if brand.Y+brand.H > max(bar.Height, float32(toolbarHeight)) || brand.X < bar.Left+theme.Space(2) || brand.X+brand.W > a.sbWidth {
				t.Fatalf("brand %v does not follow title bar insets %v", brand, bar)
			}
			a.openSettings(prefAppearance)
			tt.Frame()
			tt.Frame()
			back := mustFind(t, tt, "Back")
			height := max(bar.Height, float32(toolbarHeight))
			if back.X != bar.Left+theme.Space(2) || back.Y+back.H/2 != height/2 {
				t.Fatalf("back %v does not follow title bar insets %v", back, bar)
			}
			if back.X+back.W > a.sbWidth {
				t.Fatalf("back %v overflows the sidebar", back)
			}
			saveBrandScreenshot(t, tt, fmt.Sprintf("settings-titlebar-left-%v-right-%v", bar.Left, bar.Right))
			mustClick(t, tt, "Back")
			if a.page != pageWorkspace || tt.HasText("Back") {
				t.Fatal("title bar back did not restore workspace")
			}
		})
	}
}
