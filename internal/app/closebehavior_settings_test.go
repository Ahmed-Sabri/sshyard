package app

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
)

func TestCloseBehaviorSettings(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(strconv.FormatBool(dark), func(t *testing.T) {
			a, tt := newTestApp(t)
			a.openSettings(prefAppearance)
			tt.SetDark(dark)
			for _, lang := range settings.Languages {
				tt.SetLocale(lang)
				tt.Frame()
				m := locales.In(lang)
				for _, label := range []string{m.CloseBehavior(), m.CloseBehaviorNote(), m.CloseToTray()} {
					if !tt.HasText(label) {
						t.Fatalf("%s missing %q: %q", lang, label, tt.Texts())
					}
				}
				for _, choice := range []struct{ label, behavior string }{{m.CloseQuit(), settings.CloseQuit}, {m.CloseToTray(), settings.CloseToTray}} {
					current := m.CloseToTray()
					if a.Settings().CloseBehavior == settings.CloseQuit {
						current = m.CloseQuit()
					}
					mustClick(t, tt, current)
					tt.Frame()
					mustClick(t, tt, choice.label)
					tt.Frame()
					if a.Settings().CloseBehavior != choice.behavior {
						t.Fatalf("%s live close behavior %q, want %q", lang, a.Settings().CloseBehavior, choice.behavior)
					}
					saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
					if err != nil || saved.CloseBehavior != choice.behavior {
						t.Fatalf("%s saved close behavior %q, want %q, error %v", lang, saved.CloseBehavior, choice.behavior, err)
					}
				}
			}
		})
	}
}
