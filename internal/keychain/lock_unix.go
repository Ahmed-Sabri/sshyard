//go:build darwin || linux

package keychain

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

type fileLock struct{ file *os.File }

func acquireLock(path string) (io.Closer, error) {
	f, err := openLock(path)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			err = errors.Join(ErrBusy, err)
		}
		return nil, errors.Join(err, f.Close())
	}
	return &fileLock{file: f}, nil
}
func (l *fileLock) Close() error {
	return errors.Join(unix.Flock(int(l.file.Fd()), unix.LOCK_UN), l.file.Close())
}
func atomicReplace(from, to string) error { return os.Rename(from, to) }
