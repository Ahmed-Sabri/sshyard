//go:build !windows

package sftpsvc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// The test server's plain rename replaces (os.Rename), as some real
// servers' does. One window saves without posix-rename while another
// lists the folder: the listing, taken while the file was moved aside,
// must not put the old version back over the new one.
func TestListingDuringSaveKeepsNewContent(t *testing.T) {
	noPosixRename = true
	t.Cleanup(func() { noPosixRename = false; afterAside = nil; afterSwap = nil })
	cases := []struct {
		name string
		// snap filters the listing taken mid-save, as a listing read a
		// part at a time may miss the copy.
		snap func([]fs.FileInfo) []fs.FileInfo
	}{
		{"copy listed", func(l []fs.FileInfo) []fs.FileInfo { return l }},
		{"copy not listed", withoutCopies},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, root := connect(t)
			file := filepath.Join(root, "a.conf")
			os.WriteFile(file, []byte("old"), 0o644)
			var snap []fs.FileInfo
			afterAside = func(string, string) error {
				// The other window lists now: the file is missing, its
				// backup there.
				l, err := c.c.ReadDir(root)
				snap = tc.snap(l)
				return err
			}
			// and cleans up once the copy is in place, before the save
			// removed the backup.
			var un []Unfinished
			afterSwap = func(string, string) { _, un = c.cleanLeftovers(root, snap) }
			_, err := c.WriteFile(file, []byte("new content"), Version{})
			afterAside, afterSwap = nil, nil
			if err != nil {
				t.Fatal(err)
			}
			if got := read(t, file); got != "new content" {
				t.Fatalf("saved %q, the server has %q", "new content", got)
			}
			if len(un) != 1 || un[0].Name != "a.conf" {
				t.Errorf("unfinished %+v", un)
			}
			if l := leftovers(root); len(l) != 0 {
				t.Errorf("left %v", l)
			}
		})
	}
}

func withoutCopies(l []fs.FileInfo) []fs.FileInfo {
	var out []fs.FileInfo
	for _, e := range l {
		if !tempRe.MatchString(e.Name()) {
			out = append(out, e)
		}
	}
	return out
}

// The loss seen with saves and listings from two windows at once, made
// to happen every time: a listing that saw the file missing (and no
// copy, read a part at a time) while the old file was aside would rename
// the backup (the same name every save) back just after the save put its
// copy in place, replacing it. A listing renames nothing now, so every save keeps what it saved.
func TestListingNeverPutsBackupBack(t *testing.T) {
	noPosixRename = true
	t.Cleanup(func() { noPosixRename = false; afterAside = nil; afterSwap = nil })
	c, root := connect(t)
	lister, err := New(c.conn)
	if err != nil {
		t.Fatal(err)
	}
	defer lister.Close()
	file := filepath.Join(root, "a.conf")
	os.WriteFile(file, []byte("AAAA"), 0o644)
	for i := range 20 {
		data := fmt.Sprintf("B%03d", i) // the size the file was, the same second
		var snap []fs.FileInfo
		var before int
		afterAside = func(string, string) error {
			// The other window lists mid-save: the file is missing.
			l, err := lister.c.ReadDir(root)
			snap = withoutCopies(l)
			before = len(leftovers(root))
			return err
		}
		// and acts on it just after the copy is in place.
		afterSwap = func(string, string) { lister.cleanLeftovers(root, snap) }
		_, err := c.WriteFile(file, []byte(data), Version{})
		afterAside, afterSwap = nil, nil
		if err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
		if got := read(t, file); got != data {
			t.Fatalf("save %d succeeded, but the server has %q", i, got)
		}
		if before != 2 {
			t.Fatalf("save %d: %d leftovers mid-save", i, before)
		}
	}
}

// Were the old version put back over the copy anyway (by another app,
// or an older version of this one), the save says so: even when the old
// version is the same size and its time the same second (AAAA, BBBB),
// as SFTP times have a second's precision.
func TestSaveUndoneSameSizeSameSecond(t *testing.T) {
	noPosixRename = true
	t.Cleanup(func() { noPosixRename = false; afterSwap = nil })
	c, root := connect(t)
	file := filepath.Join(root, "a.conf")
	for i := range 20 {
		os.WriteFile(file, []byte("AAAA"), 0o644)
		afterSwap = func(aside, p string) {
			// Same second as the copy, as a save and an old file written
			// moments apart are.
			if fi, err := c.c.Lstat(p); err == nil {
				c.c.Chtimes(aside, fi.ModTime(), fi.ModTime())
			}
			c.c.Rename(aside, p)
		}
		_, err := c.WriteFile(file, []byte("BBBB"), Version{})
		afterSwap = nil
		if !errors.Is(err, ErrSaveUndone) || !errors.Is(err, ErrChanged) {
			t.Fatalf("save %d: err %v, the server has %q", i, err, read(t, file))
		}
	}
}
