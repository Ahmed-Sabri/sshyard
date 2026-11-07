package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/vt"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// readLog waits for the log at path to hold want, and returns it.
func readLog(t *testing.T, tt *ui.Tester, path, want string) string {
	t.Helper()
	var got string
	waitFor(t, tt, "the log to hold "+want, func() bool {
		b, _ := os.ReadFile(path)
		got = string(b)
		return strings.Contains(got, want)
	})
	return got
}

func TestSessionLogKeepsOutputFromStartToStop(t *testing.T) {
	srv, sh := startScript(t, "motd\r\n$ ")
	a, tt := newTestApp(t)
	if want := filepath.Join(a.localHome(), "SSHYard", "logs"); a.logDir() != want {
		t.Fatalf("default folder %q, want %q", a.logDir(), want)
	}
	dir := filepath.Join(t.TempDir(), "logs")
	if err := a.setLogDir(dir); err != nil {
		t.Fatal(err)
	}
	c := connected(t, a, tt, srv)
	waitFor(t, tt, "prompt", func() bool { return strings.Contains(c.s.Term.ScreenText(), "$") })

	if err := a.startLog(c); err != nil {
		t.Fatal(err)
	}
	path := c.log.Path()
	if filepath.Dir(path) != filepath.Join(dir, "box") {
		t.Fatalf("log at %q", path)
	}
	if n, failed := a.logCount(); n != 1 || failed {
		t.Fatalf("log count %d %v", n, failed)
	}
	// What is typed goes to the shell, not to the log: this one does not
	// echo it.
	c.tv.send([]byte("typed-secret"))
	sh.out <- "ls\r\nfile1  file2\r\n50%\r100%\r\n$ "
	waitFor(t, tt, "output", func() bool { return strings.Contains(c.s.Term.ScreenText(), "100%") })
	a.stopLog(c)
	if c.log != nil {
		t.Fatal("the log still runs")
	}
	got := readLog(t, tt, path, "<== ")
	head, body, _ := strings.Cut(got, "\n")
	if !strings.HasPrefix(head, "==> box · demo@") {
		t.Fatalf("header %q", head)
	}
	body = body[:strings.LastIndex(body, "<== ")]
	if want := "$ ls\nfile1  file2\n100%\n$\n"; body != want {
		t.Fatalf("log %q, want %q", body, want)
	}
	if strings.Contains(got, "motd") || strings.Contains(got, "typed-secret") {
		t.Fatalf("the log holds what came before it or what was typed: %q", got)
	}

	// Stopped, it takes nothing more.
	sh.out <- "after\r\n"
	waitFor(t, tt, "output", func() bool { return strings.Contains(c.s.Term.ScreenText(), "after") })
	if b, _ := os.ReadFile(path); string(b) != got {
		t.Fatalf("written after stopping: %q", b)
	}
}

func TestSessionLogEndsWithItsPane(t *testing.T) {
	srv, sh := startScript(t, "$ ")
	a, tt := newTestApp(t)
	a.setLogDir(filepath.Join(t.TempDir(), "logs"))
	c := connected(t, a, tt, srv)
	if err := a.startLog(c); err != nil {
		t.Fatal(err)
	}
	path := c.log.Path()
	sh.out <- "last words\r\n"
	waitFor(t, tt, "output", func() bool { return strings.Contains(c.s.Term.ScreenText(), "last words") })
	a.CloseSession(c.id)
	readLog(t, tt, path, "last words\n<== ")
	if n, _ := a.logCount(); n != 0 {
		t.Fatalf("%d logs after closing the pane", n)
	}
}

func TestSessionLogStartFailure(t *testing.T) {
	srv, _ := startScript(t, "$ ")
	a, tt := newTestApp(t)
	// The folder is a file: no log starts, and the terminal has no hook.
	file := filepath.Join(t.TempDir(), "not-a-folder")
	os.WriteFile(file, nil, 0o600)
	a.setLogDir(file)
	c := connected(t, a, tt, srv)
	if err := a.startLog(c); err == nil || c.log != nil {
		t.Fatalf("started in a file: %v", err)
	}
	if n, _ := a.logCount(); n != 0 {
		t.Fatal("a failed start counts as a log")
	}
}

func TestSaveBuffer(t *testing.T) {
	srv, _ := startScript(t, "line one\r\nline two\r\n$ ")
	a, tt := newTestApp(t)
	a.setLogDir(filepath.Join(t.TempDir(), "logs"))
	c := connected(t, a, tt, srv)
	waitFor(t, tt, "output", func() bool { return strings.Contains(c.s.Term.ScreenText(), "line two") })
	dest := filepath.Join(t.TempDir(), "out.txt")
	var asked string
	a.p.SaveFile = func(title, path string) (string, error) {
		asked = path
		return dest, nil
	}
	a.saveBuffer(c, locales.In("en"))
	waitFor(t, tt, "the saved output", func() bool {
		b, _ := os.ReadFile(dest)
		return len(b) > 0
	})
	b, _ := os.ReadFile(dest)
	if string(b) != "line one\nline two\n$\n" {
		t.Fatalf("saved %q", b)
	}
	if filepath.Dir(asked) != a.logDir() || !strings.HasPrefix(filepath.Base(asked), "box_") {
		t.Fatalf("suggested %q", asked)
	}
}

func TestBufferTextHasScrollback(t *testing.T) {
	term := vt.New(10, 2)
	term.WriteString("one\r\ntwo\r\nthree\r\nfour")
	if got := bufferText(term); got != "one\ntwo\nthree\nfour\n" {
		t.Fatalf("buffer %q", got)
	}
}
