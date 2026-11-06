package session

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
)

// fakeTmux puts a tmux on PATH that answers as script says, beside the
// tools the scripts use, and returns the environment to run under.
func fakeTmux(t *testing.T, script string) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"grep", "tr"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(p, filepath.Join(dir, tool)); err != nil {
			t.Fatal(err)
		}
	}
	return []string{"PATH=" + dir, "HOME=/home/u"}
}

func runScript(t *testing.T, env []string, script string, args ...string) string {
	t.Helper()
	cmd := exec.Command("sh", append([]string{"-c", script, "_"}, args...)...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return string(out)
}

// TestTmuxListScript runs the list script on a fake tmux and reads the
// sessions, their windows and clients.
func TestTmuxListScript(t *testing.T) {
	env := fakeTmux(t, `case "$1$2" in
-V) echo "tmux 3.4";;
-ulist-sessions) printf '@S $0\t2\t1\t1700000000\t1700000300\t/srv/api\tdev box\n@S $4\t1\t0\t1700000000\t1700000000\t\twith\ttab\n';;
-ulist-windows) printf '@W $0\t0\t0\t1\tzsh\n@W $0\t1\t1\t2\tvim main.go\n@W $4\t0\t1\t1\tsh\n';;
-ulist-clients) printf '4242\t/dev/pts/3\t$0\n';;
esac`)
	ti := ParseTmuxList([]byte(runScript(t, env, tmuxListScript, "")))
	if ti.Version != "tmux 3.4" || len(ti.Sessions) != 2 || len(ti.Clients) != 1 {
		t.Fatalf("%+v", ti)
	}
	s := ti.Sessions[0]
	if s.ID != "$0" || s.Name != "dev box" || s.Windows != 2 || s.Attached != 1 || s.Path != "/srv/api" ||
		!s.Activity.Equal(time.Unix(1700000300, 0)) || len(s.Wins) != 2 {
		t.Errorf("dev %+v", s)
	}
	if w := s.Wins[1]; w.Index != 1 || w.Name != "vim main.go" || !w.Active || w.Panes != 2 {
		t.Errorf("window %+v", w)
	}
	if n := ti.Sessions[1].Name; n != "with\ttab" {
		t.Errorf("a name with a tab read %q", n)
	}
	if c := ti.Clients[0]; c.TTY != "/dev/pts/3" || c.Session != "$0" || c.Mine {
		t.Errorf("client %+v", c)
	}
}

// TestTmuxListNoServer: without a tmux server the list has the version
// and nothing else, which is not a failure.
func TestTmuxListNoServer(t *testing.T) {
	env := fakeTmux(t, `[ "$1" = -V ] && { echo "tmux 3.2a"; exit 0; }
echo "no server running on /tmp/tmux-1000/default" >&2; exit 1`)
	ti := ParseTmuxList([]byte(runScript(t, env, tmuxListScript, "abc")))
	if ti.Version != "tmux 3.2a" || len(ti.Sessions) != 0 || len(ti.Clients) != 0 {
		t.Fatalf("%+v", ti)
	}
}

// TestParseTmuxList: the terminal's own client is found, and lines that
// are not right are skipped.
func TestParseTmuxList(t *testing.T) {
	ti := ParseTmuxList([]byte("@V tmux 3.3a\n" +
		"@S $1\t1\t2\t0\t0\t/\tweb\n" +
		"@S nope\t1\t1\t0\t0\t/\tbad\n" +
		"@S $2\t1\n" +
		"@W $9\t0\t1\t1\torphan\n" +
		"@W $1\tx\t1\t1\tbad index\n" +
		"@C /dev/pts/1\t$1\t0\n" +
		"@C /dev/pts/2\t$1\t1\n" +
		"@C ;rm\t$1\t1\n"))
	if len(ti.Sessions) != 1 || ti.Sessions[0].Name != "web" || len(ti.Sessions[0].Wins) != 0 || len(ti.Clients) != 2 {
		t.Fatalf("%+v", ti)
	}
	if c, ok := ti.Mine(); !ok || c.TTY != "/dev/pts/2" {
		t.Errorf("mine %+v %v", c, ok)
	}
	if !ti.Sessions[0].Created.IsZero() {
		t.Errorf("a zero time read %v", ti.Sessions[0].Created)
	}
	if _, ok := ti.Session("$1"); !ok {
		t.Error("session $1 not found")
	}
}

func TestTmuxNameOK(t *testing.T) {
	for _, s := range []string{"main", "dev box", "工作", "a-b_c", "it's"} {
		if !TmuxNameOK(s) {
			t.Errorf("%q refused", s)
		}
	}
	for _, s := range []string{"", " lead", "trail ", "-opt", "a:b", "a.b", "tab\there", strings.Repeat("x", 65)} {
		if TmuxNameOK(s) {
			t.Errorf("%q taken", s)
		}
	}
}

// TestTmuxNewScript: a new session starts detached under its name, in
// the folder asked for, "~" read as the home folder.
func TestTmuxNewScript(t *testing.T) {
	env := fakeTmux(t, `printf '%s|' "$@" >&2; echo '$7'`)
	for dir, want := range map[string]string{"": "/home/u", "~": "/home/u", "~/w x": "/home/u/w x", "/srv": "/srv"} {
		out := runScript(t, env, tmuxNewScript, "it's", dir)
		if !strings.Contains(out, "-s|it's|-c|"+want+"|") || !strings.HasSuffix(out, "$7\n") {
			t.Errorf("dir %q: %q", dir, out)
		}
	}
	// Through sh -c, quoted as runTmux quotes it.
	cmd := exec.Command("sh", "-c", "/bin/sh -c "+shellQuote(tmuxNewScript)+" _ "+shellQuote("it's")+" "+shellQuote("/a'b"))
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "-s|it's|-c|/a'b|") {
		t.Errorf("quoted: %v %q", err, out)
	}
}

func TestTmuxAttachCommand(t *testing.T) {
	if got := TmuxAttachCommand("$12"); got != "tmux attach-session -t '$12'" {
		t.Errorf("got %q", got)
	}
	if got := TmuxAttachCommand("$1; rm -rf ~"); got != "" {
		t.Errorf("a bad ID gave %q", got)
	}
}

// TestTmuxIdleScript: a shell waiting at its prompt is idle, one running
// a program in the foreground is busy. It reads Linux's /proc.
func TestTmuxIdleScript(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc")
	}
	idle := func(shell, want string) string {
		tag := newTag()
		cmd := exec.Command("sh", "-c", shell)
		cmd.Env = append(os.Environ(), TagEnv+"="+tag)
		f, err := pty.Start(cmd)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait(); f.Close() })
		var out []byte
		for range 50 {
			out, _ = exec.Command("sh", "-c", tmuxIdleScript, "_", tag).Output()
			if strings.TrimSpace(string(out)) == want {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		return strings.TrimSpace(string(out))
	}
	if got := idle("read x", "idle"); got != "idle" {
		t.Errorf("a shell reading its terminal is %q", got)
	}
	if got := idle("set -m; sleep 30; :", "busy"); got != "busy" {
		t.Errorf("a shell running sleep is %q", got)
	}
}

// TestTmuxReal drives a real tmux, on a tmux server of its own, through an
// SSH server whose exec runs sh: a session is created, listed with its
// window and folder, renamed, refused a taken name, and ended.
func TestTmuxReal(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	// A short folder: tmux's socket path must stay under 104 bytes.
	tmp, err := os.MkdirTemp("", "tmux")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	env := append(os.Environ(), "TMUX_TMPDIR="+tmp, "TMUX=")
	t.Cleanup(func() {
		k := exec.Command("tmux", "kill-server")
		k.Env = env
		k.Run()
	})
	srv := server(t)
	srv.Exec = func(ch ssh.Channel, cmd string) int {
		c := exec.Command("sh", "-c", cmd)
		c.Env, c.Stdout, c.Stderr = env, ch, ch.Stderr()
		var exit *exec.ExitError
		if err := c.Run(); errors.As(err, &exit) {
			return exit.ExitCode()
		} else if err != nil {
			return 1
		}
		return 0
	}
	client := dialClient(t, srv)
	ctx := context.Background()

	ti, err := TmuxList(ctx, client, "")
	if err != nil || !strings.HasPrefix(ti.Version, "tmux ") || len(ti.Sessions) != 0 {
		t.Fatalf("no server: %+v %v", ti, err)
	}
	id, err := TmuxNew(ctx, client, "dev box", tmp)
	if err != nil {
		t.Fatal(err)
	}
	ti, err = TmuxList(ctx, client, "")
	if err != nil || len(ti.Sessions) != 1 {
		t.Fatalf("after new: %+v %v", ti, err)
	}
	s := ti.Sessions[0]
	if s.ID != id || s.Name != "dev box" || s.Windows != 1 || len(s.Wins) != 1 || s.Attached != 0 ||
		filepath.Base(s.Path) != filepath.Base(tmp) || s.Created.IsZero() {
		t.Errorf("session %+v", s)
	}
	if err := TmuxRename(ctx, client, id, "api"); err != nil {
		t.Fatal(err)
	}
	if _, err := TmuxNew(ctx, client, "api", ""); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("a taken name: %v", err)
	}
	if err := TmuxSelectWindow(ctx, client, id, s.Wins[0].Index); err != nil {
		t.Error(err)
	}
	if ti, _ = TmuxList(ctx, client, ""); len(ti.Sessions) != 1 || ti.Sessions[0].Name != "api" {
		t.Errorf("after rename: %+v", ti)
	}
	if err := TmuxKill(ctx, client, id); err != nil {
		t.Fatal(err)
	}
	if ti, err = TmuxList(ctx, client, ""); err != nil || len(ti.Sessions) != 0 {
		t.Errorf("after kill: %+v %v", ti, err)
	}
}
