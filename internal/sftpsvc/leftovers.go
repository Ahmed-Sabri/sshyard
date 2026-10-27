package sftpsvc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// A save without posix-rename moves the old file aside to a backup
// (backupName), puts the new copy in its place, then removes the
// backup. Cut between the first two steps (the connection drops, the
// app quits) the old file is only at the backup; a save cut earlier
// leaves its copy (tempName). Both are hidden siblings of the file.
//
// The next save of the file looks up its backup by name and puts it
// back, or removes it when the file is there (cleanBeforeSave). Listing
// the folder never renames anything (cleanLeftovers): what it saw may
// be a moment old, and some servers' rename replaces, so putting a
// backup back from a listing could undo a save under way. Instead:
//
//   - a backup whose file is missing is reported (Unfinished), for the
//     user to restore (Restore) or discard (Discard). One whose file is
//     there is removed once it is staleLeftover old (a save dates its
//     backup when it makes it).
//   - a copy (".sshyard-<12 hex>-name", a regular file) is removed once
//     it is staleLeftover old, and its file is there: a copy that young
//     may be another save's, under way; one whose file is missing may be
//     an old file moved aside by an earlier version of this app, and is
//     kept.
//
// Only names in exactly these forms are touched.
var (
	tempRe   = regexp.MustCompile(`(?s)^\.sshyard-[0-9a-f]{12}-(.+)$`)
	backupRe = regexp.MustCompile(`(?s)^\.sshyard-old-[0-9a-f]{12}-(.+)$`)
)

// staleLeftover is how old a save's copy must be to be removed.
var staleLeftover = time.Hour

// backupName is where a save without posix-rename moves the old p:
// always the same name for p (12 hex of its name's hash), so that a save
// finds it without listing the folder. (Earlier versions picked 12
// random hex; listing still cleans those up.)
func backupName(p string) string {
	sum := sha256.Sum256([]byte(path.Base(p)))
	return path.Join(path.Dir(p), ".sshyard-old-"+hex.EncodeToString(sum[:6])+"-"+path.Base(p))
}

// leftover reports whether name is in a form cleanLeftovers handles.
func leftover(name string) bool {
	return tempRe.MatchString(name) || backupRe.MatchString(name)
}

// Unfinished is a file whose last save did not finish: it is missing,
// and its old version is at Backup, both names in the same folder.
type Unfinished struct {
	Name, Backup string
}

// cleanLeftovers cleans up what cut saves left in dir, as above, among
// the entries listed there: at most maxLeftovers of them, as a save
// did, so that a folder full of them (or a server that says so) costs a
// bounded number of requests. It reports whether it removed anything,
// and the files whose saves did not finish (newest backup first, one
// per file).
func (c *Client) cleanLeftovers(dir string, ents []fs.FileInfo) (bool, []Unfinished) {
	var backups, temps []fs.FileInfo
	for _, e := range ents {
		if !e.Mode().IsRegular() || len(backups)+len(temps) >= maxLeftovers {
			continue
		}
		if n := e.Name(); backupRe.MatchString(n) {
			backups = append(backups, e)
		} else if tempRe.MatchString(n) && time.Since(e.ModTime()) >= staleLeftover {
			temps = append(temps, e) // a younger one may be a save's under way
		}
	}
	if len(backups)+len(temps) == 0 {
		return false, nil
	}
	// Which of their files are there, without a set of every name.
	have := map[string]bool{}
	for _, b := range backups {
		have[backupRe.FindStringSubmatch(b.Name())[1]] = false
	}
	for _, t := range temps {
		have[tempRe.FindStringSubmatch(t.Name())[1]] = false
	}
	for _, e := range ents {
		if _, ok := have[e.Name()]; ok {
			have[e.Name()] = true
		}
	}
	changed := false
	var unfinished []Unfinished
	reported := map[string]bool{}
	sort.SliceStable(backups, func(i, j int) bool { return backups[i].ModTime().After(backups[j].ModTime()) })
	for _, b := range backups {
		name := backupRe.FindStringSubmatch(b.Name())[1]
		switch {
		case !have[name]:
			// The newest backup of a missing file is its last version.
			if !reported[name] {
				reported[name] = true
				unfinished = append(unfinished, Unfinished{Name: name, Backup: b.Name()})
			}
		case time.Since(b.ModTime()) >= staleLeftover:
			if c.c.Remove(path.Join(dir, b.Name())) == nil {
				changed = true
			}
		}
	}
	for _, t := range temps {
		if !have[tempRe.FindStringSubmatch(t.Name())[1]] {
			continue
		}
		if c.c.Remove(path.Join(dir, t.Name())) == nil {
			changed = true
		}
	}
	return changed, unfinished
}

// ErrRestoreTaken: a file has the name of the one to restore again.
var ErrRestoreTaken = errors.New("sftp: a file with that name is there again")

// errNotBackup: what Restore or Discard was given is not a backup of
// the file.
var errNotBackup = errors.New("sftp: not a backup of that file")

// backupOf checks that u's backup is one of its file's, in dir.
func backupOf(dir string, u Unfinished) (bp, p string, err error) {
	m := backupRe.FindStringSubmatch(u.Backup)
	if m == nil || m[1] != u.Name || strings.Contains(u.Backup, "/") {
		return "", "", errNotBackup
	}
	return path.Join(dir, u.Backup), path.Join(dir, u.Name), nil
}

// Restore puts the old version of an unfinished save back under its
// name, unless a file has that name again (ErrRestoreTaken): looked up
// right before, then with posix-rename where the server has it, else
// in ways that never replace a file (a hard link, or a copy created
// only if the name is free) rather than plain rename, which replaces
// on some servers.
func (c *Client) Restore(dir string, u Unfinished) error {
	bp, p, err := backupOf(dir, u)
	if err != nil {
		return err
	}
	fi, err := c.c.Lstat(bp)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return errNotBackup
	}
	switch _, err := c.c.Lstat(p); {
	case err == nil:
		return ErrRestoreTaken
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	if c.posixRename() {
		return c.c.PosixRename(bp, p)
	}
	if _, ok := c.c.HasExtension("hardlink@openssh.com"); ok && !noHardlink {
		err := c.c.Link(bp, p)
		if err == nil {
			c.c.Remove(bp)
			return nil
		}
		if _, serr := c.c.Lstat(p); serr == nil {
			return ErrRestoreTaken
		}
		// Links not supported there: copy.
	}
	return c.restoreCopy(bp, p, fi)
}

// noHardlink, for tests, acts as if the server lacked
// hardlink@openssh.com.
var noHardlink bool

// restoreCopy copies the backup bp to p, created only if missing, then
// removes the backup.
func (c *Client) restoreCopy(bp, p string, fi fs.FileInfo) error {
	src, err := c.c.Open(bp)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := c.c.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		if _, serr := c.c.Lstat(p); serr == nil {
			return ErrRestoreTaken
		}
		return err
	}
	_, err = io.Copy(dst, src)
	if err == nil {
		err = dst.Chmod(fi.Mode() & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky))
	}
	if err == nil {
		c.keepOwner(dst, fi) // as a save does; the copy is there either way
	}
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		c.c.Remove(p) // ours: created just now
		return err
	}
	c.c.Chtimes(p, fi.ModTime(), fi.ModTime())
	return c.c.Remove(bp)
}

// Discard removes the old version of an unfinished save.
func (c *Client) Discard(dir string, u Unfinished) error {
	bp, _, err := backupOf(dir, u)
	if err != nil {
		return err
	}
	return c.c.Remove(bp)
}

// cleanBeforeSave, before a save of p, puts back p's old file a cut
// save left at its backup, or removes the backup when p is there. Only
// a save without posix-rename makes backups, so only then does it look,
// and only up the backup's name: the folder is not listed (it may be
// huge, and saves are frequent).
func (c *Client) cleanBeforeSave(p string) {
	if c.posixRename() {
		return
	}
	bk := backupName(p)
	if fi, err := c.c.Lstat(bk); err != nil || !fi.Mode().IsRegular() {
		return
	}
	switch _, err := c.c.Lstat(p); {
	case err == nil:
		c.c.Remove(bk)
	case errors.Is(err, fs.ErrNotExist):
		c.c.Rename(bk, p)
	}
}

// maxLeftovers is the most leftovers a listing looks at once (the rest
// the next time).
const maxLeftovers = 1000
