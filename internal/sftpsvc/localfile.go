package sftpsvc

import (
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// ReadLocalFile reads the local file p, of at most limit bytes
// (ErrReadLimit), and its version, as ReadFileContext does a remote one.
func ReadLocalFile(p string, limit int64) ([]byte, Version, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, Version{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, Version{}, err
	}
	if !fi.Mode().IsRegular() {
		return nil, Version{}, errors.New("sftp: not a regular file")
	}
	if fi.Size() > limit {
		return nil, Version{}, ErrReadLimit
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, Version{}, err
	}
	if int64(len(data)) > limit {
		return nil, Version{}, ErrReadLimit
	}
	return data, Version{Size: int64(len(data)), ModTime: fi.ModTime()}.withSum(data), nil
}

// WriteLocalFile replaces the local file p (the file a link names) with
// data, unless it is no longer the version was (ErrChanged); a zero was
// writes regardless. The new content goes to a hidden sibling first,
// renamed over the file with its mode, so that a failed save leaves the
// file as it was; where that cannot be (the folder is not writable), the
// file is written in place.
func WriteLocalFile(p string, data []byte, was Version) (Version, error) {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	fi, err := os.Stat(p)
	if err != nil {
		return Version{}, err
	}
	if !was.ModTime.IsZero() && localChanged(p, fi, was) {
		return Version{}, ErrChanged
	}
	if err := replaceLocal(p, data, fi.Mode().Perm()); err != nil {
		if !errors.Is(err, fs.ErrPermission) {
			return Version{}, err
		}
		if err := os.WriteFile(p, data, fi.Mode().Perm()); err != nil {
			return Version{}, err
		}
	}
	now, err := os.Stat(p)
	if err != nil {
		return Version{}, err
	}
	return Version{Size: now.Size(), ModTime: now.ModTime()}.withSum(data), nil
}

// localChanged says whether the file p, now fi, is no longer was.
func localChanged(p string, fi fs.FileInfo, was Version) bool {
	if fi.Size() != was.Size || !fi.ModTime().Equal(was.ModTime) {
		return true
	}
	if was.Sum == ([sha256.Size]byte{}) {
		return false
	}
	data, err := os.ReadFile(p)
	return err != nil || sha256.Sum256(data) != was.Sum
}

func replaceLocal(p string, data []byte, perm fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(p), ".sshyard-*-"+filepath.Base(p))
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
		done = true
		return err
	}
	done = true
	return nil
}
