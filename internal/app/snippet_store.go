package app

import (
	"errors"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
)

// snippetStoreError identifies a failed persistence stage without exposing paths
// or command content. Unwrap retains the concrete cause for diagnostics.
type snippetStoreError struct {
	stage string
	err   error
}

func (e *snippetStoreError) Error() string { return "app: snippet " + e.stage + " failed" }
func (e *snippetStoreError) Unwrap() error { return e.err }

func snippetFailure(stage string, err error, tx *secretTx) error {
	if rerr := tx.rollback(); rerr != nil {
		return &snippetStoreError{stage: "rollback", err: errors.Join(err, rerr)}
	}
	return &snippetStoreError{stage: stage, err: err}
}

func (a *App) saveSnippet(meta assets.Snippet, content string) (assets.Snippet, error) {
	meta.Normalize()
	if err := meta.Validate(); err != nil {
		return assets.Snippet{}, err
	}
	if err := assets.ValidateSnippetContent(content); err != nil {
		return assets.Snippet{}, err
	}
	create := meta.ID == ""
	if create {
		meta.ID = assets.NewID()
	}
	if !assets.ValidID(meta.ID) {
		return assets.Snippet{}, &assets.FieldError{Field: "id", Reason: "invalid"}
	}
	if !create {
		if _, ok := a.store.Snippet(meta.ID); !ok {
			return assets.Snippet{}, assets.ErrNotFound
		}
	}
	tx := &secretTx{kc: a.kc}
	if err := tx.set(keychain.SnippetContent(meta.ID), []byte(content)); err != nil {
		return assets.Snippet{}, snippetFailure("write", err, tx)
	}
	var saved assets.Snippet
	var err error
	if create {
		saved, err = a.store.AddSnippet(meta)
	} else {
		saved, err = a.store.SaveSnippet(meta)
	}
	if err != nil {
		return assets.Snippet{}, snippetFailure("metadata", err, tx)
	}
	return saved, nil
}

func (a *App) deleteSnippet(id string) error {
	if !assets.ValidID(id) {
		return &assets.FieldError{Field: "id", Reason: "invalid"}
	}
	if _, ok := a.store.Snippet(id); !ok {
		return assets.ErrNotFound
	}
	tx := &secretTx{kc: a.kc}
	if err := tx.delete(keychain.SnippetContent(id)); err != nil {
		return snippetFailure("write", err, tx)
	}
	if err := a.store.DeleteSnippet(id); err != nil {
		return snippetFailure("metadata", err, tx)
	}
	return nil
}

func (a *App) snippetContent(id string) (string, error) {
	if !assets.ValidID(id) {
		return "", &assets.FieldError{Field: "id", Reason: "invalid"}
	}
	if _, ok := a.store.Snippet(id); !ok {
		return "", assets.ErrNotFound
	}
	v, err := a.kc.Get(keychain.SnippetContent(id))
	if errors.Is(err, keychain.ErrNotFound) {
		return "", err
	}
	if err != nil {
		return "", &snippetStoreError{stage: "read", err: err}
	}
	defer clear(v)
	content := string(v)
	if err := assets.ValidateSnippetContent(content); err != nil {
		return "", err
	}
	return content, nil
}
