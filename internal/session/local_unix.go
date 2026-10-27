//go:build !windows

package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/creack/pty"
)

type unixPTY struct {
	f    *os.File
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

// localShell is the user's login shell.
func localShell() string {
	if sh := os.Getenv("SHELL"); filepath.IsAbs(sh) {
		return sh
	}
	if runtime.GOOS == "darwin" {
		return "/bin/zsh"
	}
	return "/bin/sh"
}

// startLocal starts the user's shell as a login shell, in their home, as
// a terminal app does.
func startLocal(cols, rows int, term string) (localPTY, error) {
	cmd := exec.Command(localShell(), "-l")
	cmd.Env = append(os.Environ(), "TERM="+term, "COLORTERM=truecolor", "TERM_PROGRAM=SSHYard")
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	p := &unixPTY{f: f, cmd: cmd, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

func (p *unixPTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *unixPTY) Write(b []byte) (int, error) { return p.f.Write(b) }

func (p *unixPTY) Resize(cols, rows int) error {
	return pty.Setsize(p.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (p *unixPTY) Wait() error {
	<-p.done
	return p.err
}

// Close hangs the terminal up, which ends the shell; one that is still
// there two seconds later is killed.
func (p *unixPTY) Close() error {
	err := p.f.Close()
	go func() {
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
			p.cmd.Process.Kill()
		}
	}()
	return err
}
