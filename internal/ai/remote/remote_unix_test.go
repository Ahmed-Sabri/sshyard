//go:build !windows

package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sftpsvc"
	"github.com/Licoy/sshyard/internal/sshtest"
	"golang.org/x/crypto/ssh"
)

func dialBash(t *testing.T, execFn func(ssh.Channel, string) int) *ssh.Client {
	t.Helper()
	return dialHost(t, "", execFn)
}

func dialHost(t *testing.T, root string, execFn func(ssh.Channel, string) int) *ssh.Client {
	t.Helper()
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	if root != "" {
		srv.SFTPRoot = root
	}
	if execFn == nil {
		execFn = bashExec
	}
	srv.Exec = execFn
	kh, err := session.OpenKnownHosts(filepath.Join(t.TempDir(), "kh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := kh.Add(srv.Addr, srv.HostKey.PublicKey()); err != nil {
		t.Fatal(err)
	}
	c, err := session.Dial(context.Background(), session.Config{
		Addr: srv.Addr, User: "demo", HostKey: kh.Callback(),
		Credentials: func(context.Context) (session.Credentials, error) {
			return session.Credentials{Password: "secret"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// bashExec is a non-PTY exec handler: the channel stays open until the
// shell exits, and closing it does not kill the child.
func bashExec(ch ssh.Channel, line string) int {
	cmd := exec.Command("/bin/bash", "-c", line)
	cmd.Stdout = ch
	cmd.Stderr = ch.Stderr()
	in, err := cmd.StdinPipe()
	if err != nil {
		return 127
	}
	if err := cmd.Start(); err != nil {
		return 127
	}
	go func() { io.Copy(in, ch); in.Close() }()
	err = cmd.Wait()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 127
}

func waitPID(t *testing.T, file string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(file)
		if err == nil {
			pid, conv := strconv.Atoi(strings.TrimSpace(string(b)))
			if conv == nil && pid > 1 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pid file %s was not written", file)
	return 0
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil && syscall.Kill(-pid, 0) != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process group %d still running", pid)
}

func killGroup(pidfile string) {
	b, err := os.ReadFile(pidfile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

func TestRunEchoAndExit(t *testing.T) {
	c := dialBash(t, nil)
	r := New(c, Options{})
	var stdout, stderr bytes.Buffer
	var count CountingWriter
	count.W = &stdout
	exit, err := r.Run(context.Background(), "printf %s \"it's\"; echo; echo bad >&2", "", &count, &stderr)
	if err != nil || exit != 0 {
		t.Fatalf("exit %d err %v stderr %q", exit, err, stderr.String())
	}
	if stdout.String() != "it's\n" || stderr.String() != "bad\n" {
		t.Fatalf("stdout %q stderr %q", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "SSHYARD_PGID") || strings.Contains(stderr.String(), "SSHYARD_PID") {
		t.Fatalf("marker leaked: %q", stderr.String())
	}
	if count.Bytes() != int64(len("it's\n")) {
		t.Fatalf("bytes %d", count.Bytes())
	}

	exit, err = r.Run(context.Background(), "exit 3", "", nil, nil)
	if err != nil || exit != 3 {
		t.Fatalf("exit %d err %v", exit, err)
	}
}

func TestRunUsesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a b'c")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	c := dialBash(t, nil)
	r := New(c, Options{})
	var stdout, stderr bytes.Buffer
	exit, err := r.Run(context.Background(), "pwd", dir, &stdout, &stderr)
	if err != nil || exit != 0 {
		t.Fatalf("exit %d err %v stderr %q", exit, err, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != dir {
		t.Fatalf("pwd %q", stdout.String())
	}
}

func TestRunCapsConcurrentCommands(t *testing.T) {
	var cur, max, runs atomic.Int32
	c := dialBash(t, func(ch ssh.Channel, line string) int {
		if strings.Contains(line, "sleep 0.5") {
			n := cur.Add(1)
			for {
				old := max.Load()
				if n <= old || max.CompareAndSwap(old, n) {
					break
				}
			}
			defer cur.Add(-1)
			runs.Add(1)
		}
		return bashExec(ch, line)
	})
	a := New(c, Options{})
	b := New(c, Options{})
	start := time.Now()
	errc := make(chan error, 3)
	run := func(r *Runner) {
		_, err := r.Run(context.Background(), "sleep 0.5", "", nil, nil)
		errc <- err
	}
	go run(a)
	go run(a)
	go run(b)
	for range 3 {
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	if runs.Load() != 3 || max.Load() != MaxConcurrent {
		t.Fatalf("runs %d max %d", runs.Load(), max.Load())
	}
	if elapsed < 900*time.Millisecond {
		t.Fatalf("three sleeps overlapped: %s", elapsed)
	}
}

func TestRunCancelWhileWaiting(t *testing.T) {
	var runs atomic.Int32
	c := dialBash(t, func(ch ssh.Channel, line string) int {
		runs.Add(1)
		return bashExec(ch, line)
	})
	r := New(c, Options{})
	hold, stop := context.WithCancel(context.Background())
	defer stop()
	errc := make(chan error, 2)
	var files []string
	for range 2 {
		pidfile := filepath.Join(t.TempDir(), "pid")
		files = append(files, pidfile)
		t.Cleanup(func() { killGroup(pidfile) })
		go func() {
			_, err := r.Run(hold, "echo $$ > "+pidfile+"; sleep 30", "", nil, nil)
			errc <- err
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for runs.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("holders did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	short, cancelShort := context.WithCancel(context.Background())
	cancelShort()
	if _, err := r.Run(short, "echo should-not", "", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if runs.Load() != 2 {
		t.Fatalf("waiting command started: %d", runs.Load())
	}
	stop()
	for _, f := range files {
		for time.Now().Before(deadline) {
			if _, err := os.Stat(f); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		killGroup(f)
	}
	for range 2 {
		select {
		case <-errc:
		case <-time.After(3 * time.Second):
			t.Fatal("holder did not return")
		}
	}
}

func TestRunCancelKillsSleep(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	t.Cleanup(func() { killGroup(pidfile) })
	r := New(dialBash(t, nil), Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		exit, err := r.Run(ctx, "echo $$ > '"+pidfile+"'; sleep 600", "", nil, nil)
		if exit != -1 && err == nil {
			err = errors.New("exit 0")
		}
		done <- err
	}()
	pid := waitPID(t, pidfile)
	cancel()
	t0 := time.Now()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Run did not return")
	}
	if time.Since(t0) > 4*time.Second {
		t.Fatalf("TERM did not stop the command: %s", time.Since(t0))
	}
	waitGone(t, pid)
}

func TestRunKillsTermIgnoringCommand(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	t.Cleanup(func() { killGroup(pidfile) })
	r := New(dialBash(t, nil), Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := r.Run(ctx, "echo $$ > '"+pidfile+"'; trap '' TERM; sleep 600", "", nil, nil)
		done <- err
	}()
	pid := waitPID(t, pidfile)
	cancel()
	t0 := time.Now()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%v", err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("Run did not return")
	}
	if elapsed := time.Since(t0); elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Fatalf("ignored TERM should die on KILL after 5s, took %s", elapsed)
	}
	waitGone(t, pid)
}

func TestRunTurnTimeout(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	t.Cleanup(func() { killGroup(pidfile) })
	r := New(dialBash(t, nil), Options{TurnTimeout: 500 * time.Millisecond})
	r.BeginTurn()
	start := time.Now()
	exit, err := r.Run(context.Background(), "echo $$ > '"+pidfile+"'; sleep 600", "", nil, nil)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) || exit != -1 {
		t.Fatalf("exit %d err %v", exit, err)
	}
	if elapsed < 350*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("turn took %s", elapsed)
	}
	waitGone(t, waitPID(t, pidfile))
}

func TestRunCommandTimeout(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pid")
	t.Cleanup(func() { killGroup(pidfile) })
	r := New(dialBash(t, nil), Options{CommandTimeout: MinCommandTimeout})
	start := time.Now()
	exit, err := r.Run(context.Background(), "echo $$ > '"+pidfile+"'; sleep 600", "", nil, nil)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) || exit != -1 {
		t.Fatalf("exit %d err %v", exit, err)
	}
	if elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Fatalf("command timeout took %s", elapsed)
	}
	waitGone(t, waitPID(t, pidfile))
}

func TestRunExpiredTurnDoesNotStart(t *testing.T) {
	var runs atomic.Int32
	c := dialBash(t, func(ch ssh.Channel, line string) int {
		runs.Add(1)
		return bashExec(ch, line)
	})
	r := New(c, Options{TurnTimeout: 40 * time.Millisecond})
	r.BeginTurn()
	time.Sleep(80 * time.Millisecond)
	exit, err := r.Run(context.Background(), "echo started", "", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) || exit != -1 || runs.Load() != 0 {
		t.Fatalf("exit %d err %v runs %d", exit, err, runs.Load())
	}
}

func TestWriteRefusesWhenChanged(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f, err := OpenFiles(dialHost(t, root, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	path := filepath.Join(root, "note.txt")
	if _, err := f.Write(path, []byte("one"), sftpsvc.Version{}); err != nil {
		t.Fatal(err)
	}
	body, was, err := f.Read(path)
	if err != nil || string(body) != "one" || was.ModTime.IsZero() || was.Size != 3 {
		t.Fatalf("read %q %v size %d", body, err, was.Size)
	}
	if err := os.WriteFile(path, []byte("two-changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(path, []byte("three"), was); !errors.Is(err, sftpsvc.ErrChanged) {
		t.Fatalf("changed file: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "two-changed" {
		t.Fatalf("overwrote a changed file: %q", got)
	}

	// Same size and timestamp: the sum from Read still has to refuse.
	if err := os.WriteFile(path, []byte("two-changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, was, err = f.Read(path)
	if err != nil || string(body) != "two-changed" {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("edit-quiet!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, was.ModTime, was.ModTime); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(path, []byte("agent-save"), was); !errors.Is(err, sftpsvc.ErrChanged) {
		t.Fatalf("same size and time: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "edit-quiet!" {
		t.Fatalf("overwrote a quiet edit: %q", got)
	}

	_, was, err = f.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(path, []byte("three"), was); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "three" {
		t.Fatalf("fresh version did not save: %q", got)
	}
}

// shellBin is bash when the host has it, otherwise sh. Paths come from
// LookPath: macOS has no /usr/bin/setsid, and /bin/bash is not universal.
func shellBin(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"bash", "sh"} {
		p, err := exec.LookPath(name)
		if err == nil {
			return p
		}
	}
	t.Fatal("bash or sh not on PATH")
	return ""
}

// leaderCmd starts script as a session leader. OpenSSH's session child
// calls setsid(); the setsid(1) binary is not what makes the leader.
func leaderCmd(t *testing.T, script string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(shellBin(t), "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

func skipWithoutSetsid(t *testing.T, bins []string) {
	t.Helper()
	for _, name := range bins {
		if name != "setsid" {
			continue
		}
		if _, err := exec.LookPath("setsid"); err != nil {
			t.Skip("setsid not on PATH")
		}
		return
	}
}

// OpenSSH's session child is a process-group leader, and bash execs a
// simple command, so setsid is a leader there. Without --wait it forks
// and the parent exits 0 while the command keeps running.
func TestWrapWaitsUnderSessionLeader(t *testing.T) {
	script, err := wrap("sleep 1; exit 3", "")
	if err != nil {
		t.Fatal(err)
	}
	cmd := leaderCmd(t, script)
	start := time.Now()
	err = cmd.Run()
	elapsed := time.Since(start)
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Fatalf("err %v elapsed %s", err, elapsed)
	}
	if elapsed < 800*time.Millisecond {
		t.Fatalf("returned in %s without waiting", elapsed)
	}
}

// limitedPath is a directory of symlinks. The wrapped command must not see
// setsid or perl except when the case puts them here.
func limitedPath(t *testing.T, names []string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		src, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("lookup %s: %v", name, err)
		}
		if err := os.Symlink(src, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestWrapBranchExit(t *testing.T) {
	tests := []struct {
		name   string
		bins   []string
		marker string
		cmd    string
		out    string
	}{
		{"setsid", []string{"sh", "setsid", "true"}, "SSHYARD_PGID ", "printf %s hi; exit 3", "hi"},
		{"perl", []string{"sh", "perl"}, "SSHYARD_PGID ", "printf %s hi; exit 3", "hi"},
		{"pid", []string{"sh"}, "SSHYARD_PID ", "printf %s hi; exit 3", "hi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skipWithoutSetsid(t, tt.bins)
			script, err := wrap(tt.cmd, "")
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(shellBin(t), "-c", script)
			cmd.Env = []string{"PATH=" + limitedPath(t, tt.bins)}
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err = cmd.Run()
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != 3 {
				t.Fatalf("err %v stdout %q stderr %q", err, stdout.String(), stderr.String())
			}
			if stdout.String() != tt.out {
				t.Fatalf("stdout %q stderr %q", stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.marker) {
				t.Fatalf("stderr %q, want marker %s", stderr.String(), tt.marker)
			}
			other := "SSHYARD_PID "
			if tt.marker == other {
				other = "SSHYARD_PGID "
			}
			if strings.Contains(stderr.String(), other) {
				t.Fatalf("wrong marker in %q", stderr.String())
			}
		})
	}
}

// TestWrapBranchStop runs each wrapper branch under a session leader that
// also has a sibling sleep in the same group. Stop must kill the command
// (TERM, then KILL after 5s when TERM is ignored) and leave the sibling.
func TestWrapBranchStop(t *testing.T) {
	tests := []struct {
		name  string
		bins  []string
		group bool
		cmd   string
		min   time.Duration
		max   time.Duration
	}{
		{"setsid", []string{"sh", "setsid", "true", "sleep"}, true, "sleep 600", 0, 4 * time.Second},
		{"perl", []string{"sh", "perl", "sleep"}, true, "sleep 600", 0, 4 * time.Second},
		{"pid", []string{"sh", "sleep"}, false, "sleep 600", 0, 4 * time.Second},
		{"setsid-ignore", []string{"sh", "setsid", "true", "sleep"}, true, "trap '' TERM; sleep 600", 4 * time.Second, 15 * time.Second},
		{"perl-ignore", []string{"sh", "perl", "sleep"}, true, "trap '' TERM; sleep 600", 4 * time.Second, 15 * time.Second},
		{"pid-ignore", []string{"sh", "sleep"}, false, "trap '' TERM; sleep 600", 4 * time.Second, 15 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			skipWithoutSetsid(t, tt.bins)
			shell := shellBin(t)
			dir := t.TempDir()
			script, err := wrap(tt.cmd, "")
			if err != nil {
				t.Fatal(err)
			}
			wrapPath := filepath.Join(dir, "wrap.sh")
			if err := os.WriteFile(wrapPath, []byte(script+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			sibFile := filepath.Join(dir, "sib")
			errFile := filepath.Join(dir, "err")
			sleepBin, err := exec.LookPath("sleep")
			if err != nil {
				t.Fatal(err)
			}
			sup := leaderCmd(t, `"$SLEEP" 600 & echo $! > "$SIB"; PATH="$LIM" "$BASH" "$WRAP" >"$OUT" 2>"$ERR"`)
			sup.Env = []string{
				"PATH=" + os.Getenv("PATH"),
				"SLEEP=" + sleepBin,
				"BASH=" + shell,
				"SIB=" + sibFile,
				"LIM=" + limitedPath(t, tt.bins),
				"WRAP=" + wrapPath,
				"OUT=" + filepath.Join(dir, "out"),
				"ERR=" + errFile,
			}
			if err := sup.Start(); err != nil {
				t.Fatal(err)
			}
			var m mark
			var sib int
			t.Cleanup(func() {
				if sib > 1 {
					_ = syscall.Kill(sib, syscall.SIGKILL)
				}
				if m.id > 1 {
					if m.group {
						_ = syscall.Kill(-m.id, syscall.SIGKILL)
					} else {
						_ = syscall.Kill(m.id, syscall.SIGKILL)
						_ = exec.Command("pkill", "-KILL", "-P", strconv.Itoa(m.id)).Run()
					}
				}
				if sup.Process != nil {
					_ = sup.Process.Kill()
				}
			})
			waitDone := make(chan error, 1)
			go func() { waitDone <- sup.Wait() }()

			sib = waitPID(t, sibFile)
			deadline := time.Now().Add(3 * time.Second)
			var raw []byte
			for {
				raw, err = os.ReadFile(errFile)
				if err == nil && len(raw) > 0 {
					line, _, _ := strings.Cut(string(raw), "\n")
					if got, ok := parseMarker(line); ok {
						m = got
						break
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("no marker in %q (%v)", raw, err)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if m.group != tt.group {
				t.Fatalf("marker %+v, want group %v", m, tt.group)
			}

			k := exec.Command(shell, "-c", killScript(m))
			start := time.Now()
			out, err := k.CombinedOutput()
			elapsed := time.Since(start)
			if err != nil || strings.Contains(string(out), "STILL_ALIVE") {
				t.Fatalf("kill %v elapsed %s output %q", err, elapsed, out)
			}
			if elapsed < tt.min || elapsed > tt.max {
				t.Fatalf("kill took %s", elapsed)
			}
			if syscall.Kill(sib, 0) != nil {
				t.Fatalf("sibling %d died; stop signalled the session group", sib)
			}
			waitDead(t, m.id, m.group)
			select {
			case <-waitDone:
			case <-time.After(2 * time.Second):
				t.Fatal("supervisor still running")
			}
		})
	}
}

func TestPidTreeAwkParsesBSDPS(t *testing.T) {
	awk, err := exec.LookPath("awk")
	if err != nil {
		t.Skip("awk not on PATH")
	}
	script := killScript(mark{id: 42, group: false})
	if !strings.Contains(script, "pid=,ppid=") {
		t.Fatalf("kill script %q", script)
	}
	tests := []struct {
		name string
		in   string
		root string
		want string
	}{
		{
			name: "bsd header and spacing",
			in:   "  PID  PPID\n    10     1\n   20    10\n    21    20\n   30     1\n",
			root: "10",
			want: "10 20 21",
		},
		{
			name: "linux columns",
			in:   "     10       1\n     11      10\n     12      11\n",
			root: "10",
			want: "10 11 12",
		},
		{
			name: "blanks",
			in:   "\n\n10 1\n\n11 10\n",
			root: "10",
			want: "10 11",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(awk, "-v", "r="+tt.root, pidTreeAwk)
			cmd.Stdin = strings.NewReader(tt.in)
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(out)) != tt.want {
				t.Fatalf("got %q want %q", out, tt.want)
			}
		})
	}
}

func waitDead(t *testing.T, id int, group bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		if group {
			err = syscall.Kill(-id, 0)
		} else {
			err = syscall.Kill(id, 0)
		}
		if err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d still running", id)
}
