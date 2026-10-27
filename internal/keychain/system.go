package keychain

import (
	"errors"

	"github.com/egoist/mygo"
)

// System is the Backend of the operating system's keychain, mygo.Secrets,
// under the app's identifier (com.licoy.sshyard).
type System struct{}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mygo.ErrSecretNotFound):
		return ErrNotFound
	case errors.Is(err, errors.ErrUnsupported):
		return errors.Join(ErrUnavailable, err)
	}
	return err
}

func (System) Set(key string, v []byte) error { return mapErr(mygo.Secrets.Set(key, v)) }
func (System) Delete(key string) error        { return mapErr(mygo.Secrets.Delete(key)) }
func (System) Get(key string) ([]byte, error) {
	v, err := mygo.Secrets.Get(key)
	return v, mapErr(err)
}
