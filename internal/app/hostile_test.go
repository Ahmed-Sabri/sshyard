package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/egoist/mygo/ui"
)

// hostilePane opens a files pane on a server listing what ffs says, its
// local side on an empty folder inside a sandbox folder.
func hostilePane(t *testing.T, ffs *sshtest.FakeFS) (*App, *ui.Tester, *conn, string, string) {
	t.Helper()
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	srv.SFTPHandlers, srv.SFTPConn = ffs.Handlers, ffs.Conn
	t.Cleanup(func() { srv.Close() })
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "box", "secret")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	a.selected = h.ID
	showLocal(a)
	tt.Frame()
	mustClick(t, tt, "Open Files")
	fc := a.focused()
	waitFor(t, tt, "files connected", func() bool { return fc.fs.remote.client != nil })
	sandbox := t.TempDir()
	local := filepath.Join(sandbox, "a", "b")
	os.MkdirAll(local, 0o755)
	a.listLocal(&fc.fs.local, local)
	a.refreshSides(fc)
	return a, tt, fc, sandbox, local
}

// files lists the files below dir.
func files(dir string) []string {
	var out []string
	filepath.Walk(dir, func(p string, fi os.FileInfo, _ error) error {
		if fi != nil && !fi.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

const evilName = `..\..\win.txt`

func evilFS() *sshtest.FakeFS {
	return &sshtest.FakeFS{
		Dirs: map[string][]sshtest.FakeEntry{
			"/":     {{Name: "evil", Dir: true}, {Name: evilName, Size: 3}, {Name: "CON.txt", Size: 3}, {Name: "run.exe", Size: 3}},
			"/evil": {{Name: "ok.txt", Size: 2}, {Name: evilName, Size: 3}},
		},
		Files: map[string][]byte{
			"/" + evilName: []byte("pwn"), "/CON.txt": []byte("con"), "/run.exe": []byte("MZ\x00"),
			"/evil/ok.txt": []byte("ok"), "/evil/" + evilName: []byte("pwn"),
		},
	}
}

// Names not safe here are left out of a download, named in a notice
// with why; the rest of a folder still downloads.
func TestHostileNamesDownload(t *testing.T) {
	a, tt, fc, sandbox, local := hostilePane(t, evilFS())
	waitFor(t, tt, "listing", func() bool { return tt.HasText("evil") && tt.HasText(evilName) })
	download := func(name string) {
		t.Helper()
		if err := tt.RightClick(name); err != nil {
			t.Fatal(err)
		}
		if err := chooseMenu(a, tt, "Download"); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{evilName, "CON.txt"} {
		fc.fs.skipped = nil
		download(name)
		waitFor(t, tt, "skipped "+name, func() bool {
			return tt.HasText("Left out of the download: 1") && tt.HasText("“"+name+unsafeNote)
		})
		if fc.fs.local.err != "" {
			t.Fatalf("error: %s", fc.fs.local.err)
		}
	}
	if n := len(fc.fs.transfers.List()); n != 0 {
		t.Fatalf("%d queued", n)
	}
	download("evil")
	waitFor(t, tt, "evil downloaded", func() bool { fc.fs.transfers.Wait(); return len(files(sandbox)) == 1 })
	waitFor(t, tt, "evil skipped", func() bool {
		return tt.HasText("“evil/" + evilName + unsafeNote)
	})
	if got := files(sandbox); got[0] != filepath.Join(local, "evil", "ok.txt") {
		t.Fatalf("written: %v", got)
	}
	mustClick(t, tt, "Dismiss")
	tt.Frame()
	if tt.HasText("Left out of the download") {
		t.Fatal("notice still shown")
	}
}

// A long list of skipped names shows the first few and how many more.
func TestDownloadSkippedNoticeMore(t *testing.T) {
	ffs := &sshtest.FakeFS{Dirs: map[string][]sshtest.FakeEntry{"/": {{Name: "d", Dir: true}}, "/d": nil}}
	for i := range 11 {
		ffs.Dirs["/d"] = append(ffs.Dirs["/d"], sshtest.FakeEntry{Name: fmt.Sprintf("%02d:00.log", i), Size: 1})
	}
	a, tt, fc, _, _ := hostilePane(t, ffs)
	waitFor(t, tt, "listing", func() bool { return tt.HasText("d") })
	if err := tt.RightClick("d"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(a, tt, "Download"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "notice", func() bool { return tt.HasText("Left out of the download: 11") })
	if !tt.HasText("“d/07:00.log"+unsafeNote) || tt.HasText("“d/08:00.log"+unsafeNote) ||
		!tt.HasText("…and 3 more") {
		t.Fatalf("%q", tt.Texts())
	}
	if len(fc.fs.skipped) != 11 {
		t.Fatalf("%+v", fc.fs.skipped)
	}
}

func TestHostileNamesOpenWithDefaultApp(t *testing.T) {
	a, tt, fc, _, _ := hostilePane(t, evilFS())
	var mu sync.Mutex
	var opened []string
	a.p.OpenPath = func(p string) error { mu.Lock(); opened = append(opened, p); mu.Unlock(); return nil }
	cache := filepath.Join(t.TempDir(), "cache")
	a.p.CacheDir = cache
	waitFor(t, tt, "listing", func() bool { return tt.HasText(evilName) })
	open := func(name string) {
		t.Helper()
		if err := tt.RightClick(name); err != nil {
			t.Fatal(err)
		}
		if err := chooseMenu(a, tt, "Open With Default App"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	for _, name := range []string{evilName, "CON.txt"} {
		open(name)
		waitFor(t, tt, "refused "+name, func() bool { return tt.HasText("not a safe local file name") })
	}
	// A program asks first; cancelling opens nothing.
	open("run.exe")
	if !tt.HasText("Open “run.exe”") || !tt.HasText("“run.exe” is a type of file that opening may run as a program on this computer, with your permissions. Only open it if you trust the server and the file. To read or change it safely, use Open as Text instead.") {
		t.Fatalf("no question in %q", tt.Texts())
	}
	mustClick(t, tt, "Cancel")
	tt.Frame()
	mu.Lock()
	n := len(opened)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("opened %v", opened)
	}
	for _, p := range files(filepath.Dir(cache)) {
		if !strings.HasPrefix(p, cache) {
			t.Errorf("written outside the cache: %s", p)
		}
	}
	// Confirmed, it opens.
	open("run.exe")
	mustClick(t, tt, "Open With Default App")
	waitFor(t, tt, "opened", func() bool { mu.Lock(); defer mu.Unlock(); return len(opened) == 1 })
	if filepath.Base(opened[0]) != "run.exe" || !strings.HasPrefix(opened[0], cache) {
		t.Fatalf("opened %v", opened)
	}
	a.CloseSession(fc.id)
}
