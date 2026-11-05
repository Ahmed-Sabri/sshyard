package app

import (
	"errors"
	"testing"

	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestPlatformErrorVisible(t *testing.T) {
	for _, ready := range []bool{true, false} {
		name := "ready"
		if !ready {
			name = "storage unavailable"
		}
		t.Run(name, func(t *testing.T) {
			var a *App
			var tt *ui.Tester
			if ready {
				a, tt = newTestApp(t)
			} else {
				var err error
				a, err = New(Platform{DataDir: t.TempDir(), Secrets: keychain.NewMemory(), StartupError: errors.New("storage unavailable")})
				if err == nil || a.ready() {
					t.Fatal("expected storage startup failure")
				}
				a.Start()
				tt = ui.NewTester(a.View, 1200, 760)
				tt.SetLocale("en")
			}
			message := locales.In("en").TrayUnavailable()
			a.ReportError(message)
			tt.Frame()
			waitFor(t, tt, "visible native platform error", func() bool { return tt.HasText(message) })
			if len(a.toasts) != 0 {
				t.Fatal("platform error was not consumed by the visible view")
			}
			a.Close()
			if !a.closed || a.ready() {
				t.Fatal("error notification prevented application cleanup")
			}
		})
	}
}
