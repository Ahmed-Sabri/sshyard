package sftpsvc

import (
	"path"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/sshtest"
)

// A save does not list the file's folder, however big: with
// posix-rename it looks for no leftovers at all, without it only up its
// own file's backup by name.
func TestSaveDoesNotList(t *testing.T) {
	for _, posix := range []bool{true, false} {
		noPosixRename = !posix
		ffs := &sshtest.FakeFS{
			Many:  func(p string) (int, bool) { return 400_000, p == "/big" },
			Files: map[string][]byte{"/big/a.conf": []byte("old")},
		}
		c := connectFake(t, ffs)
		if _, err := c.WriteFile("/big/a.conf", []byte("new"), Version{}); err != nil {
			t.Fatal(err)
		}
		noPosixRename = false
		if n := ffs.Listed(); n != 0 || ffs.Served.Load() != 0 {
			t.Errorf("posix-rename %v: the save listed %d times (%d entries)", posix, n, ffs.Served.Load())
		}
		var looked []string
		for _, r := range ffs.Seen() {
			if strings.HasPrefix(path.Base(strings.Fields(r)[1]), ".sshyard-old-") && (strings.HasPrefix(r, "Lstat ") || strings.HasPrefix(r, "Stat ")) {
				looked = append(looked, r)
			}
		}
		if want := backupName("/big/a.conf"); posix && len(looked) != 0 || !posix && (len(looked) != 1 || strings.Fields(looked[0])[1] != want) {
			t.Errorf("posix-rename %v: looked up %v", posix, looked)
		}
		if got := string(ffs.File("/big/a.conf")); got != "new" {
			t.Errorf("posix-rename %v: saved %q", posix, got)
		}
	}
	t.Cleanup(func() { noPosixRename = false })
}

func TestBackupName(t *testing.T) {
	a, b := backupName("/d/a.conf"), backupName("/e/a.conf")
	if path.Dir(a) != "/d" || path.Base(a) != path.Base(b) || !backupRe.MatchString(path.Base(a)) || backupRe.FindStringSubmatch(path.Base(a))[1] != "a.conf" {
		t.Fatalf("%q, %q", a, b)
	}
	if path.Base(backupName("/d/b.conf")) == path.Base(a) {
		t.Fatal("same key for two names")
	}
}

func BenchmarkSaveHugeFolder(b *testing.B) {
	noPosixRename = true
	defer func() { noPosixRename = false }()
	ffs := &sshtest.FakeFS{
		Many:  func(p string) (int, bool) { return 400_000, p == "/big" },
		Files: map[string][]byte{"/big/a.conf": []byte("old")},
	}
	c := connectFake(b, ffs)
	for b.Loop() {
		if _, err := c.WriteFile("/big/a.conf", []byte("new"), Version{}); err != nil {
			b.Fatal(err)
		}
	}
	if ffs.Listed() != 0 {
		b.Fatal("a save listed the folder")
	}
}
