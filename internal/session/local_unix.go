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

// localShells is the user's login shell, run as one.
func localShells() []Shell {
	sh := os.Getenv("SHELL")
	if !filepath.IsAbs(sh) {
		sh = "/bin/sh"
		if runtime.GOOS == "darwin" {
			sh = "/bin/zsh"
		}
	}
	return []Shell{{ID: sh, Name: filepath.Base(sh), Path: sh, Args: []string{"-l"}}}
}

// startLocal starts sh in the user's home, as a terminal app does.
func startLocal(sh Shell, cols, rows int, term string) (localPTY, error) {
	cmd := exec.Command(sh.Path, sh.Args...)
	cmd.Env = append(append(os.Environ(), termEnv(term)...), sh.Env...)
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
