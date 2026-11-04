package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/migration"
)

func TestMigrationInternalOverwriteUsesFinalRow(t *testing.T) {
	a, _ := newTestApp(t)
	jump := migrationEntry("jump", "jump")
	first := migrationEntry("first", "target")
	first.Password = []byte("old")
	first.Jumps = []migration.Hop{{Ref: "jump"}}
	last := migrationEntry("last", "target")
	last.Password = []byte("final")
	last.Jumps = []migration.Hop{{Ref: "jump"}}
	f := migrationDraft(a, jump, first, last)
	f.saveSecrets = true
	f.rows[2].action = migrationOverwrite
	n, err := a.saveMigration(en, f)
	if err != nil || n != 2 {
		t.Fatalf("internal overwrite %d %v", n, err)
	}
	for _, h := range a.store.List() {
		if h.Address == "target" {
			if h.Name != "last" || len(h.JumpChain) != 1 {
				t.Fatalf("merged rows corrupted metadata: %+v", h)
			}
			secret, err := a.kc.Get(keychain.Host(h.ID, keychain.Password))
			if err != nil || string(secret) != "final" {
				t.Fatal("earlier row credential survived overwrite")
			}
		}
	}
	// A skipped invalid duplicate is not an attempted write.
	bad := last
	bad.Issues = []migration.Issue{{Code: "invalid-entry"}}
	f = migrationDraft(a, bad, migrationEntry("new", "another"))
	if n, err := a.saveMigration(en, f); err != nil || n != 1 {
		t.Fatalf("skipped invalid duplicate %d %v", n, err)
	}
}
func TestMigrationWriteAndRollbackFailures(t *testing.T) {
	for _, rollbackFails := range []bool{false, true} {
		t.Run(fmt.Sprint(rollbackFails), func(t *testing.T) {
			a, _, backend := newFlakyApp(t)
			path := filepath.Join(t.TempDir(), "hosts.json")
			var err error
			a.store, err = assets.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			h, err := a.store.Add(assets.Host{Name: "before", Address: "host", User: "root", Auth: assets.AuthPassword})
			if err != nil {
				t.Fatal(err)
			}
			key := keychain.Host(h.ID, keychain.Password)
			if err := a.kc.Set(key, []byte("original")); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			writes := 0
			backend.failOn(func(k string) bool {
				if k == key {
					writes++
					return rollbackFails && writes > 1
				}
				return false
			}, nil)
			e := migrationEntry("after", "host")
			e.Password = []byte("replacement")
			f := migrationDraft(a, e)
			f.saveSecrets = true
			f.rows[0].action = migrationOverwrite
			n, err := a.saveMigration(en, f)
			if n != 0 || err == nil || !strings.Contains(err.Error(), en.MigrationSaveError()) {
				t.Fatalf("failure %d %v", n, err)
			}
			if rollbackFails && !strings.Contains(err.Error(), "restore") {
				t.Fatalf("rollback error hidden: %v", err)
			}
			got, _ := a.store.Get(h.ID)
			if got.Name != "before" {
				t.Fatal("failed batch changed metadata")
			}
			if !rollbackFails {
				secret, err := a.kc.Get(key)
				if err != nil || string(secret) != "original" {
					t.Fatal("metadata failure did not roll back secret")
				}
			}
		})
	}
	a, _, backend := newFlakyApp(t)
	e := migrationEntry("host", "host")
	e.Password = []byte("value")
	f := migrationDraft(a, e)
	f.saveSecrets = true
	backend.failOn(func(string) bool { return true }, nil)
	if n, err := a.saveMigration(en, f); err == nil || n != 0 || a.store.Len() != 0 {
		t.Fatal("secret write failure reported successful import")
	}
}
func TestMigrationCancelClearsTransientSecrets(t *testing.T) {
	a, _ := newTestApp(t)
	e := migrationEntry("host", "host")
	e.Password = []byte("secret")
	e.PrivateKey = []byte("key")
	e.Passphrase = []byte("pass")
	f := migrationDraft(a, e)
	f.rows[0].pass = "typed"
	a.migration = f
	a.migrationOpen = true
	a.closeMigration()
	if !bytes.Equal(e.Password, make([]byte, len(e.Password))) || !bytes.Equal(e.PrivateKey, make([]byte, len(e.PrivateKey))) || f.rows[0].pass != "" {
		t.Fatal("cancel retained secret input")
	}
}

func TestMigrationDuplicateSelectionAndExpandedJumpValidation(t *testing.T) {
	a, _, backend := newFlakyApp(t)
	first := migrationEntry("first", "same")
	second := migrationEntry("second", "same")
	f := migrationDraft(a, first, second)
	f.rows[0].selected = false
	if f.count() != 1 {
		t.Fatal("deselected earlier duplicate still blocks row")
	}
	if n, err := a.saveMigration(en, f); err != nil || n != 1 || a.store.List()[0].Name != "second" {
		t.Fatalf("unique remaining row %d %v", n, err)
	}
	for _, diamond := range []bool{false, true} {
		a.store, _ = assets.Open(filepath.Join(t.TempDir(), "hosts.json"))
		bottom := migrationEntry("bottom", "bottom")
		left := migrationEntry("left", "left")
		left.Jumps = []migration.Hop{{Ref: "bottom"}}
		right := migrationEntry("right", "right")
		right.Jumps = []migration.Hop{{Ref: "bottom"}}
		root := migrationEntry("root", "root")
		root.Password = []byte("must-not-write")
		root.Jumps = []migration.Hop{{Ref: "left"}, {Ref: "left"}}
		if diamond {
			root.Jumps[1].Ref = "right"
		}
		f := migrationDraft(a, bottom, left, right, root)
		f.saveSecrets = true
		writes := 0
		backend.failOn(func(string) bool { writes++; return false }, func(string) bool { writes++; return false })
		if n, err := a.saveMigration(en, f); err == nil || n != 0 || writes != 0 {
			t.Fatalf("invalid expanded chain wrote credentials: count=%d writes=%d err=%v", n, writes, err)
		}
	}
}

func TestMigrationConditionalRestorePreservesConcurrentEdit(t *testing.T) {
	a, _, backend := newFlakyApp(t)
	h, err := a.store.Add(assets.Host{Name: "before", Address: "host", User: "root", Auth: assets.AuthPassword})
	if err != nil {
		t.Fatal(err)
	}
	key := keychain.Host(h.ID, keychain.Password)
	if err := a.kc.Set(key, []byte("original")); err != nil {
		t.Fatal(err)
	}
	e := migrationEntry("imported", "host")
	e.Password = []byte("replacement")
	f := migrationDraft(a, e)
	f.saveSecrets = true
	f.rows[0].action = migrationOverwrite
	changed := false
	backend.failOn(func(k string) bool {
		if k == key && !changed {
			changed = true
			h.Name = "concurrent edit"
			if _, err := a.store.Update(h); err != nil {
				t.Fatal(err)
			}
		}
		return false
	}, nil)
	n, err := a.saveMigration(en, f)
	if n != 0 || err == nil || err.Error() != en.MigrationStale() {
		t.Fatalf("conditional restore %d %v", n, err)
	}
	saved, _ := a.store.Get(h.ID)
	if saved.Name != "concurrent edit" {
		t.Fatal("migration overwrote a concurrent edit")
	}
	value, err := a.kc.Get(key)
	if err != nil || string(value) != "original" {
		t.Fatal("stale batch did not roll back its credential")
	}
}
