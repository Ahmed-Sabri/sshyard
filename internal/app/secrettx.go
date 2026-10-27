package app

import (
	"errors"

	"github.com/Licoy/sshyard/internal/keychain"
)

// secretTx compensates secret writes when a later metadata save fails.
// Separate metadata and secret files are not crash-atomic together.
type secretTx struct {
	kc   *keychain.Keychain
	undo []func() error
}

// before remembers what key holds now, for rollback.
func (t *secretTx) before(key string) error {
	old, err := t.kc.Get(key)
	switch {
	case errors.Is(err, keychain.ErrNotFound):
		t.undo = append(t.undo, func() error { return t.kc.Delete(key) })
	case err != nil:
		return err
	default:
		t.undo = append(t.undo, func() error { return t.kc.Set(key, old) })
	}
	return nil
}

func (t *secretTx) set(key string, v []byte) error {
	if err := t.before(key); err != nil {
		return err
	}
	return t.kc.Set(key, v)
}

// setBatch captures the old values before one encrypted-store commit.
func (t *secretTx) setBatch(changes []keychain.Change) error {
	undo := make([]keychain.Change, 0, len(changes))
	for _, change := range changes {
		old, err := t.kc.Get(change.Key)
		if err != nil && !errors.Is(err, keychain.ErrNotFound) {
			return err
		}
		undo = append(undo, keychain.Change{Key: change.Key, Value: old, Delete: errors.Is(err, keychain.ErrNotFound)})
	}
	// Include the batch in rollback even when an injected backend partially fails.
	t.undo = append(t.undo, func() error { return t.kc.Apply(undo) })
	return t.kc.Apply(changes)
}

func (t *secretTx) delete(key string) error {
	ok, err := t.kc.Exists(key)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := t.before(key); err != nil {
		return err
	}
	return t.kc.Delete(key)
}

// rollback puts every changed key back, newest first.
func (t *secretTx) rollback() error {
	var errs []error
	for i := len(t.undo) - 1; i >= 0; i-- {
		errs = append(errs, t.undo[i]())
	}
	t.undo = nil
	return errors.Join(errs...)
}
