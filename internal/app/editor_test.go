package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/layout"
)

// editorOn opens the remote file name of the files pane fc in an editor,
// from the row's menu, and returns the editor pane.
func editorOn(t *testing.T, a *App, tt interface {
	RightClick(string) error
	ChooseMenuItem(...string) error
	Frame()
}, name string) {
	t.Helper()
	if err := tt.RightClick(name); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Edit"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}

func TestEditorPane(t *testing.T) {
	a, tt, fc, root := filesPane(t)
	file := filepath.Join(root, "notes.txt")
	write(t, file, "one\n")
	a.refreshSides(fc)
	waitFor(t, tt, "listing", func() bool { return tt.HasText("notes.txt") })
	editorOn(t, a, tt, "notes.txt")
	var ec *conn
	waitFor(t, tt, "editor", func() bool {
		ec = a.focused()
		return ec != nil && ec.kind == layout.Editor && ec.ed != nil
	})
	if len(a.ws.Tabs) != 2 || a.ws.ActiveTab().Count(layout.Editor) != 1 {
		t.Fatalf("editor not in a new tab: %v", a.ws.Items())
	}
	if ec.ed.st.Buffer.String() != "one\n" || !tt.HasText("notes.txt") {
		t.Fatalf("editor shows %q", ec.ed.st.Buffer.String())
	}
	if kindIcon(layout.Editor, true) == kindIcon(layout.Files, true) {
		t.Error("editor has the files icon")
	}
	waitFor(t, tt, "editor connected", func() bool { return ec.fs.remote.client != nil })

	// Edit and save: the file is what the editor holds.
	ec.ed.st.Buffer.Set("two\nlines\n")
	tt.Frame()
	if !tt.HasText("Edited") {
		t.Error("not marked edited")
	}
	mustClick(t, tt, "Save")
	waitFor(t, tt, "saved", func() bool { return read(file) == "two\nlines\n" && !ec.ed.saving })
	tt.Frame()
	if ec.ed.st.Buffer.Modified() || tt.HasText("Edited") {
		t.Error("still edited after saving")
	}

	// Changed on the server meanwhile: asks; reload takes the server's.
	write(t, file, "changed elsewhere\n")
	ec.ed.st.Buffer.Set("mine\n")
	tt.Frame()
	a.saveEditor(ec, false)
	waitFor(t, tt, "conflict", func() bool { return tt.HasText("Changed on the server") })
	mustClick(t, tt, "Reload")
	waitFor(t, tt, "reloaded", func() bool { return ec.ed.st.Buffer.String() == "changed elsewhere\n" && !ec.ed.loading })
	// or replace it.
	write(t, file, "changed again!\n")
	ec.ed.st.Buffer.Set("mine\n")
	a.saveEditor(ec, false)
	waitFor(t, tt, "conflict", func() bool { return tt.HasText("Changed on the server") })
	mustClick(t, tt, "Replace")
	waitFor(t, tt, "replaced", func() bool { return read(file) == "mine\n" })
	if read(file) != "mine\n" {
		t.Error("not replaced")
	}
}

func TestOpenConfirmations(t *testing.T) {
	a, tt, fc, root := filesPane(t)
	s := a.settings
	s.OpenConfirmMB = 1
	a.ApplySettings(s)
	write(t, filepath.Join(root, "big.log"), strings.Repeat("x", 2<<20))
	write(t, filepath.Join(root, "blob.bin"), "a\x00b")
	a.refreshSides(fc)
	waitFor(t, tt, "listing", func() bool { return tt.HasText("big.log") && tt.HasText("blob.bin") })

	editorOn(t, a, tt, "big.log")
	if !tt.HasText("Open a large file?") || !tt.HasText("2.0 MB") {
		t.Fatalf("no large file question in %q", tt.Texts())
	}
	mustClick(t, tt, "Cancel")
	if len(a.ws.Tabs) != 1 || tt.HasText("Open a large file?") {
		t.Fatal("opened after cancel")
	}
	editorOn(t, a, tt, "big.log")
	mustClick(t, tt, "Open")
	waitFor(t, tt, "large opened", func() bool { c := a.focused(); return c.kind == layout.Editor && c.ed.st.Buffer.Len() == 2<<20 })

	a.ws.Activate(fc.id)
	tt.Frame()
	editorOn(t, a, tt, "blob.bin")
	waitFor(t, tt, "binary question", func() bool { return tt.HasText("Open a binary file?") })
	mustClick(t, tt, "Open")
	waitFor(t, tt, "binary opened", func() bool { c := a.focused(); return c.kind == layout.Editor && c.ed.name == "blob.bin" })
}

func TestOpenWithDefaultApp(t *testing.T) {
	a, tt, fc, root := filesPane(t)
	old := watchEvery
	watchEvery = 20 * time.Millisecond
	t.Cleanup(func() { watchEvery = old })
	var mu sync.Mutex
	var opened string
	a.p.OpenPath = func(p string) error { mu.Lock(); opened = p; mu.Unlock(); return nil }
	a.p.CacheDir = filepath.Join(t.TempDir(), "cache")
	file := filepath.Join(root, "conf.ini")
	write(t, file, "a=1\n")
	a.refreshSides(fc)
	waitFor(t, tt, "listing", func() bool { return tt.HasText("conf.ini") })
	tt.RightClick("conf.ini")
	if err := tt.ChooseMenuItem("Open With Default App"); err != nil {
		t.Fatal(err)
	}
	local := ""
	waitFor(t, tt, "opened", func() bool { mu.Lock(); defer mu.Unlock(); local = opened; return local != "" })
	if pane := filepath.Base(filepath.Dir(filepath.Dir(local))); !strings.HasPrefix(pane, strconv.Itoa(os.Getpid())+"-") {
		t.Errorf("pane folder %q not named for the process", pane)
	}
	if !strings.HasPrefix(local, a.p.CacheDir) || filepath.Base(local) != "conf.ini" || read(local) != "a=1\n" {
		t.Fatalf("opened %q", local)
	}
	wantMode := os.FileMode(0o600)
	if runtime.GOOS == "windows" {
		// Chmod only toggles read-only, so a writable copy is 0666.
		wantMode = 0o666
	}
	if fi, _ := os.Stat(local); fi.Mode().Perm() != wantMode {
		t.Errorf("copy mode %v", fi.Mode())
	}
	// Saving it there uploads it. The new bytes show up before WriteFile
	// has read the version back, so wait for the upload to finish. Don't
	// hold the remote file open while waiting: Windows cannot rename
	// over a file this process has open.
	uploaded := func() bool { return tt.HasText("Uploaded conf.ini") }
	dropToasts := func() {
		t.Helper()
		tt.Advance(time.Minute)
		if tt.HasText("Uploaded conf.ini") {
			t.Fatal("uploaded toast still showing")
		}
	}
	if err := os.WriteFile(local, []byte("a=2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "auto upload", uploaded)
	if read(file) != "a=2\n" {
		t.Fatalf("uploaded %q", read(file))
	}

	// Changed on the server meanwhile: not uploaded over it; asks, as the
	// editor does. Reload puts the server's in the copy.
	theirs := func(s string) {
		write(t, file, s)
		later := time.Now().Add(time.Minute)
		os.Chtimes(file, later, later)
	}
	theirs("a=theirs\n")
	os.WriteFile(local, []byte("a=3\n"), 0o600)
	waitFor(t, tt, "conflict", func() bool { return tt.HasText("Changed on the server") })
	if read(file) != "a=theirs\n" {
		t.Fatalf("uploaded over the server's change: %q", read(file))
	}
	mustClick(t, tt, "Reload")
	waitFor(t, tt, "copy reloaded", func() bool { return read(local) == "a=theirs\n" })
	time.Sleep(5 * watchEvery)
	if read(file) != "a=theirs\n" || tt.HasText("Changed on the server") {
		t.Fatalf("after reload: server %q", read(file))
	}
	// or replace it.
	theirs("a=again\n")
	os.WriteFile(local, []byte("a=4\n"), 0o600)
	waitFor(t, tt, "conflict", func() bool { return tt.HasText("Changed on the server") })
	dropToasts()
	mustClick(t, tt, "Replace")
	waitFor(t, tt, "replaced", uploaded)
	if read(file) != "a=4\n" {
		t.Fatalf("replaced with %q", read(file))
	}
	// and later saves upload again.
	dropToasts()
	os.WriteFile(local, []byte("a=5\n"), 0o600)
	waitFor(t, tt, "auto upload", uploaded)
	if read(file) != "a=5\n" {
		t.Fatalf("uploaded %q", read(file))
	}
	// Closing the pane deletes the copy.
	a.CloseSession(fc.id)
	waitFor(t, tt, "copy deleted", func() bool { _, err := os.Stat(local); return os.IsNotExist(err) })
	es, _ := os.ReadDir(a.p.CacheDir)
	if len(es) != 0 {
		t.Errorf("left in the cache: %v", es)
	}
}

func TestCleanCache(t *testing.T) {
	root := t.TempDir()
	dead := exec.Command(os.Args[0], "-test.run=^$")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	mk := func(name string) string {
		d := filepath.Join(root, name)
		os.MkdirAll(d, 0o700)
		write(t, filepath.Join(d, "f.txt"), "copy")
		return d
	}
	gone := []string{
		mk(strconv.Itoa(dead.Process.Pid) + "-0123456789ab"),
		mk("abcdef012345"),
	}
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(gone[1], old, old)
	kept := []string{
		mk(strconv.Itoa(os.Getpid()) + "-0123456789ab"),
		mk(strconv.Itoa(os.Getppid()) + "-0123456789ab"), // alive
		mk("fedcba987654"), // recent, old style
		mk("notes"),
		mk("12-not-hex-at-all"),
	}
	cleanCache(root)
	for _, d := range gone {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("%s left", filepath.Base(d))
		}
	}
	for _, d := range kept {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s removed", filepath.Base(d))
		}
	}
}

// A copy that could not be written leaves no folder behind, and later
// copies get folders of their own.
func TestOpenWithDefaultAppAfterFailedCopy(t *testing.T) {
	a, tt, fc, root := filesPane(t)
	var mu sync.Mutex
	var opened []string
	a.p.OpenPath = func(p string) error { mu.Lock(); opened = append(opened, p); mu.Unlock(); return nil }
	a.p.CacheDir = filepath.Join(t.TempDir(), "cache")
	write(t, filepath.Join(root, "a.txt"), "A")
	write(t, filepath.Join(root, "b.txt"), "B")
	var failed atomic.Bool
	old := writeLocalCopy
	writeLocalCopy = func(name string, data []byte, perm os.FileMode) error {
		if failed.CompareAndSwap(false, true) {
			os.WriteFile(name, data[:0], perm) // a partial copy
			return errors.New("disk full")
		}
		return old(name, data, perm)
	}
	t.Cleanup(func() { writeLocalCopy = old })
	a.refreshSides(fc)
	waitFor(t, tt, "listing", func() bool { return tt.HasText("a.txt") && tt.HasText("b.txt") })
	open := func(name string, n int) string {
		t.Helper()
		tt.RightClick(name)
		if err := tt.ChooseMenuItem("Open With Default App"); err != nil {
			t.Fatal(err)
		}
		var got string
		waitFor(t, tt, "opened "+name, func() bool {
			mu.Lock()
			defer mu.Unlock()
			if len(opened) < n {
				return false
			}
			got = opened[n-1]
			return true
		})
		return got
	}
	tt.RightClick("a.txt")
	if err := tt.ChooseMenuItem("Open With Default App"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "failed copy", func() bool { return tt.HasText("disk full") })
	b := open("b.txt", 1)
	ac := open("a.txt", 2)
	if read(b) != "B" || filepath.Base(b) != "b.txt" || read(ac) != "A" || filepath.Base(ac) != "a.txt" {
		t.Fatalf("opened %q, %q", b, ac)
	}
	if filepath.Dir(b) == filepath.Dir(ac) {
		t.Fatalf("copies share a folder: %q", filepath.Dir(b))
	}
	subs, _ := os.ReadDir(filepath.Dir(filepath.Dir(b)))
	if len(subs) != 2 {
		t.Errorf("copy folders %v, want the two copies'", subs)
	}
	a.CloseSession(fc.id)
}
