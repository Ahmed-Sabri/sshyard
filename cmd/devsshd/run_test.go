//go:build !windows

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/sshtest"
	"golang.org/x/crypto/ssh"
)

func TestExecDisabled(t *testing.T) {
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	enableExec(srv, runner{shell: "/bin/sh", root: t.TempDir()}, false)
	c, err := ssh.Dial("tcp", srv.Addr, &ssh.ClientConfig{
		User:            "demo",
		Auth:            []ssh.AuthMethod{ssh.Password("pw")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Run("true"); err == nil {
		t.Fatal("exec was accepted")
	}
	sess.Close()
	sess, err = c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.Shell(); err != nil {
		t.Fatal(err)
	}
}

// An exec on a channel that asked for a PTY runs in one (ssh -t host
// cmd); without one it does not, and stderr stays apart.
func TestExecInPTY(t *testing.T) {
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	r := runner{shell: "/bin/sh", root: t.TempDir()}
	srv.Exec, srv.ExecPTY, srv.Shell = r.exec, r.execPTY, r.login
	c, err := ssh.Dial("tcp", srv.Addr, &ssh.ClientConfig{
		User:            "demo",
		Auth:            []ssh.AuthMethod{ssh.Password("pw")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	const probe = `if [ -t 0 ] && [ -t 1 ]; then echo tty; else echo notty; fi; stty size 2>/dev/null; echo "$TERM"; exit 3`
	run := func(withPTY bool) (string, string, int) {
		sess, err := c.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		defer sess.Close()
		if withPTY {
			if err := sess.RequestPty("xterm-256color", 30, 100, ssh.TerminalModes{}); err != nil {
				t.Fatal(err)
			}
		}
		var out, errOut strings.Builder
		sess.Stdout, sess.Stderr = &out, &errOut
		err = sess.Run(probe + `; `)
		code := 0
		if e, ok := err.(*ssh.ExitError); ok {
			code = e.ExitStatus()
		} else if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(out.String(), "\r", ""), errOut.String(), code
	}
	out, _, code := run(true)
	if !strings.HasPrefix(out, "tty\n30 100\nxterm-256color\n") || code != 3 {
		t.Errorf("with pty: %q, exit %d", out, code)
	}
	out, _, code = run(false)
	if !strings.HasPrefix(out, "notty\n") || !strings.Contains(out, "dumb") || code != 3 {
		t.Errorf("without pty: %q, exit %d", out, code)
	}
}
