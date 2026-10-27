package app

import (
	"errors"

	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/locales"
)

// secretStoreErrorText classifies local store failures without exposing file
// paths or backend error text; unrelated business errors retain their wording.
func secretStoreErrorText(m locales.Messages, err error) string {
	var op *keychain.OpError
	var system *keychain.SystemError
	if errors.As(err, &op) || errors.As(err, &system) {
		return unlockErrorText(m, err)
	}
	for _, known := range []error{keychain.ErrLocked, keychain.ErrBusy, keychain.ErrMasterMissing, keychain.ErrMasterInvalid, keychain.ErrCorrupt, keychain.ErrVersion, keychain.ErrUnavailable} {
		if errors.Is(err, known) {
			return unlockErrorText(m, err)
		}
	}
	return err.Error()
}
