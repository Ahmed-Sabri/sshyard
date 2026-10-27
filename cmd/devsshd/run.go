//go:build !windows

package main

import (
	"io"
	"os"
	"os/exec"

	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
)

// runner starts the shell and exec commands of devsshd.
type runner struct {
	shell, root string
}

// enableExec wires the login shell, and exec requests when on is set.
// With on false the server refuses exec and leaves the shell running.
func enableExec(srv *sshtest.Server, r runner, on bool) {
	srv.Shell = r.login
	srv.Exec, srv.ExecPTY = nil, nil
	if on {
		srv.Exec, srv.ExecPTY = r.exec, r.execPTY
	}
}

func (r runner) env(term string) []string {
	e := append(os.Environ(), "TERM="+term)
	if r.root != "" {
		e = append(e, "HOME="+r.root) // ~ is the root, as SFTP's is
	}
	return e
}

func (r runner) command(args ...string) *exec.Cmd {
	cmd := exec.Command(r.shell, args...)
	cmd.Dir = r.root
	return cmd
}

func exitCode(err error, other int) int {
	if err == nil {
		return 0
	}
	if e, ok := err.(*exec.ExitError); ok {
		return e.ExitCode()
	}
	return other
}

// exec runs an exec request without a PTY: stdout and stderr apart.
func (r runner) exec(ch ssh.Channel, line string) int {
	cmd := r.command("-c", line)
	cmd.Env = r.env("dumb")
	cmd.Stdout, cmd.Stderr = ch, ch.Stderr()
	in, err := cmd.StdinPipe()
	if err != nil {
		return 127
	}
	if err := cmd.Start(); err != nil {
		return 127
	}
	go func() { io.Copy(in, ch); in.Close() }()
	return exitCode(cmd.Wait(), 127)
}

// execPTY runs an exec request on a channel that asked for a PTY, in a
// PTY, as sshd does for ssh -t host cmd.
func (r runner) execPTY(ch ssh.Channel, line string, p sshtest.PTY, resize <-chan sshtest.PTY) int {
	return r.inPTY(r.command("-c", line), ch, p, resize)
}

// login runs the login shell in a PTY.
func (r runner) login(ch ssh.Channel, p sshtest.PTY, resize <-chan sshtest.PTY) int {
	return r.inPTY(r.command("-l"), ch, p, resize)
}

func (r runner) inPTY(cmd *exec.Cmd, ch ssh.Channel, p sshtest.PTY, resize <-chan sshtest.PTY) int {
	term := p.Term
	if term == "" {
		term = "xterm-256color"
	}
	cmd.Env = r.env(term)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(max(p.Cols, 1)), Rows: uint16(max(p.Rows, 1))})
	if err != nil {
		io.WriteString(ch, err.Error()+"\r\n")
		return 1
	}
	defer f.Close()
	go func() {
		for s := range resize {
			pty.Setsize(f, &pty.Winsize{Cols: uint16(s.Cols), Rows: uint16(s.Rows)})
		}
	}()
	go io.Copy(f, ch)
	io.Copy(ch, f)
	return exitCode(cmd.Wait(), 1)
}
