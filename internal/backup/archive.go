package backup

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"time"
)

// Format and Version name the archive in its manifest.
const (
	Format  = "sshyard-backup"
	Version = 2
)

// Manifest is the archive's first file: what made it, and when.
type Manifest struct {
	Format  string    `json:"format"`
	Version int       `json:"version"`
	App     string    `json:"app,omitempty"`
	Created time.Time `json:"created"`
	Device  string    `json:"device,omitempty"`
	// Secrets says whether the archive holds secrets.
	Secrets bool `json:"secrets"`
}

const manifestName = "manifest.json"

// maxFiles bounds the files an archive may hold.
const maxFiles = 64

var (
	// ErrArchive reports an archive that is damaged or not SSHYard's.
	ErrArchive = errors.New("backup: damaged archive")
	// ErrVersion reports an archive of a newer version than this app reads.
	ErrVersion = errors.New("backup: made by a newer version of SSHYard")
)

// IsArchive reports whether plain, an opened backup, is an archive. A
// backup from before archives is one JSON document.
func IsArchive(plain []byte) bool { return bytes.HasPrefix(plain, []byte("PK\x03\x04")) }

// validName reports whether an archive may hold a file named s: a plain
// name, no folders.
func validName(s string) bool {
	if s == "" || len(s) > 64 || s == manifestName {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return s[0] != '.'
}

// Pack zips m and files, compressed, the manifest first and the others by
// name. Format and Version are filled in.
func Pack(m Manifest, files map[string][]byte) ([]byte, error) {
	m.Format, m.Version = Format, Version
	mb, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		if !validName(name) {
			return nil, errors.New("backup: bad file name " + name)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, data []byte) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: m.Created})
		if err == nil {
			_, err = w.Write(data)
		}
		return err
	}
	if err := add(manifestName, mb); err != nil {
		return nil, err
	}
	for _, name := range names {
		if err := add(name, files[name]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Unpack reads an archive Pack made: its manifest and its files, by name.
// What it unpacks is bounded by MaxSize; a file of a name Pack would not
// write, a name twice or a missing manifest is ErrArchive.
func Unpack(b []byte) (Manifest, map[string][]byte, error) {
	var m Manifest
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil || len(zr.File) == 0 || len(zr.File) > maxFiles {
		return m, nil, ErrArchive
	}
	files := map[string][]byte{}
	left := int64(MaxSize)
	var manifest []byte
	for _, f := range zr.File {
		if f.Name != manifestName && !validName(f.Name) {
			return m, nil, ErrArchive
		}
		if _, dup := files[f.Name]; dup || f.Name == manifestName && manifest != nil {
			return m, nil, ErrArchive
		}
		rc, err := f.Open()
		if err != nil {
			return m, nil, ErrArchive
		}
		var data bytes.Buffer
		n, err := data.ReadFrom(io.LimitReader(rc, left+1))
		rc.Close()
		if err != nil {
			return m, nil, ErrArchive
		}
		if left -= n; left < 0 {
			return m, nil, ErrTooLarge
		}
		if f.Name == manifestName {
			manifest = data.Bytes()
			continue
		}
		files[f.Name] = data.Bytes()
	}
	if manifest == nil || json.Unmarshal(manifest, &m) != nil || m.Format != Format {
		return Manifest{}, nil, ErrArchive
	}
	switch {
	case m.Version > Version:
		return m, nil, ErrVersion
	case m.Version < 2:
		return m, nil, ErrArchive
	}
	return m, files, nil
}
