package session

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// TestCwdScript runs the script on a /proc of its own: the shell (whose
// parent, sshd, is untagged) is found, not the program it runs.
func TestCwdScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh")
	}
	if err := exec.Command("sh", "-c", `printf 'a\0b\0' | grep -qzx b`).Run(); err != nil {
		t.Skip("this grep has no -z")
	}
	root := t.TempDir()
	tag := "0123abcd"
	proc := func(pid, ppid, comm, env, cwd string) {
		d := filepath.Join(root, pid)
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "environ"), []byte(env), 0o644)
		os.WriteFile(filepath.Join(d, "stat"), []byte(pid+" ("+comm+") S "+ppid+" 1 1 0"), 0o644)
		os.Symlink(cwd, filepath.Join(d, "cwd"))
	}
	tagged := "HOME=/root\x00" + TagEnv + "=" + tag + "\x00TERM=xterm\x00"
	proc("100", "1", "sshd: root@pts/0", "HOME=/root\x00", "/")
	proc("101", "100", "bash", tagged, "/srv/app")
	proc("102", "101", "vim) x (y", tagged, "/tmp")
	proc("200", "100", "bash", "HOME=/root\x00"+TagEnv+"=other\x00", "/home")
	script := strings.Replace(cwdScript(tag), "p=/proc", "p="+root, 1)
	out, err := exec.Command("sh", "-c", script).Output()
	if err != nil || strings.TrimSpace(string(out)) != "/srv/app" {
		t.Fatalf("%q %v", out, err)
	}
	script = strings.Replace(cwdScript("nope"), "p=/proc", "p="+root, 1)
	if out, err := exec.Command("sh", "-c", script).Output(); err == nil {
		t.Fatalf("an unknown tag: %q", out)
	}
}

// TestCwdScriptUntagged runs the script for a shell the server did not
// tag on a /proc of its own: the one process on a terminal that the sshd
// running the script started is the shell, not the sftp server beside
// it, the program the shell runs or the script's own login shell; with
// two such, it cannot tell.
func TestCwdScriptUntagged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh")
	}
	root := t.TempDir()
	proc := func(pid, ppid, comm, stdin, cwd string) {
		d := filepath.Join(root, pid)
		os.MkdirAll(filepath.Join(d, "fd"), 0o755)
		os.WriteFile(filepath.Join(d, "comm"), []byte(comm+"\n"), 0o644)
		os.WriteFile(filepath.Join(d, "stat"), []byte(pid+" ("+comm+") S "+ppid+" 1 1 0"), 0o644)
		os.Symlink(stdin, filepath.Join(d, "fd", "0"))
		os.Symlink(cwd, filepath.Join(d, "cwd"))
	}
	proc("90", "1", "sshd", "/dev/null", "/")
	proc("100", "90", "sshd", "/dev/null", "/")
	proc("101", "100", "bash", "/dev/pts/0", "/srv/app")
	proc("102", "101", "vim", "/dev/pts/0", "/tmp")
	proc("103", "100", "sftp-server", "pipe:[7]", "/root")
	proc("300", "100", "bash", "pipe:[8]", "/root")
	proc("301", "300", "sh", "pipe:[8]", "/root")
	run := func() (string, error) {
		script := strings.Replace(cwdScript(""), "p=/proc", "p="+root, 1)
		script = strings.Replace(script, "m=$$", "m=301", 1)
		out, err := exec.Command("sh", "-c", script).Output()
		return strings.TrimSpace(string(out)), err
	}
	if d, err := run(); err != nil || d != "/srv/app" {
		t.Fatalf("%q %v", d, err)
	}
	proc("104", "100", "zsh", "/dev/pts/1", "/home")
	if d, err := run(); err == nil {
		t.Fatalf("two shells: %q", d)
	}
}

// TestShellCwd: the shell is tagged, and ShellCwd asks the server for
// that tag's directory; a server that finds none is ErrNoCwd.
func TestShellCwd(t *testing.T) {
	srv := server(t)
	var asked string
	srv.Exec = func(ch ssh.Channel, cmd string) int {
		asked = cmd
		if strings.Contains(cmd, "tagged-elsewhere") {
			return 1
		}
		io.WriteString(ch, "/srv/app/../www\n")
		return 0
	}
	s := New(Config{Addr: srv.Addr, User: "demo", HostKey: trusted(t, srv).Callback(), Credentials: password("secret")}, Options{}, 80, 24)
	defer s.Close()
	s.Start()
	waitState(t, s, Connected)
	tag, ok := s.Tag()
	if !ok || len(tag) != 16 {
		t.Fatalf("tag %q %v", tag, ok)
	}
	d, err := ShellCwd(context.Background(), s.Client(), tag)
	if err != nil || d != "/srv/www" || !strings.Contains(asked, TagEnv+"="+tag) {
		t.Fatalf("%q %v, asked %q", d, err, asked)
	}
	if _, err := ShellCwd(context.Background(), s.Client(), "tagged-elsewhere"); !errors.Is(err, ErrNoCwd) {
		t.Fatalf("no shell: %v", err)
	}
}
