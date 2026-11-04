package app

import (
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/keys"
)

func TestMigrationEncryptedKeyAsksAtConnection(t *testing.T) {
	a, _ := newTestApp(t)
	key, err := keys.Generate("migration", []byte("test-passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	e := migrationEntry("encrypted", "encrypted.example")
	e.PrivateKey = key.PEM
	f := migrationDraft(a, e)
	f.saveSecrets = true
	n, err := a.saveMigration(en, f)
	if err != nil || n != 1 {
		t.Fatalf("encrypted key import %d %v", n, err)
	}
	h := a.store.List()[0]
	if h.Auth != assets.AuthKey || !h.AskPassphrase {
		t.Fatal("encrypted key did not retain ask-at-connection authentication")
	}
	stored, err := a.kc.Get(keychain.Host(h.ID, keychain.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Import(stored, []byte("test-passphrase")); err != nil {
		t.Fatal("saved key no longer opens")
	}
	if exists, err := a.kc.Exists(keychain.Host(h.ID, keychain.Passphrase)); err != nil || exists {
		t.Fatal("unprovided passphrase saved")
	}
	f = migrationDraft(a, e)
	f.rows[0].action = migrationOverwrite
	f.saveSecrets = true
	f.rows[0].pass = "wrong"
	if n, err := a.saveMigration(en, f); err == nil || n != 0 {
		t.Fatal("wrong passphrase was accepted")
	}
	f.rows[0].pass = "test-passphrase"
	if n, err := a.saveMigration(en, f); err != nil || n != 1 {
		t.Fatalf("explicit passphrase import %d %v", n, err)
	}
	h, _ = a.store.Get(h.ID)
	if h.AskPassphrase {
		t.Fatal("explicit passphrase was ignored")
	}
}
