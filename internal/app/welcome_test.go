package app

import (
	"fmt"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/locales"
)

func TestWelcomeSavedHostsKeepActions(t *testing.T) {
	a, tt := newTestApp(t)
	if _, err := a.store.Add(assets.Host{Name: "saved", Address: "host.example", User: "root"}); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.ws.ActiveTab() != nil {
		t.Fatal("fixture unexpectedly opened a session")
	}
	for _, label := range []string{"Quick Migration", locales.In("en").QuickConnect(), locales.In("en").NewHost()} {
		if !tt.HasText(label) {
			t.Fatalf("welcome with saved host lacks %q: %v", label, tt.Texts())
		}
	}
	tt.SetSize(700, 520)
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		for _, label := range []string{"Quick Migration", locales.In("en").QuickConnect(), locales.In("en").NewHost()} {
			r := mustFind(t, tt, label)
			if r.X < a.sbWidth+1 || r.X+r.W > 700-railWidth || r.Y < toolbarHeight || r.Y+r.H > 520-statusBarHeight {
				t.Fatalf("narrow welcome action outside content: %s %+v", label, r)
			}
		}
		saveBrandScreenshot(t, tt, fmt.Sprintf("welcome-saved-narrow-%v", dark))
	}
}

func TestWelcomeQuickMigrationStates(t *testing.T) {
	labels := map[string]string{"en": "Quick Migration", "zh-Hans": "快速迁移", "zh-Hant": "快速遷移", "ko": "빠른 마이그레이션", "ja": "クイック移行"}
	for _, saved := range []bool{false, true} {
		for _, lang := range []string{"en", "zh-Hans", "zh-Hant", "ko", "ja"} {
			for _, dark := range []bool{false, true} {
				t.Run(fmt.Sprintf("saved=%v/%s/dark=%v", saved, lang, dark), func(t *testing.T) {
					a, tt := newTestApp(t)
					if saved {
						if _, err := a.store.Add(assets.Host{Name: "saved", Address: "host.example", User: "root"}); err != nil {
							t.Fatal(err)
						}
					}
					tt.SetLocale(lang)
					tt.SetDark(dark)
					tt.Frame()
					m := locales.In(lang)
					if m.QuickMigration() != labels[lang] {
						t.Fatal("quick migration translation mismatch")
					}
					for _, label := range []string{labels[lang], m.QuickConnect(), m.NewHost()} {
						if !tt.HasText(label) {
							t.Fatalf("welcome lacks %q", label)
						}
						r := mustFind(t, tt, label)
						if r.X < a.sbWidth+1 || r.X+r.W > 1200-railWidth || r.Y < toolbarHeight || r.Y+r.H > 760-statusBarHeight {
							t.Fatalf("welcome action outside content: %s %+v", label, r)
						}
					}
					if saved && lang == "zh-Hans" {
						saveBrandScreenshot(t, tt, fmt.Sprintf("welcome-saved-%v", dark))
					}
					mustClick(t, tt, labels[lang])
					tt.Frame()
					if a.page != pageSettings || a.prefNow() != prefMigration || !tt.HasText(m.MigrationDesc()) || !tt.HasText("OpenSSH") || !tt.HasText("WinSCP") {
						t.Fatalf("quick migration did not show migration settings: %v", tt.Texts())
					}
					if a.importing || a.imp != nil || a.migrationOpen || a.unlock.modal || a.unlock.busy {
						t.Fatal("navigation opened an import or authorization dialog")
					}
					mustClick(t, tt, m.Back())
					tt.Frame()
					mustClick(t, tt, m.QuickConnect())
					tt.Frame()
					if !a.quicking {
						t.Fatal("quick connect unavailable")
					}
					mustClick(t, tt, m.Cancel())
					tt.Frame()
					if a.quicking {
						t.Fatal("quick connect cancel did not return to welcome")
					}
					mustClick(t, tt, m.NewHost())
					tt.Frame()
					if !a.editing {
						t.Fatal("new host unavailable")
					}
					mustClick(t, tt, m.Cancel())
					tt.Frame()
					if a.editing {
						t.Fatal("host editor cancel did not return to welcome")
					}
				})
			}
		}
	}
}

func TestWelcomeMigrationWhileSecretsLocked(t *testing.T) {
	backend := &authorizationBackend{Memory: keychain.NewMemory()}
	backend.fail.Store(true)
	a, tt := lockedTestApp(t, backend)
	if _, err := a.store.Add(assets.Host{Name: "saved", Address: "host.example", User: "root"}); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	calls := backend.calls.Load()
	mustClick(t, tt, "Quick Migration")
	tt.Frame()
	if a.page != pageSettings || a.prefNow() != prefMigration || a.importing || a.unlock.busy || a.unlock.modal || a.unlock.after != nil || backend.calls.Load() != calls {
		t.Fatal("migration navigation requested secret authorization")
	}
}
