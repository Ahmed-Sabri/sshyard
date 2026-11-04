package app

import (
	"bytes"
	"errors"
	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/vault"
	"os"
	"testing"
)

func TestSnippetBackupRoundTrip(t *testing.T) {
	src, tt, dir := backupApp(t)
	n, err := src.saveSnippet(assets.Snippet{Name: "deploy", Group: "work", SendEnter: true}, "echo private-snippet-backup-marker")
	if err != nil {
		t.Fatal(err)
	}
	path := export(t, src, tt, dir, "correct horse", true)
	sealed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("private-snippet-backup-marker")) {
		t.Fatal("backup plaintext")
	}
	plain, err := vault.Open(sealed, []byte("correct horse"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := unpackBackup(plain)
	if err != nil {
		t.Fatal(err)
	}
	dst, _, _ := backupApp(t)
	if err := dst.restoreBackup(en, payload); err != nil {
		t.Fatal(err)
	}
	if got, err := dst.snippetContent(n.ID); err != nil || got != "echo private-snippet-backup-marker" {
		t.Fatal("restored body", err)
	}
	if got, _ := dst.store.Snippet(n.ID); got != n {
		t.Fatal("restored metadata", got)
	}
	without, err := src.collectBackup(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(without.Secrets) != 0 {
		t.Fatal("secrets exported without opt-in")
	}
	fresh, _, _ := backupApp(t)
	if err := fresh.restoreBackup(en, without); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.snippetContent(n.ID); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatal("missing body", err)
	}
	without.Assets.Snippets[0].Name = "renamed without body"
	if err := dst.restoreBackup(en, without); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.snippetContent(n.ID); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatal("stale body retained", err)
	}
}

func TestSnippetBackupRejectsInvalidContent(t *testing.T) {
	a, _, _ := backupApp(t)
	n, err := a.saveSnippet(assets.Snippet{Name: "original"}, "echo old")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, key, value string }{
		{n.ID, keychain.SnippetContent(n.ID), "echo\x1b[31m"},
		{n.ID, keychain.SnippetContent(n.ID), ""},
		{"../bad", keychain.SnippetContent("../bad"), "echo invalid id"},
	} {
		p := backupPayload{Assets: assets.Snapshot{Snippets: []assets.Snippet{{ID: tc.id, Name: "new"}}}, Settings: a.settings, Secrets: map[string][]byte{tc.key: []byte(tc.value)}}
		if err := a.restoreBackup(en, p); err == nil {
			t.Fatal("invalid payload accepted", tc.key)
		}
		if got, err := a.snippetContent(n.ID); err != nil || got != "echo old" {
			t.Fatal("invalid restore changed body", err)
		}
		if got, _ := a.store.Snippet(n.ID); got.Name != "original" {
			t.Fatal("invalid restore changed metadata")
		}
	}
}

func TestSnippetRestoreFailureRollsBackBodies(t *testing.T) {
	for _, withBody := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-body", true: "replacement-body"}[withBody], func(t *testing.T) {
			a, _, _ := backupApp(t)
			opts, _ := installBackupStore(t, a)
			n, err := a.saveSnippet(assets.Snippet{Name: "original"}, "echo old")
			if err != nil {
				t.Fatal(err)
			}
			p := backupPayload{Assets: assets.Snapshot{Snippets: []assets.Snippet{{ID: n.ID, Name: "new"}}}, Settings: a.settings}
			if withBody {
				p.Secrets = map[string][]byte{keychain.SnippetContent(n.ID): []byte("echo new")}
			}
			blockSnippetMetadata(t, a)
			if err := a.restoreBackup(en, p); err == nil {
				t.Fatal("metadata write failure ignored")
			}
			if got, _ := a.store.Snippet(n.ID); got.Name != "original" {
				t.Fatal("metadata not rolled back")
			}
			if err := a.kc.Close(); err != nil {
				t.Fatal(err)
			}
			a.kc, err = keychain.Open(opts)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := a.snippetContent(n.ID); err != nil || got != "echo old" {
				t.Fatal("encrypted body rollback not persisted", err)
			}
		})
	}
}

func TestSnippetBackupIgnoresUnownedSecret(t *testing.T) {
	a, _, _ := backupApp(t)
	p := backupPayload{Settings: a.settings, Secrets: map[string][]byte{keychain.SnippetContent("unowned"): []byte("echo unowned")}}
	if err := a.restoreBackup(en, p); err != nil {
		t.Fatal(err)
	}
	if _, err := a.kc.Get(keychain.SnippetContent("unowned")); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatal("unowned secret imported", err)
	}
}
