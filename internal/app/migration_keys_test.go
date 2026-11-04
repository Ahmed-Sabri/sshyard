package app

import (
	"bytes"
	"strings"
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

func TestMigrationKeyOverwriteCertificate(t *testing.T) {
	for _, scenario := range []string{"matching", "different-key", "unreadable-certificate"} {
		t.Run(scenario, func(t *testing.T) {
			a, _ := newTestApp(t)
			original, err := keys.Generate("original", nil)
			if err != nil {
				t.Fatal(err)
			}
			replacement := original
			if scenario == "different-key" {
				replacement, err = keys.Generate("replacement", nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			line, _ := signUserCert(t, original.Public)
			certificate := strings.TrimSpace(line)
			if scenario == "unreadable-certificate" {
				certificate = "invalid public certificate"
			}
			h, err := a.store.Add(assets.Host{Name: "before", Address: "host.example", User: "root", Auth: assets.AuthKey, Certificate: certificate})
			if err != nil {
				t.Fatal(err)
			}
			if err := a.kc.Set(keychain.Host(h.ID, keychain.PrivateKey), original.PEM); err != nil {
				t.Fatal(err)
			}
			e := migrationEntry("after", "host.example")
			e.PrivateKey = replacement.PEM
			f := migrationDraft(a, e)
			f.saveSecrets = true
			f.rows[0].action = migrationOverwrite
			if n, err := a.saveMigration(en, f); err != nil || n != 1 {
				t.Fatalf("key overwrite count=%d err=%v", n, err)
			}
			saved, _ := a.store.Get(h.ID)
			want := ""
			if scenario == "matching" {
				want = certificate
			}
			if saved.Certificate != want || saved.Auth != assets.AuthKey {
				t.Fatalf("certificate after overwrite got %q want %q", saved.Certificate, want)
			}
			data, err := a.kc.Get(keychain.Host(h.ID, keychain.PrivateKey))
			if err != nil {
				t.Fatal(err)
			}
			imported, err := keys.Import(data, nil)
			if err != nil || imported.Fingerprint() != replacement.Fingerprint() {
				t.Fatal("replacement key was not saved")
			}
		})
	}
}

func TestMigrationMetadataOverwritePreservesCertificate(t *testing.T) {
	a, _ := newTestApp(t)
	original, err := keys.Generate("original", nil)
	if err != nil {
		t.Fatal(err)
	}
	line, _ := signUserCert(t, original.Public)
	certificate := strings.TrimSpace(line)
	h, err := a.store.Add(assets.Host{Name: "before", Address: "host.example", User: "root", Auth: assets.AuthKey, Certificate: certificate})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.kc.Set(keychain.Host(h.ID, keychain.PrivateKey), original.PEM); err != nil {
		t.Fatal(err)
	}
	replacement, err := keys.Generate("replacement", nil)
	if err != nil {
		t.Fatal(err)
	}
	e := migrationEntry("after", "host.example")
	e.PrivateKey = replacement.PEM
	f := migrationDraft(a, e)
	f.rows[0].action = migrationOverwrite
	if n, err := a.saveMigration(en, f); err != nil || n != 1 {
		t.Fatalf("metadata overwrite count=%d err=%v", n, err)
	}
	saved, _ := a.store.Get(h.ID)
	if saved.Name != "after" || saved.Certificate != certificate || saved.Auth != assets.AuthKey {
		t.Fatal("metadata-only overwrite changed key authentication")
	}
	data, err := a.kc.Get(keychain.Host(h.ID, keychain.PrivateKey))
	if err != nil || !bytes.Equal(data, original.PEM) {
		t.Fatal("metadata-only overwrite changed saved private key")
	}
}
