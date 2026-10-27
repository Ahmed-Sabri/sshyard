package app

import (
	"errors"

	"github.com/Licoy/sshyard/internal/keychain"
)

// secretTx changes keychain entries so that they can be put back: a save
// writes its secrets first, then its metadata, and undoes the secrets when
// either step fails, so the two never disagree.
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
