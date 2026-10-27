//go:build !windows

package session

import (
	"strings"
	"testing"
)

// TestLocalShell: a local session runs the user's shell on a pty of the
// terminal's size, and ends, Exited, with it.
func TestLocalShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	s := New(Config{}, Options{Local: true}, 80, 24)
	defer s.Close()
	s.Start()
	waitState(t, s, Connected)
	if s.Client() != nil {
		t.Fatal("a local session has an SSH client")
	}
	if _, tagged := s.Tag(); tagged {
		t.Fatal("a local shell is tagged")
	}
	s.Write([]byte("echo out-$((40+2)); stty size\n"))
	waitFor(t, "the shell's output", func() bool { return strings.Contains(s.Term.ScreenText(), "out-42") })
	waitFor(t, "the pty's size", func() bool { return strings.Contains(s.Term.ScreenText(), "24 80") })
	s.Resize(100, 30)
	s.Write([]byte("stty size\n"))
	waitFor(t, "the new size", func() bool { return strings.Contains(s.Term.ScreenText(), "30 100") })
	s.Write([]byte("exit\n"))
	waitState(t, s, Exited)
}

// TestLocalClose: closing a local session ends its shell.
func TestLocalClose(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	s := New(Config{}, Options{Local: true}, 80, 24)
	s.Start()
	waitState(t, s, Connected)
	s.Close()
	<-s.Done()
}
