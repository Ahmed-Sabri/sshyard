package sftpsvc

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// localDestination pins the existing destination ancestor. Root keeps every
// later operation confined there even when another process swaps directories.
type localDestination struct {
	root *os.Root
	base string
}

func destinationRoot(dst string) (localDestination, error) {
	abs, err := filepath.Abs(dst)
	if err != nil {
		return localDestination{}, err
	}
	// macOS's standard temporary directory uses the system /var alias.
	temp := filepath.Clean(os.TempDir())
	if real, err := filepath.EvalSymlinks(temp); err == nil && strings.HasPrefix(abs, temp+string(filepath.Separator)) {
		abs = filepath.Join(real, strings.TrimPrefix(abs, temp+string(filepath.Separator)))
	}
	parent := filepath.Dir(abs)
	for {
		fi, err := os.Lstat(parent)
		if err == nil {
			if !fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0 {
				return localDestination{}, ErrUnsafePath
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return localDestination{}, err
		}
		parent = filepath.Dir(parent)
	}
	// Reject all existing ancestor links before pinning the directory.
	for p := parent; p != filepath.Dir(p); p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil {
			return localDestination{}, err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return localDestination{}, ErrUnsafePath
		}
	}
	root, err := os.OpenRoot(parent)
	return localDestination{root, parent}, err
}
func (d localDestination) relative(p string) string {
	abs, _ := filepath.Abs(p)
	temp := filepath.Clean(os.TempDir())
	if real, err := filepath.EvalSymlinks(temp); err == nil && strings.HasPrefix(abs, temp+string(filepath.Separator)) {
		abs = filepath.Join(real, strings.TrimPrefix(abs, temp+string(filepath.Separator)))
	}
	rel, err := filepath.Rel(d.base, abs)
	if err != nil {
		return "../invalid"
	}
	return rel
}
func (d localDestination) stat(p string) (fs.FileInfo, error) { return d.root.Lstat(d.relative(p)) }
func (d localDestination) open(p string) (io.ReadSeekCloser, error) {
	return d.root.Open(d.relative(p))
}
func (d localDestination) create(p string, keep bool) (writeSeekCloser, error) {
	flags := os.O_WRONLY
	if !keep {
		flags |= os.O_CREATE | os.O_EXCL
	}
	if keep {
		fi, err := d.root.Lstat(d.relative(p))
		if err != nil {
			return nil, err
		}
		if !fi.Mode().IsRegular() {
			return nil, ErrPartChanged
		}
	}
	return d.root.OpenFile(d.relative(p), flags, 0o600)
}
func (d localDestination) mkdirAll(p string) error { return d.root.MkdirAll(d.relative(p), 0o755) }
func (d localDestination) remove(p string) error   { return d.root.Remove(d.relative(p)) }
func (d localDestination) rename(a, b string) error {
	return d.root.Rename(d.relative(a), d.relative(b))
}

// MkdirAllLocal creates download-plan directories with the same confinement
// as the files that will later populate them.
func MkdirAllLocal(p string) error {
	d, err := destinationRoot(filepath.Join(p, ".sshyard-directory"))
	if err != nil {
		return err
	}
	defer d.root.Close()
	return d.mkdirAll(p)
}

func (d localDestination) lstat(p string) (fs.FileInfo, error) { return d.stat(p) }
