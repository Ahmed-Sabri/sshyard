package keychain

import "errors"

var (
	ErrNotFound      = errors.New("keychain: secret not found")
	ErrUnavailable   = errors.New("keychain: system keychain unavailable")
	ErrLocked        = errors.New("keychain: secret store locked")
	ErrBusy          = errors.New("keychain: secret store in use")
	ErrMasterMissing = errors.New("keychain: master key missing")
	ErrMasterInvalid = errors.New("keychain: invalid master key")
	ErrCorrupt       = errors.New("keychain: encrypted store damaged or key mismatched")
	ErrVersion       = errors.New("keychain: unsupported encrypted store version")
)

// OpError identifies the failed stage without requiring UI to render raw errors.
// Op is lock, master-read, master-write, master-verify, file-read,
// file-decrypt, file-write, or closed.
type OpError struct {
	Op  string
	Err error
}

func (e *OpError) Error() string { return "keychain: " + e.Op + ": " + e.Err.Error() }
func (e *OpError) Unwrap() error { return e.Err }

// SystemError preserves a structured OSStatus and its original error chain.
type SystemError struct {
	Code int
	Err  error
}

func (e *SystemError) Error() string        { return e.Err.Error() }
func (e *SystemError) Unwrap() error        { return e.Err }
func (e *SystemError) Is(target error) bool { return e.Code == -25291 && target == ErrUnavailable }
