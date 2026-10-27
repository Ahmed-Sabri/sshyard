//go:build !windows

package sftpsvc

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

func inode(t *testing.T, p string) uint64 {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Sys().(*syscall.Stat_t).Ino
}

// leftovers are the save's temporary files left in dir.
func leftovers(dir string) []string {
	var out []string
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		if strings.HasPrefix(e.Name(), ".sshyard-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestWriteFileReplacesAtomically(t *testing.T) {
	for _, posix := range []bool{true, false} {
		name := "posix-rename"
		if !posix {
			name = "rename aside"
		}
		t.Run(name, func(t *testing.T) {
			noPosixRename = !posix
			t.Cleanup(func() { noPosixRename = false })
			c, root := connect(t)
			file := filepath.Join(root, "secret.conf")
			os.WriteFile(file, []byte("old\n"), 0o600)
			os.Chmod(file, 0o640)
			was, _ := c.Version(file)
			ino := inode(t, file)

			// While the copy is written, the file is the old one and the
			// copy only the user's.
			saw := false
			beforeReplace = func(tmp, p string) error {
				saw = true
				if p != file || read(t, file) != "old\n" || read(t, tmp) != "new\n" {
					t.Errorf("before the rename: %q is %q, %q is %q", p, read(t, file), tmp, read(t, tmp))
				}
				return nil
			}
			t.Cleanup(func() { beforeReplace = nil })
			v, err := c.WriteFile(file, []byte("new\n"), was)
			if err != nil || !saw {
				t.Fatalf("save: %v (copy seen %v)", err, saw)
			}
			if read(t, file) != "new\n" || inode(t, file) == ino {
				t.Error("not replaced by a copy")
			}
			if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o640 {
				t.Errorf("mode %v, want 0640", fi.Mode())
			}
			if now, _ := c.Version(file); now.Size != v.Size || !now.ModTime.Equal(v.ModTime) || v.Sum == ([32]byte{}) {
				t.Errorf("version %v, now %v", v, now)
			}
			if l := leftovers(root); len(l) > 0 {
				t.Errorf("left %v", l)
			}

			// A save cut before the rename leaves the old file whole.
			beforeReplace = func(string, string) error { return errors.New("cut") }
			if _, err := c.WriteFile(file, []byte("half"), v); err == nil {
				t.Fatal("cut save succeeded")
			}
			if read(t, file) != "new\n" {
				t.Errorf("after a cut save: %q", read(t, file))
			}
			if l := leftovers(root); len(l) > 0 {
				t.Errorf("left %v", l)
			}
			beforeReplace = nil

			// A new file (no version) is created with the usual mode.
			fresh := filepath.Join(root, "fresh.txt")
			if _, err := c.WriteFile(fresh, []byte("x"), Version{}); err != nil || read(t, fresh) != "x" {
				t.Fatalf("new file: %v", err)
			}
			if fi, _ := os.Stat(fresh); fi.Mode().Perm() != 0o644 {
				t.Errorf("new file mode %v", fi.Mode())
			}
		})
	}
}

func TestWriteFileThroughLink(t *testing.T) {
	c, root := connect(t)
	target := filepath.Join(root, "real.txt")
	link := filepath.Join(root, "link.txt")
	os.WriteFile(target, []byte("old"), 0o644)
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	was, _ := c.Version(link)
	if _, err := c.WriteFile(link, []byte("new"), was); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced")
	}
	if read(t, target) != "new" {
		t.Errorf("target %q", read(t, target))
	}
}

func TestWriteFilePermissions(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes anything")
	}
	c, root := connect(t)
	// A read-only file is not replaced, though its folder is writable.
	ro := filepath.Join(root, "ro.txt")
	os.WriteFile(ro, []byte("keep"), 0o444)
	if _, err := c.WriteFile(ro, []byte("new"), Version{}); err == nil || read(t, ro) != "keep" {
		t.Errorf("read-only file written: %v %q", err, read(t, ro))
	}
	// In a folder that is not writable, a writable file is written in
	// place.
	dir := filepath.Join(root, "locked")
	os.Mkdir(dir, 0o755)
	file := filepath.Join(dir, "f.txt")
	os.WriteFile(file, []byte("old"), 0o600)
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	ino := inode(t, file)
	if _, err := c.WriteFile(file, []byte("new"), Version{}); err != nil {
		t.Fatal(err)
	}
	if read(t, file) != "new" || inode(t, file) != ino {
		t.Error("not written in place")
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, _ := os.ReadFile(p)
	return string(b)
}

// A writer that lands after the rename and before WriteFile stats the
// result must not become the version of what we saved. The stat is a
// different size, so the sum would stay zero, and the next save would
// match it and overwrite them.
func TestWriteFileDoesNotAdoptAConcurrentReplace(t *testing.T) {
	c, root := connect(t)
	file := filepath.Join(root, "conf.ini")
	if err := os.WriteFile(file, []byte("a=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, was, err := c.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	afterRename = func() {
		if err := os.WriteFile(file, []byte("a=theirs\n"), 0o644); err != nil {
			t.Error(err)
		}
		later := time.Now().Add(time.Minute)
		if err := os.Chtimes(file, later, later); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { afterRename = nil })
	if _, err := c.WriteFile(file, []byte("a=2\n"), was); !errors.Is(err, ErrChanged) {
		t.Fatalf("save adopted the concurrent replace: %v", err)
	}
	if got := read(t, file); got != "a=theirs\n" {
		t.Fatalf("server file %q", got)
	}
}

func TestWriteFileSeesSameSizeSameTimeEdit(t *testing.T) {
	c, root := connect(t)
	file := filepath.Join(root, "app.conf")
	os.WriteFile(file, []byte("port=8080\n"), 0o644)
	_, was, err := c.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// Someone edits it within the same second, same size: size and time
	// as before.
	os.WriteFile(file, []byte("port=9090\n"), 0o644)
	os.Chtimes(file, was.ModTime, was.ModTime)
	if now, _ := c.Version(file); now.Size != was.Size || !now.ModTime.Equal(was.ModTime) {
		t.Fatal("the edit shows in size or time: the test holds nothing")
	}
	if _, err := c.WriteFile(file, []byte("port=7070\n"), was); !errors.Is(err, ErrChanged) {
		t.Fatalf("saved over the edit: %v", err)
	}
	if read(t, file) != "port=9090\n" {
		t.Fatalf("file %q", read(t, file))
	}
	// Unchanged, it saves, and the version saved compares the same way.
	_, was, _ = c.ReadFile(file)
	v, err := c.WriteFile(file, []byte("port=7070\n"), was)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WriteFile(file, []byte("port=6060\n"), v); err != nil || read(t, file) != "port=6060\n" {
		t.Fatalf("second save: %v", err)
	}
	// A file over SumMax is compared by size and time only.
	big := filepath.Join(root, "big.bin")
	os.WriteFile(big, make([]byte, SumMax+1), 0o644)
	_, bv, _ := c.ReadFile(big)
	if bv.Sum != ([32]byte{}) {
		t.Error("a large file has a sum")
	}
}

// A save without posix-rename cut once the old file is aside leaves it
// at its backup: the next listing reports it, to restore (or discard);
// the next save of the file puts it back itself.
func TestCutSwapRecovers(t *testing.T) {
	noPosixRename = true
	t.Cleanup(func() { noPosixRename = false; afterAside = nil; noHardlink = false })
	c, root := connect(t)
	file := filepath.Join(root, "a.conf")
	os.WriteFile(file, []byte("old"), 0o640)
	// Dated long ago: the backup is dated when made, not by the file.
	long := time.Now().Add(-48 * time.Hour)
	os.Chtimes(file, long, long)
	was, _ := c.Version(file)
	bk := path.Base(backupName(file))
	cut := func() {
		t.Helper()
		afterAside = func(string, string) error { return errors.New("connection lost") }
		if _, err := c.WriteFile(file, []byte("new"), was); err == nil {
			t.Fatal("cut save succeeded")
		}
		afterAside = nil
		l := leftovers(root)
		if _, err := os.Stat(file); err == nil || len(l) != 1 || l[0] != bk || read(t, filepath.Join(root, l[0])) != "old" {
			t.Fatalf("after the cut: %v", l)
		}
	}
	listed := func() []Unfinished {
		t.Helper()
		es, un, err := c.ListUnfinished(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range es {
			if e.Name == "a.conf" {
				t.Fatal("a.conf listed: put back by the listing")
			}
		}
		if _, err := os.Stat(file); err == nil {
			t.Fatal("the listing put the file back")
		}
		return un
	}
	for _, how := range []string{"posix-rename", "hard link", "copy"} {
		t.Run(how, func(t *testing.T) {
			cut()
			un := listed()
			if want := []Unfinished{{"a.conf", bk}}; !reflect.DeepEqual(un, want) {
				t.Fatalf("unfinished %+v, want %+v", un, want)
			}
			noPosixRename, noHardlink = how != "posix-rename", how == "copy"
			err := c.Restore(root, un[0])
			noPosixRename, noHardlink = true, false
			if err != nil {
				t.Fatal(err)
			}
			if read(t, file) != "old" || len(leftovers(root)) != 0 {
				t.Fatalf("%q, left %v", read(t, file), leftovers(root))
			}
			if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o640 {
				t.Errorf("mode %v", fi.Mode())
			}
			was, _ = c.Version(file)
		})
	}
	t.Run("name taken", func(t *testing.T) {
		cut()
		un := listed()
		os.WriteFile(file, []byte("uploaded since"), 0o644)
		for _, how := range []string{"posix-rename", "hard link", "copy"} {
			noPosixRename, noHardlink = how != "posix-rename", how == "copy"
			err := c.Restore(root, un[0])
			noPosixRename, noHardlink = true, false
			if !errors.Is(err, ErrRestoreTaken) {
				t.Fatalf("%s: %v", how, err)
			}
		}
		if read(t, file) != "uploaded since" || read(t, filepath.Join(root, bk)) != "old" {
			t.Fatal("replaced")
		}
		// Uploaded again under the same name: the backup stays an hour.
		if _, un, _ := c.ListUnfinished(root); len(un) != 0 || read(t, filepath.Join(root, bk)) != "old" {
			t.Fatalf("unfinished %v, left %v", un, leftovers(root))
		}
		hour := time.Now().Add(-staleLeftover - time.Minute)
		os.Chtimes(filepath.Join(root, bk), hour, hour)
		c.List(root)
		if l := leftovers(root); len(l) != 0 {
			t.Fatalf("left %v", l)
		}
		os.WriteFile(file, []byte("old"), 0o640)
		was, _ = c.Version(file)
	})
	t.Run("discard", func(t *testing.T) {
		cut()
		if err := c.Discard(root, Unfinished{"a.conf", ".sshyard-old-000000000000-b.conf"}); err == nil {
			t.Fatal("discarded another file's backup")
		}
		if err := c.Discard(root, listed()[0]); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(file); err == nil || len(leftovers(root)) != 0 {
			t.Fatalf("left %v", leftovers(root))
		}
		os.WriteFile(file, []byte("old"), 0o640)
		was, _ = c.Version(file)
	})
	t.Run("next save", func(t *testing.T) {
		cut()
		// The next save puts the old file back, sees it unchanged, saves.
		if _, err := c.WriteFile(file, []byte("new"), was); err != nil {
			t.Fatal(err)
		}
		if read(t, file) != "new" || len(leftovers(root)) != 0 {
			t.Fatalf("%q, left %v", read(t, file), leftovers(root))
		}
	})
}

// Cut by a dropped connection, a save may leave its copy too: the
// listing still reports the missing file (a young copy no longer hides
// it), and removes nothing.
func TestCutSaveWithCopyReported(t *testing.T) {
	c, root := connect(t)
	bk := path.Base(backupName("/x/a.conf"))
	os.WriteFile(filepath.Join(root, bk), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(root, ".sshyard-0123456789ab-a.conf"), []byte("new"), 0o600)
	es, un, err := c.ListUnfinished(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Unfinished{{"a.conf", bk}}; !reflect.DeepEqual(un, want) {
		t.Fatalf("unfinished %+v", un)
	}
	for _, e := range es {
		if e.Name == "a.conf" {
			t.Fatal("put back")
		}
	}
	if l := leftovers(root); len(l) != 2 {
		t.Fatalf("left %v", l)
	}
}

// Only leftovers in this app's exact forms are cleaned up, a save's copy
// only once stale and its file there.
func TestCleanLeftovers(t *testing.T) {
	c, root := connect(t)
	write := func(name, s string, age time.Duration) {
		p := filepath.Join(root, name)
		os.WriteFile(p, []byte(s), 0o600)
		old := time.Now().Add(-age)
		os.Chtimes(p, old, old)
	}
	write("a.conf", "a", 0)
	write("b.conf", "new b", 0)
	write(".sshyard-0123456789ab-a.conf", "stale copy", 2*time.Hour)      // removed
	write(".sshyard-0123456789ac-a.conf", "fresh copy", time.Minute)      // another save's: kept
	write(".sshyard-0123456789ad-gone.txt", "maybe the old", 2*time.Hour) // file missing: kept
	write(".sshyard-0123-a.conf", "not ours", 2*time.Hour)
	// Uppercase hex is not ours. The suffix differs too: on a
	// case-insensitive volume AB-a.conf would be the same file as ab-a.conf;
	// b.conf is there, so lowercase hex here would be removed.
	write(".sshyard-0123456789AB-b.conf", "not ours", 2*time.Hour)
	write(".sshyard-old-0123456789ab-b.conf", "old b", 2*time.Hour)   // file there: removed
	write(".sshyard-old-0123456789ad-b.conf", "young b", time.Minute) // an hour young: kept
	write(".sshyard-old-0123456789ab-c.conf", "old c", 48*time.Hour)  // missing: reported
	write(".sshyard-old-0123456789ac-c.conf", "newer old c", 24*time.Hour)
	os.Mkdir(filepath.Join(root, ".sshyard-0123456789ae-a.conf"), 0o700) // a folder: kept
	_, un, err := c.ListUnfinished(root)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(leftovers(root), ",")
	want := ".sshyard-0123-a.conf,.sshyard-0123456789AB-b.conf,.sshyard-0123456789ac-a.conf,.sshyard-0123456789ad-gone.txt,.sshyard-0123456789ae-a.conf,.sshyard-old-0123456789ab-c.conf,.sshyard-old-0123456789ac-c.conf,.sshyard-old-0123456789ad-b.conf"
	if got != want {
		t.Fatalf("left %s\nwant %s", got, want)
	}
	if want := []Unfinished{{"c.conf", ".sshyard-old-0123456789ac-c.conf"}}; !reflect.DeepEqual(un, want) {
		t.Fatalf("unfinished %+v", un)
	}
	if _, err := os.Stat(filepath.Join(root, "c.conf")); err == nil || read(t, filepath.Join(root, "b.conf")) != "new b" {
		t.Fatal("a file put back")
	}
	// A save looks only at its own file's backup (and only without
	// posix-rename): it puts that back first; the others are left to the
	// next listing.
	noPosixRename = true
	t.Cleanup(func() { noPosixRename = false })
	write(".sshyard-0123456789af-a.conf", "stale copy", 2*time.Hour)
	write(".sshyard-old-0123456789af-d.conf", "old d", 2*time.Hour)
	write(path.Base(backupName("/x/e.conf")), "old e", 2*time.Hour)
	e := filepath.Join(root, "e.conf")
	if _, err := c.WriteFile(e, []byte("e2"), Version{}); err != nil {
		t.Fatal(err)
	}
	want += ",.sshyard-0123456789af-a.conf,.sshyard-old-0123456789af-d.conf"
	l := leftovers(root)
	sort.Strings(l)
	w := strings.Split(want, ",")
	sort.Strings(w)
	if got, want := strings.Join(l, ","), strings.Join(w, ","); got != want || read(t, e) != "e2" {
		t.Fatalf("after a save, left %s\nwant %s", got, want)
	}
}

// A name may hold a newline: its save's leftovers are known as such.
func TestLeftoversOfNewlineName(t *testing.T) {
	c, root := connect(t)
	bk := path.Base(backupName("/x/a\nb.conf"))
	os.WriteFile(filepath.Join(root, bk), []byte("old"), 0o644)
	_, un, err := c.ListUnfinished(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Unfinished{{"a\nb.conf", bk}}; !reflect.DeepEqual(un, want) {
		t.Fatalf("unfinished %+v", un)
	}
}
