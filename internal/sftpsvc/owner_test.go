package sftpsvc

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestParseIDs(t *testing.T) {
	got := parseIDs([]byte("# comment\nroot:x:0:0:root:/root:/bin/sh\r\nubuntu:x:1000:1000::/home/ubuntu:/bin/bash\nbad:x:nope:\nshort:x\n:x:5:\ndup:x:1000:\n"))
	if len(got) != 2 || got[0] != "root" || got[1000] != "ubuntu" {
		t.Errorf("parseIDs = %v", got)
	}
}

// The owner of a remote entry is listed with it, and the server's names
// for the IDs are read once.
func TestOwners(t *testing.T) {
	c, root := connect(t)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644)
	es, err := c.List(root)
	if err != nil || len(es) != 1 {
		t.Fatalf("list %v %v", es, err)
	}
	if e := es[0]; !e.IDs || e.UID != uint32(os.Getuid()) || e.GID != uint32(os.Getgid()) {
		t.Errorf("remote owner %+v", e)
	}
	local, err := ListLocal(root)
	if err != nil || len(local) != 1 || !local[0].IDs || local[0].UID != uint32(os.Getuid()) {
		t.Errorf("local owner %+v %v", local, err)
	}
	if n := LocalUser(uint32(os.Getuid())); n == "" {
		t.Error("no local user name")
	}
	users, _ := c.Names()
	if _, err := os.Stat("/etc/passwd"); err == nil && users[0] != "root" {
		t.Errorf("users %v", users)
	}
}

func TestLocalFileReadWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("one"), 0o640)
	link := filepath.Join(dir, "link.txt")
	os.Symlink(p, link)
	data, ver, err := ReadLocalFile(link, 10)
	if err != nil || string(data) != "one" || ver.Size != 3 {
		t.Fatalf("read %q %+v %v", data, ver, err)
	}
	if _, _, err := ReadLocalFile(p, 2); !errors.Is(err, ErrReadLimit) {
		t.Errorf("limit %v", err)
	}
	ver, err = WriteLocalFile(link, []byte("two"), ver)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the link was replaced")
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", fi.Mode())
	}
	if got, _ := os.ReadFile(p); string(got) != "two" {
		t.Errorf("saved %q", got)
	}
	// Changed meanwhile, with the same size and time: the sum tells.
	os.WriteFile(p, []byte("own"), 0o640)
	os.Chtimes(p, time.Time{}, ver.ModTime)
	if _, err := WriteLocalFile(p, []byte("three"), ver); !errors.Is(err, ErrChanged) {
		t.Errorf("changed: %v", err)
	}
	if _, err := WriteLocalFile(p, []byte("three"), Version{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != "three" {
		t.Errorf("forced %q", got)
	}
	if es, _ := os.ReadDir(dir); len(es) != 2 {
		t.Errorf("left behind %v", es)
	}
}
