package sftpsvc

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ArchiveFormat is the kind of archive Compress makes.
type ArchiveFormat int

const (
	Zip ArchiveFormat = iota
	TarGz
)

// Ext is the file name extension of an archive of the format.
func (f ArchiveFormat) Ext() string {
	if f == TarGz {
		return ".tar.gz"
	}
	return ".zip"
}

// ErrNotArchive is an archive of a kind Extract does not know.
var ErrNotArchive = errors.New("sftpsvc: not an archive")

// archiveExts are the extensions of the archives Extract opens, longest
// first, and the tar flag that reads each ("" for a zip, "-" for a plain
// tar).
var archiveExts = []struct{ ext, tar string }{
	{".tar.gz", "z"}, {".tar.bz2", "j"}, {".tar.xz", "J"},
	{".tgz", "z"}, {".tbz2", "j"}, {".tbz", "j"}, {".txz", "J"},
	{".tar", "-"}, {".zip", ""},
}

// archiveExt is the archive extension name ends with, and its tar flag.
func archiveExt(name string) (ext, flag string, ok bool) {
	low := strings.ToLower(name)
	for _, a := range archiveExts {
		if strings.HasSuffix(low, a.ext) && len(low) > len(a.ext) {
			return name[len(name)-len(a.ext):], a.tar, true
		}
	}
	return "", "", false
}

// IsArchive reports whether name is an archive Extract opens: on the
// server (remote), or on this computer, which does not read xz.
func IsArchive(name string, remote bool) bool {
	_, flag, ok := archiveExt(name)
	return ok && (remote || flag != "J")
}

// ArchiveBase is name without its archive extension, the folder an
// archive extracts into.
func ArchiveBase(name string) string {
	if ext, _, ok := archiveExt(name); ok {
		return name[:len(name)-len(ext)]
	}
	return name
}

// ShellQuote quotes s as one word of a POSIX shell.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// CompressCommand is the shell command that makes the archive dst, of
// format f, of the entries names of the remote folder dir, stored by
// their names. The names go after "./", so that none reads as an option.
func CompressCommand(dir string, names []string, dst string, f ArchiveFormat) string {
	var b strings.Builder
	b.WriteString("cd " + ShellQuote(dir) + " && ")
	if f == TarGz {
		b.WriteString("tar -czf " + ShellQuote(dst))
	} else {
		b.WriteString("zip -q -r -y " + ShellQuote(dst))
	}
	for _, n := range names {
		b.WriteString(" " + ShellQuote("./"+n))
	}
	return b.String()
}

// ExtractCommand is the shell command that extracts the remote archive
// into the folder dst, which is there.
func ExtractCommand(archive, dst string) (string, error) {
	_, flag, ok := archiveExt(archive)
	switch {
	case !ok:
		return "", ErrNotArchive
	case flag == "":
		return "unzip -q -o " + ShellQuote(archive) + " -d " + ShellQuote(dst), nil
	case flag == "-":
		flag = ""
	}
	return "tar -x" + flag + "f " + ShellQuote(archive) + " -C " + ShellQuote(dst), nil
}

// CompressLocal makes the archive dst, of format f, of the entries names
// of the local folder dir, stored by their names. Links are stored as
// links. A failed or canceled archive is removed.
func CompressLocal(ctx context.Context, dir string, names []string, dst string, f ArchiveFormat) (err error) {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(dst)
		}
	}()
	var add func(rel, p string, fi fs.FileInfo) error
	var finish func() error
	if f == TarGz {
		gz := gzip.NewWriter(out)
		tw := tar.NewWriter(gz)
		add = func(rel, p string, fi fs.FileInfo) error { return addTar(tw, rel, p, fi) }
		finish = func() error {
			if err := tw.Close(); err != nil {
				return err
			}
			return gz.Close()
		}
	} else {
		zw := zip.NewWriter(out)
		add = func(rel, p string, fi fs.FileInfo) error { return addZip(zw, rel, p, fi) }
		finish = zw.Close
	}
	for _, n := range names {
		err := filepath.Walk(filepath.Join(dir, n), func(p string, fi fs.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if p == dst {
				return nil // the archive itself, made in a folder it holds
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			return add(filepath.ToSlash(rel), p, fi)
		})
		if err != nil {
			return err
		}
	}
	return finish()
}

// linkTarget is the target of the link p, for an archive's entry of it.
func linkTarget(p string, fi fs.FileInfo) (string, error) {
	if fi.Mode()&fs.ModeSymlink == 0 {
		return "", nil
	}
	return os.Readlink(p)
}

func addTar(tw *tar.Writer, rel, p string, fi fs.FileInfo) error {
	link, err := linkTarget(p, fi)
	if err != nil {
		return err
	}
	h, err := tar.FileInfoHeader(fi, link)
	if err != nil {
		return err
	}
	h.Name = rel
	if fi.IsDir() {
		h.Name += "/"
	}
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return nil
	}
	return copyFile(tw, p)
}

func addZip(zw *zip.Writer, rel, p string, fi fs.FileInfo) error {
	link, err := linkTarget(p, fi)
	if err != nil {
		return err
	}
	h, err := zip.FileInfoHeader(fi)
	if err != nil {
		return err
	}
	h.Name = rel
	switch {
	case fi.IsDir():
		h.Name += "/"
	case fi.Mode().IsRegular():
		h.Method = zip.Deflate
	}
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	switch {
	case link != "":
		_, err = io.WriteString(w, link)
		return err
	case fi.Mode().IsRegular():
		return copyFile(w, p)
	}
	return nil
}

func copyFile(w io.Writer, p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// ExtractLocal extracts the local archive into the folder dst, which is
// there. Every entry stays inside dst (an entry naming a place outside it
// fails the extraction); links and other special files are left out.
func ExtractLocal(ctx context.Context, archive, dst string) error {
	_, flag, ok := archiveExt(archive)
	if !ok || flag == "J" {
		return ErrNotArchive
	}
	root, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer root.Close()
	if flag == "" {
		return extractZip(ctx, archive, root)
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	switch flag {
	case "z":
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	case "j":
		r = bzip2.NewReader(f)
	}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = extractDir(root, h.Name)
		case tar.TypeReg:
			err = extractFile(root, h.Name, fs.FileMode(h.Mode), tr)
		}
		if err != nil {
			return err
		}
	}
}

func extractZip(ctx context.Context, archive string, root *os.Root) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			err = extractDir(root, f.Name)
		case mode.IsRegular():
			var rc io.ReadCloser
			if rc, err = f.Open(); err == nil {
				err = extractFile(root, f.Name, mode, rc)
				rc.Close()
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// entryPath is the place of an archive's entry name inside the folder it
// extracts into, or an error for a name that leaves it.
func entryPath(name string) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	clean := path.Clean(strings.TrimSuffix(name, "/"))
	if clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || filepath.VolumeName(filepath.FromSlash(clean)) != "" {
		return "", ErrUnsafePath
	}
	return filepath.FromSlash(clean), nil
}

func extractDir(root *os.Root, name string) error {
	p, err := entryPath(name)
	if err != nil {
		return err
	}
	return root.MkdirAll(p, 0o755)
}

func extractFile(root *os.Root, name string, mode fs.FileMode, r io.Reader) error {
	p, err := entryPath(name)
	if err != nil {
		return err
	}
	if d := filepath.Dir(p); d != "." {
		if err := root.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	perm := mode.Perm()
	if perm == 0 {
		perm = 0o644
	}
	f, err := root.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm|0o200)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// LocalDirSize is how much the files under the local folder p hold, and
// how many there are. Links are not followed.
func LocalDirSize(ctx context.Context, p string) (size int64, files int, err error) {
	err = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // what cannot be read is left out
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				size += fi.Size()
				files++
			}
		}
		return nil
	})
	return size, files, err
}

// DirSize is how much the files under the remote folder p hold, and how
// many there are. Links are not followed.
func (c *Client) DirSize(ctx context.Context, p string) (size int64, files int, err error) {
	w := c.c.Walk(p)
	for w.Step() {
		if err := ctx.Err(); err != nil {
			return size, files, err
		}
		if w.Err() != nil {
			continue
		}
		if st := w.Stat(); st.Mode().IsRegular() {
			size += st.Size()
			files++
		}
	}
	return size, files, nil
}
