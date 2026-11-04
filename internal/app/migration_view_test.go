package app

import (
	"fmt"
	"testing"

	"github.com/Licoy/sshyard/internal/migration"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestMigrationLongPreviewBounds(t *testing.T) {
	a, _ := newTestApp(t)
	var entries []migration.Entry
	for i := range 24 {
		e := migrationEntry(fmt.Sprintf("production database session %d", i), fmt.Sprintf("db-%d.example", i))
		e.Issues = []migration.Issue{{Code: "unsupported-field", Field: "tags"}, {Code: "encrypted-secret", Field: "password"}, {Code: "unsupported-field", Field: "notes"}}
		entries = append(entries, e)
	}
	for _, size := range [][2]int{{520, 760}, {1000, 900}} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/%v", size[0], size[1], dark), func(t *testing.T) {
				a.migration, a.migrationOpen = migrationDraft(a, entries...), true
				tt := ui.NewTester(func(c *ui.Context) { a.migrationView(c, locales.In(c.Locale())) }, size[0], size[1])
				tt.SetLocale("zh-Hans")
				tt.SetDark(dark)
				tt.Frame()
				m := locales.In("zh-Hans")
				for _, label := range []string{m.SearchHosts(), m.Cancel(), m.ImportButton("24")} {
					rect := mustFind(t, tt, label)
					if rect.X < 40 || rect.X+rect.W > float32(size[0])-40 || rect.Y < 20 || rect.Y+rect.H > float32(size[1])-20 {
						t.Fatalf("control %s outside dialog inner bounds: %+v", label, rect)
					}
				}
				if size[0] == 520 {
					saveBrandScreenshot(t, tt, fmt.Sprintf("migration-many-narrow-%v", dark))
				} else {
					saveBrandScreenshot(t, tt, fmt.Sprintf("migration-many-wide-%v", dark))
				}
				a.closeMigration()
			})
		}
	}
}
func TestMigrationReadErrorMapping(t *testing.T) {
	for _, lang := range []string{"en", "zh-Hans", "zh-Hant", "ko", "ja"} {
		m := locales.In(lang)
		for _, row := range []struct {
			err  error
			want string
		}{{migration.ErrRead, m.MigrationReadError()}, {migration.ErrFormat, m.MigrationFormatError()}, {migration.ErrNoEntries, m.MigrationEmpty()}, {migration.ErrTooLarge, m.MigrationTooLarge()}, {migration.ErrEncrypted, m.MigrationEncryptedSource()}} {
			if got := migrationReadError(m, fmt.Errorf("private diagnostic: %w", row.err)); got != row.want {
				t.Fatalf("%s: got %q want %q", lang, got, row.want)
			}
		}
	}
}
