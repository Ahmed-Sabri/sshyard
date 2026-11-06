package keychain

import (
	"errors"
	"regexp"
	"strconv"

	"github.com/egoist/mygo"
)

// System is the Backend of the operating system's keychain, mygo.Secrets,
// under the app's identifier (com.sshyard.desktop).
type System struct{}

var osStatusSuffix = regexp.MustCompile(`\(OSStatus (-?[0-9]+)\)$`)

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mygo.ErrSecretNotFound):
		return ErrNotFound
	}
	err = mapLinuxServiceError(err)
	if errors.Is(err, errors.ErrUnsupported) {
		return errors.Join(ErrUnavailable, err)
	}
	if _, ok := err.(*LinuxServiceError); ok {
		return err
	}
	if matches := osStatusSuffix.FindStringSubmatch(err.Error()); len(matches) == 2 {
		if code, e := strconv.Atoi(matches[1]); e == nil {
			return &SystemError{Code: code, Err: err}
		}
	}
	return err
}

func (System) Set(key string, v []byte) error { return mapErr(mygo.Secrets.Set(key, v)) }
func (System) Delete(key string) error        { return mapErr(mygo.Secrets.Delete(key)) }
func (System) Get(key string) ([]byte, error) {
	v, err := mygo.Secrets.Get(key)
	return v, mapErr(err)
}

// GetSilently reads key without any system authorization prompt: where one
// would be needed, the read fails.
func (System) GetSilently(key string) ([]byte, error) {
	v, err := silently(func() ([]byte, error) { return mygo.Secrets.Get(key) })
	return v, mapErr(err)
}
