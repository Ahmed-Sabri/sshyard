package keychain

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

type fileLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquireLock(path string) (io.Closer, error) {
	f, err := openLock(path)
	if err != nil {
		return nil, err
	}
	l := &fileLock{file: f}
	err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &l.overlapped)
	if err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			err = errors.Join(ErrBusy, err)
		}
		return nil, errors.Join(err, f.Close())
	}
	return l, nil
}
func (l *fileLock) Close() error {
	return errors.Join(windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &l.overlapped), l.file.Close())
}
func atomicReplace(from, to string) error {
	src, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(src, dst, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
