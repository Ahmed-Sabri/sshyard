package app

import (
	"errors"
	"net/netip"
	"reflect"
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/migration"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

type migrationAction uint8

const (
	migrationSkip migrationAction = iota
	migrationOverwrite
	migrationKeepBoth
)

type migrationRow struct {
	entry             migration.Entry
	name, group, pass string
	selected          bool
	saveSecret        bool
	action            migrationAction
}
type migrationForm struct {
	source                       migration.Source
	rows                         []*migrationRow
	issues                       []migration.Issue
	baseline                     []assets.Host
	search, group, err           string
	saveSecrets, reading, saving bool
}

func migrationTarget(address string, port int, user string) string {
	address = strings.Trim(strings.TrimSpace(address), "[]")
	if ip, err := netip.ParseAddr(address); err == nil {
		address = ip.Unmap().String()
	}
	if port == 0 {
		port = 22
	}
	return strings.ToLower(address) + "\x00" + strconv.Itoa(port) + "\x00" + user
}
func (a *App) newMigration(source migration.Source, result migration.Result) *migrationForm {
	f := &migrationForm{source: source, issues: result.Issues, baseline: a.store.List()}
	for _, e := range result.Entries {
		if e.Port == 0 {
			e.Port = 22
		}
		if e.User == "" {
			e.User = a.localUser()
		}
		f.rows = append(f.rows, &migrationRow{entry: e, name: e.Name, group: e.Group, selected: true, saveSecret: true})
	}
	return f
}
func (f *migrationForm) visible(r *migrationRow) bool {
	return strings.Contains(strings.ToLower(r.name+" "+r.entry.Address+" "+r.entry.User+" "+r.group), strings.ToLower(f.search))
}
func (f *migrationForm) selectVisible(selected bool) {
	for _, r := range f.rows {
		if f.visible(r) {
			r.selected = selected
		}
	}
}

// migrationChoices derives conflicts from the current selection, including
// internal duplicates whose earlier row may have been deselected.
func (f *migrationForm) choices() (map[*migrationRow]bool, map[string]*migrationRow) {
	conflicts := map[*migrationRow]bool{}
	targets := map[string]string{}
	final := map[string]*migrationRow{}
	for _, h := range f.baseline {
		targets[migrationTarget(h.Address, h.Port, h.User)] = h.ID
	}
	for i, r := range f.rows {
		target := migrationTarget(r.entry.Address, r.entry.Port, r.entry.User)
		id, exists := targets[target]
		conflicts[r] = exists
		if !r.selected || exists && r.action == migrationSkip {
			continue
		}
		if !exists || r.action == migrationKeepBoth {
			id = "row:" + strconv.Itoa(i)
		}
		targets[target] = id
		final[id] = r
	}
	return conflicts, final
}
func (f *migrationForm) count() int { _, rows := f.choices(); return len(rows) }
func (f *migrationForm) needsSecrets() bool {
	if !f.saveSecrets {
		return false
	}
	_, rows := f.choices()
	for _, r := range rows {
		e := r.entry
		if r.saveSecret && (len(e.Password) > 0 || len(e.PrivateKey) > 0 || e.KeyPath != "") {
			return true
		}
	}
	return false
}
func (a *App) closeMigration() {
	if f := a.migration; f != nil && !f.saving {
		for _, r := range f.rows {
			clear(r.entry.Password)
			clear(r.entry.PrivateKey)
			clear(r.entry.Passphrase)
			r.pass = ""
		}
		a.migration = nil
		a.migrationOpen = false
	}
}
func (a *App) readMigration(m locales.Messages, source migration.Source, directory bool) {
	f := &migrationForm{source: source, reading: true}
	a.migration, f.reading, a.migrationOpen = f, true, true
	go func() {
		var paths []string
		var err error
		if directory {
			var path string
			path, err = a.p.PickFolder(m.MigrationChooseFolder(), "")
			if path != "" {
				paths = []string{path}
			}
		} else {
			paths, err = a.p.PickFiles(m.MigrationChooseFiles())
		}
		var result migration.Result
		if err == nil && len(paths) > 0 {
			result, err = migration.Read(source, paths)
		}
		a.post(func() {
			if a.migration != f {
				clearMigrationResult(result)
				return
			}
			f.reading = false
			if err != nil {
				clearMigrationResult(result)
				f.err = migrationReadError(m, err)
				return
			}
			if len(paths) == 0 {
				a.closeMigration()
				return
			}
			next := a.newMigration(source, result)
			if len(next.rows) == 0 {
				next.err = m.MigrationEmpty()
			}
			a.migration = next
		})
	}()
}
func (a *App) startMigrationSave(m locales.Messages) {
	f := a.migration
	if f == nil || f.saving || f.reading || f.count() == 0 {
		return
	}
	if f.needsSecrets() && !a.unlocked() {
		a.withSecrets(func() {
			if a.migration == f {
				a.startMigrationSave(m)
			}
		})
		return
	}
	f.saving = true
	f.err = ""
	// Freeze editor input while the batch runs off the UI thread.
	go func() {
		count, err := a.saveMigration(m, f)
		a.post(func() {
			if a.migration != f {
				return
			}
			f.saving = false
			if err != nil {
				f.err = err.Error()
				return
			}
			a.closeMigration()
			a.toast(func(m locales.Messages) ui.Toast {
				return ui.Toast{Title: m.MigrationDone(strconv.Itoa(count)), Type: "success"}
			})
		})
	}()
}
func (a *App) migrationUnchanged(f *migrationForm) bool {
	return reflect.DeepEqual(f.baseline, a.store.List())
}

func clearMigrationResult(result migration.Result) {
	for _, e := range result.Entries {
		clear(e.Password)
		clear(e.PrivateKey)
		clear(e.Passphrase)
	}
}

func migrationReadError(m locales.Messages, err error) string {
	switch {
	case errors.Is(err, migration.ErrNoEntries):
		return m.MigrationEmpty()
	case errors.Is(err, migration.ErrTooLarge):
		return m.MigrationTooLarge()
	case errors.Is(err, migration.ErrFormat):
		return m.MigrationFormatError()
	case errors.Is(err, migration.ErrEncrypted):
		return m.MigrationEncryptedSource()
	default:
		return m.MigrationReadError()
	}
}
