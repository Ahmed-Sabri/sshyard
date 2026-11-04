package app

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/egoist/mygo/ui"
)

// fakeUpdates is the update window in tests: it counts the checks the
// user asked for and records the automatic checks the app chose.
type fakeUpdates struct {
	enabled   bool
	checks    int
	automatic []bool
	available string
}

func (f *fakeUpdates) Enabled() bool                { return f.enabled }
func (f *fakeUpdates) Check()                       { f.checks++ }
func (f *fakeUpdates) SetAutomatic(on bool)         { f.automatic = append(f.automatic, on) }
func (f *fakeUpdates) Available() string            { return f.available }
func (f *fakeUpdates) OnChange(func()) (off func()) { return func() {} }

func newUpdatesApp(t *testing.T, upd *fakeUpdates) (*App, *ui.Tester) {
	t.Helper()
	dir := t.TempDir()
	a, err := New(Platform{DataDir: dir, Secrets: keychain.NewMemory(), HomeDir: dir, CacheDir: filepath.Join(dir, "cache"), Updates: upd})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	a.Start()
	tt := ui.NewTester(a.View, 1200, 1600)
	tt.SetLocale("en")
	tt.Frame()
	return a, tt
}

func TestUpdateSettings(t *testing.T) {
	upd := &fakeUpdates{enabled: true}
	a, tt := newUpdatesApp(t, upd)
	if !slices.Equal(upd.automatic, []bool{true}) {
		t.Fatalf("automatic checks %v, want the settings' on", upd.automatic)
	}
	a.openSettings(prefAbout)
	tt.Frame()
	for _, s := range []string{"Check every", "Takes effect after SSHYard relaunches.", "1 h", "2 h", "6 h", "12 h", "24 h"} {
		if !tt.HasText(s) {
			t.Errorf("about misses %q: %q", s, tt.Texts())
		}
	}
	if tt.HasText("View Update") || tt.HasText("Updates are not enabled for this build.") {
		t.Errorf("update row without an update: %q", tt.Texts())
	}

	mustClick(t, tt, "6 h")
	tt.Frame()
	if a.Settings().UpdateIntervalHours != 6 {
		t.Fatalf("interval %d, want 6", a.Settings().UpdateIntervalHours)
	}
	if s, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json")); err != nil || s.UpdateIntervalHours != 6 {
		t.Fatalf("saved interval %d %v", s.UpdateIntervalHours, err)
	}

	a.updateCheck = false
	s := a.Settings()
	s.CheckUpdates = false
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(upd.automatic, []bool{true, false}) {
		t.Fatalf("automatic checks %v after turning them off", upd.automatic)
	}

	mustClick(t, tt, "Check for Updates")
	if upd.checks != 1 || a.page != pageSettings {
		t.Fatalf("about check: %d checks, page %v", upd.checks, a.page)
	}

	// A newer version shows in the about page, the status bar and the
	// title bar; each opens the update window with its notes.
	upd.available = "9.9.9"
	tt.Frame()
	if n := textCount(tt, "Version 9.9.9 is available."); n != 2 {
		t.Errorf("available version shown %d times in the title bar and about: %q", n, tt.Texts())
	}
	mustClick(t, tt, "View Update")
	a.closeSettings()
	tt.Frame()
	mustClick(t, tt, "Update")
	if upd.checks != 3 {
		t.Fatalf("%d checks after view and status bar", upd.checks)
	}
	a.openSettings(prefAbout)

	upd.enabled, upd.available = false, ""
	tt.Frame()
	if !tt.HasText("Updates are not enabled for this build.") {
		t.Errorf("no disabled note: %q", tt.Texts())
	}
}

func TestUpdateSettingsZh(t *testing.T) {
	a, tt := newUpdatesApp(t, &fakeUpdates{enabled: true, available: "9.9.9"})
	tt.SetLocale("zh-Hans")
	a.openSettings(prefAbout)
	tt.Frame()
	for _, s := range []string{"检查频率", "重启 SSHYard 后生效。", "1 小时", "24 小时", "查看更新"} {
		if !tt.HasText(s) {
			t.Errorf("about misses %q: %q", s, tt.Texts())
		}
	}
}
