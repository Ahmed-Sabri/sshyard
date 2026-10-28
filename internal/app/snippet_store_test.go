package app

import (
	"bytes"
	"errors"
	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"os"
	"path/filepath"
	"testing"
)

func blockSnippetMetadata(t *testing.T, a *App) {
	t.Helper()
	// Keep saved metadata while making the next atomic replacement fail.
	path := filepath.Join(a.p.DataDir, "hosts.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
}

func TestSnippetEncryptedCRUD(t *testing.T) {
	a, _ := newTestApp(t)
	content := "  printf 'private-command-marker'\r\n\ttrue "
	n, err := a.saveSnippet(assets.Snippet{Name: " name ", Group: " group ", SendEnter: true}, content)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := a.snippetContent(n.ID); err != nil || got != content {
		t.Fatal("body changed", err)
	}
	for _, path := range []string{"hosts.json", "secrets.enc"} {
		data, err := os.ReadFile(filepath.Join(a.p.DataDir, path))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(content)) || bytes.Contains(data, []byte("private-command-marker")) {
			t.Fatal("plaintext persisted in", path)
		}
	}
	n.Name = "updated"
	if _, err := a.saveSnippet(n, "echo updated"); err != nil {
		t.Fatal(err)
	}
	if got, err := a.snippetContent(n.ID); err != nil || got != "echo updated" {
		t.Fatal("update failed", err)
	}
	if err := a.deleteSnippet(n.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.store.Snippet(n.ID); ok {
		t.Fatal("metadata remains")
	}
	if _, err := a.kc.Get(keychain.SnippetContent(n.ID)); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatal("body remains", err)
	}
	n, err = a.store.SaveSnippet(assets.Snippet{Name: "missing body"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.snippetContent(n.ID); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatal("missing body hidden", err)
	}
}

func TestSnippetSecretAndMetadataFailureRollback(t *testing.T) {
	a, _, kc := newFlakyApp(t)
	n, err := a.saveSnippet(assets.Snippet{Name: "original"}, "echo old")
	if err != nil {
		t.Fatal(err)
	}
	n.Name = "new"
	kc.failOn(suffix("/content"), nil)
	if _, err := a.saveSnippet(n, "echo new"); err == nil {
		t.Fatal("secret failure ignored")
	}
	kc.failOn(nil, suffix("/content"))
	if err := a.deleteSnippet(n.ID); err == nil {
		t.Fatal("delete failure ignored")
	}
	kc.failOn(nil, nil)
	if got, err := a.snippetContent(n.ID); err != nil || got != "echo old" {
		t.Fatal("secret failure changed body", err)
	}
	blockSnippetMetadata(t, a)
	for _, fn := range []func() error{
		func() error { _, err := a.saveSnippet(n, "echo new"); return err },
		func() error { return a.deleteSnippet(n.ID) },
		func() error { _, err := a.saveSnippet(assets.Snippet{Name: "new entry"}, "echo new entry"); return err },
	} {
		if err := fn(); err == nil {
			t.Fatal("metadata failure ignored")
		}
		if got, err := a.snippetContent(n.ID); err != nil || got != "echo old" {
			t.Fatal("rollback failed", err)
		}
		if got, _ := a.store.Snippet(n.ID); got.Name != "original" || len(a.store.Snippets()) != 1 {
			t.Fatal("metadata changed")
		}
		if kc.Len() != 1 {
			t.Fatal("orphan secret after failed create")
		}
	}
}

func TestSnippetPersistenceErrorsHaveSafeStages(t *testing.T) {
	a, _, kc := newFlakyApp(t)
	n, err := a.saveSnippet(assets.Snippet{Name: "original"}, "echo old")
	if err != nil {
		t.Fatal(err)
	}
	kc.mu.Lock()
	kc.failGet = suffix("/content")
	kc.mu.Unlock()
	_, err = a.snippetContent(n.ID)
	var se *snippetStoreError
	if !errors.As(err, &se) || se.stage != "read" || !errors.Is(err, errFlaky) {
		t.Fatal("read cause/stage lost", err)
	}
	kc.mu.Lock()
	kc.failGet = nil
	kc.mu.Unlock()
	failed := false
	kc.failOn(func(string) bool {
		if !failed {
			failed = true
			return true
		}
		return false
	}, nil)
	_, err = a.saveSnippet(n, "echo changed")
	if !errors.As(err, &se) || se.stage != "write" || !errors.Is(err, errFlaky) {
		t.Fatal("write cause/stage lost", err)
	}
	kc.failOn(nil, nil)
	blockSnippetMetadata(t, a)
	_, err = a.saveSnippet(n, "echo changed")
	if !errors.As(err, &se) || se.stage != "metadata" {
		t.Fatal("metadata stage lost", err)
	}
	original, rollback := errors.New("original-private-path"), errors.New("rollback-private-path")
	err = snippetFailure("metadata", original, &secretTx{undo: []func() error{func() error { return rollback }}})
	if !errors.As(err, &se) || se.stage != "rollback" || !errors.Is(err, original) || !errors.Is(err, rollback) {
		t.Fatal("rollback causes/stage lost", err)
	}
	if err.Error() != "app: snippet rollback failed" {
		t.Fatal("error exposes details", err)
	}
}
