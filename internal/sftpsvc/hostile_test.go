package sftpsvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sshtest"
)

// connectFake connects to a server that lists what ffs says.
func connectFake(t testing.TB, ffs *sshtest.FakeFS) *Client {
	t.Helper()
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	srv.SFTPHandlers, srv.SFTPConn = ffs.Handlers, ffs.Conn
	t.Cleanup(srv.Close)
	kh, _ := session.OpenKnownHosts(filepath.Join(t.TempDir(), "kh"))
	kh.Add(srv.Addr, srv.HostKey.PublicKey())
	conn, err := session.Dial(context.Background(), session.Config{Addr: srv.Addr, User: "demo", HostKey: kh.Callback(),
		Credentials: func(context.Context) (session.Credentials, error) {
			return session.Credentials{Password: "secret"}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c, err := New(conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestSafeName(t *testing.T) {
	for _, n := range []string{"a.txt", "report.tar.gz", ".bashrc", "CONSOLE.txt", "com10", "lpt0.txt", "a b", "日本語.md", "con-fig", "auxiliary"} {
		if err := SafeName(n); err != nil {
			t.Errorf("%q: %v", n, err)
		}
	}
	for _, n := range []string{"", ".", "..", "a/b", `a\b`, `..\..\win.txt`, "C:evil", "a\x00b", "a\nb",
		"CON", "con", "Con.txt", "nul.tar.gz", "AUX", "prn.", "COM1", "com9.log", "LPT1", "lpt9.x", "CON .txt",
		"COM¹", "x.", "x ", "x. ."} {
		if err := SafeName(n); !errors.Is(err, ErrUnsafeName) {
			t.Errorf("%q: %v, want ErrUnsafeName", n, err)
		}
	}
}

func TestLocalPath(t *testing.T) {
	d := t.TempDir()
	if p, err := LocalPath(d, "a/b.txt"); err != nil || p != filepath.Join(d, "a", "b.txt") {
		t.Fatalf("%q %v", p, err)
	}
	for _, rel := range []string{"", "../x", "a/../../x", `..\x`, "a//b", "/etc/passwd"} {
		if p, err := LocalPath(d, rel); err == nil {
			t.Errorf("%q: %q, want an error", rel, p)
		}
	}
	if Within(d, filepath.Join(d, "..", "x")) || Within(d, filepath.Dir(d)) || !Within(d, d) || !Within(d, filepath.Join(d, "..x")) {
		t.Fatal("Within")
	}
}

func TestExecutable(t *testing.T) {
	for _, n := range []string{"a.exe", "A.EXE", "run.Bat", "x.lnk", "s.ps1", "t.sh", "u.js", "v.msi", "w.exe. ", "x.desktop", "y.app", "z.command",
		"h.chm", "i.msix", "i.appx", "i.appinstaller", "i.application", "j.jnlp", "d.iso", "d.img",
		"d.vhd", "d.VHDX", "l.webloc", "l.fileloc", "l.inetloc", "m.docm", "m.xlsm", "m.pptm",
		"s.ws", "s.wsf", "m.psd1", "t.mst", "x.xll", "d.diagcab"} {
		if !Executable(n) {
			t.Errorf("%q not executable", n)
		}
	}
	for _, n := range []string{"a.txt", "b.md", "exe", "c.json", "d.tar.gz", "Makefile", "e.docx", "e.xlsx", "e.pptx", "p.psd", "w.wsx", "i.png"} {
		if Executable(n) {
			t.Errorf("%q executable", n)
		}
	}
}

// hostile lists names that must not become local paths.
func hostile() *sshtest.FakeFS {
	return &sshtest.FakeFS{
		Dirs: map[string][]sshtest.FakeEntry{
			"/":     {{Name: "evil", Dir: true}},
			"/evil": {{Name: "ok.txt", Size: 2}, {Name: `..\..\win.txt`, Size: 3}},
			"/dev":  {{Name: "CON.txt", Size: 1}},
			"/col":  {{Name: "C:x", Size: 1}},
		},
		Files: map[string][]byte{"/evil/ok.txt": []byte("ok"), `/evil/..\..\win.txt`: []byte("pwn"), "/dev/CON.txt": []byte("c"), "/col/C:x": []byte("c")},
	}
}

// Names a server allows that are not safe here are left out of a plan,
// each named with why, and the rest still downloads.
func TestPlanDownloadSkipsUnsafeNames(t *testing.T) {
	c := connectFake(t, hostile())
	root := t.TempDir()
	local := filepath.Join(root, "in")
	os.Mkdir(local, 0o755)
	for _, tc := range []struct {
		src   string
		items []string
		skip  []string
	}{
		{`/evil/..\..\win.txt`, nil, []string{`..\..\win.txt`}},
		{"/evil", []string{"evil", "evil/ok.txt"}, []string{`evil/..\..\win.txt`}},
		{"/dev", []string{"dev"}, []string{"dev/CON.txt"}},
		{"/col", []string{"col"}, []string{"col/C:x"}},
		{"/dev/CON.txt", nil, []string{"CON.txt"}},
	} {
		items, skips, err := c.PlanDownload(context.Background(), []string{tc.src}, local)
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		if got := rels(items); fmt.Sprint(got) != fmt.Sprint(tc.items) {
			t.Errorf("%s: items %v, want %v", tc.src, got, tc.items)
		}
		var sk []string
		for _, s := range skips {
			if s.Reason != SkipName {
				t.Errorf("%s: %+v", tc.src, s)
			}
			sk = append(sk, s.Rel)
		}
		if fmt.Sprint(sk) != fmt.Sprint(tc.skip) {
			t.Errorf("%s: skipped %v, want %v", tc.src, sk, tc.skip)
		}
		for _, it := range items {
			if !Within(local, it.Dst) {
				t.Errorf("%s: %+v", tc.src, it)
			}
		}
	}
	// Planning makes no files.
	var found []string
	filepath.Walk(root, func(p string, _ os.FileInfo, _ error) error {
		found = append(found, p)
		return nil
	})
	if len(found) != 2 {
		t.Fatalf("files made: %v", found)
	}
}

// Names legal on Linux but not on Windows (or in Finder) are skipped,
// not the folder: aux.c, 10:30.log, "x.".
func TestPlanDownloadSkipsWindowsNames(t *testing.T) {
	c := connectFake(t, &sshtest.FakeFS{Dirs: map[string][]sshtest.FakeEntry{
		"/":         {{Name: "proj", Dir: true}},
		"/proj":     {{Name: "aux.c", Size: 1}, {Name: "10:30.log", Size: 1}, {Name: "x.", Size: 1}, {Name: "main.go", Size: 1}, {Name: "sub", Dir: true}},
		"/proj/sub": {{Name: "nul", Size: 1}, {Name: "ok", Size: 1}},
	}})
	items, skips, err := c.PlanDownload(context.Background(), []string{"/proj"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(rels(items)); got != "[proj proj/main.go proj/sub proj/sub/ok]" {
		t.Fatalf("items %s", got)
	}
	var sk []string
	for _, s := range skips {
		sk = append(sk, s.Rel+" "+s.Src)
	}
	if got := strings.Join(sk, ","); got != "proj/10:30.log /proj/10:30.log,proj/aux.c /proj/aux.c,proj/sub/nul /proj/sub/nul,proj/x. /proj/x." {
		t.Fatalf("skipped %s", got)
	}
}

func rels(items []Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Rel)
	}
	return out
}

func TestListHidesImpossibleNames(t *testing.T) {
	c := connectFake(t, &sshtest.FakeFS{Dirs: map[string][]sshtest.FakeEntry{
		"/":  {{Name: "d", Dir: true}},
		"/d": {{Name: "../", Dir: true}, {Name: "a/b"}, {Name: "fine"}, {Name: `back\slash`}},
	}})
	es, err := c.List("/d")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name)
	}
	// pkg/sftp keeps the last part of "a/b"; "../" becomes "..", hidden.
	if strings.Join(names, ",") != `b,back\slash,fine` {
		t.Fatalf("%v", names)
	}
}

// A server listing "../" as a file: the plan leaves it out (no file has
// that name), and a download queued to it anyway leaves no part file in
// the parent folder.
func TestDotDotFileLeavesNoPart(t *testing.T) {
	ffs := &sshtest.FakeFS{
		Dirs: map[string][]sshtest.FakeEntry{
			"/":  {{Name: "d", Dir: true}},
			"/d": {{Name: "../", Size: 4}, {Name: "a.txt", Size: 1}},
		},
		Files: map[string][]byte{"/": []byte("root"), "/d/a.txt": []byte("a")},
	}
	c := connectFake(t, ffs)
	parent := t.TempDir()
	local := filepath.Join(parent, "in")
	os.Mkdir(local, 0o755)
	if items, skips, err := c.PlanDownload(context.Background(), []string{"/d"}, local); err != nil || fmt.Sprint(rels(items)) != "[d d/a.txt]" || len(skips) != 0 {
		t.Fatalf("%+v %+v %v", items, skips, err)
	}
	ts := &Transfers{Client: func(context.Context) (*Client, error) { return c, nil }, Logf: func(string, ...any) {}}
	// What the old plan made of it: the file "/d/.." (a file at "/" on
	// this server) to the folder chosen, or to its parent.
	for _, dst := range []string{local, filepath.Join(local, "..")} {
		id := ts.Add(Download, "/d/..", dst, "..")
		tr := waitState(t, ts, id, Failed)
		if !errors.Is(tr.Err, ErrDstIsDir) || tr.Attempt != 0 {
			t.Fatalf("%+v", tr)
		}
	}
	id := ts.Add(Download, "/d/..", filepath.Join(local, "x", ".."), "..")
	if tr := waitState(t, ts, id, Failed); tr.Attempt != 0 {
		t.Fatalf("%+v", tr)
	}
	id = ts.Add(Download, "/d/a.txt", filepath.Join(local, "CON"), "CON")
	if tr := waitState(t, ts, id, Failed); !errors.Is(tr.Err, ErrUnsafeName) || tr.Attempt != 0 {
		t.Fatalf("%+v", tr)
	}
	if got := files(parent); len(got) != 0 {
		t.Fatalf("left: %v", got)
	}
}

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

// Folders without end: a folder that is itself, endless depth and
// endless width all stop with an error; a "../" folder is left out.
func TestPlanDownloadStops(t *testing.T) {
	plan := func(ffs *sshtest.FakeFS, src string) error {
		t.Helper()
		c := connectFake(t, ffs)
		done := make(chan error, 1)
		go func() {
			_, _, err := c.PlanDownload(context.Background(), []string{src}, t.TempDir())
			done <- err
		}()
		select {
		case err := <-done:
			return err
		case <-time.After(20 * time.Second):
			t.Fatal("plan does not stop")
			return nil
		}
	}
	// "../" listed as a folder.
	err := plan(&sshtest.FakeFS{Dirs: map[string][]sshtest.FakeEntry{
		"/":  {{Name: "d", Dir: true}},
		"/d": {{Name: "../", Dir: true}, {Name: "f"}},
	}}, "/d")
	if err != nil {
		t.Errorf("../ folder: %v", err)
	}
	// Every folder holds "sub", and is the same folder.
	sub := func(p string) ([]sshtest.FakeEntry, bool) {
		if p == "/loop" || strings.HasPrefix(p, "/loop/") {
			return []sshtest.FakeEntry{{Name: "sub", Dir: true}}, true
		}
		return nil, false
	}
	err = plan(&sshtest.FakeFS{List: sub, Real: func(string) string { return "/loop" }}, "/loop")
	if !errors.Is(err, ErrPlanLoop) {
		t.Errorf("loop: %v", err)
	}
	// ... each a different folder: too deep.
	err = plan(&sshtest.FakeFS{List: sub}, "/loop")
	if !errors.Is(err, ErrPlanTooDeep) {
		t.Errorf("deep: %v", err)
	}
	// Too many entries.
	old := PlanMaxEntries
	PlanMaxEntries = 50
	t.Cleanup(func() { PlanMaxEntries = old })
	var wide []sshtest.FakeEntry
	for i := range 60 {
		wide = append(wide, sshtest.FakeEntry{Name: fmt.Sprintf("f%d", i)})
	}
	err = plan(&sshtest.FakeFS{Dirs: map[string][]sshtest.FakeEntry{"/w": wide}}, "/w")
	if !errors.Is(err, ErrPlanTooLarge) {
		t.Errorf("wide: %v", err)
	}
}
