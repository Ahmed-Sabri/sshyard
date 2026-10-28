package session

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type gatedInput struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	written chan string
	bytes.Buffer
}

func (w *gatedInput) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	n, err := w.Buffer.Write(p)
	if w.written != nil {
		w.written <- string(p)
	}
	return n, err
}

func (*gatedInput) Close() error { return nil }

func TestCapturedInputStaysBoundAndRefusesBusyWrites(t *testing.T) {
	old := &gatedInput{entered: make(chan struct{}), release: make(chan struct{})}
	next := &bytes.Buffer{}
	s := &Session{stdin: &serializedInput{WriteCloser: old}, status: Status{State: Connected}}
	w, err := s.CaptureInput()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := w.Write([]byte("whole paste\r")); done <- err }()
	<-old.entered
	busy := make(chan error, 1)
	go func() { _, err := s.Write([]byte("key")); busy <- err }()
	select {
	case err := <-busy:
		if !errors.Is(err, ErrInputBusy) {
			t.Fatalf("busy write: %v", err)
		}
	case <-time.After(time.Second):
		close(old.release)
		t.Fatal("ordinary input blocked on captured write")
	}
	// A reconnect gets an independent lock, even while the old writer remains
	// blocked. New input is never held hostage by an obsolete connection.
	s.mu.Lock()
	s.stdin = &serializedInput{WriteCloser: nopWriteCloser{next}}
	s.mu.Unlock()
	if _, err := s.Write([]byte("new")); err != nil || next.String() != "new" {
		t.Fatalf("new connection blocked by old input: %q, %v", next.String(), err)
	}
	close(old.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if old.String() != "whole paste\r" || next.String() != "new" {
		t.Fatalf("write changed connections: old=%q next=%q", old.String(), next.String())
	}
	if _, err := s.Write([]byte("next")); err != nil || next.String() != "newnext" {
		t.Fatalf("new input: %q, %v", next.String(), err)
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestCaptureInputDisconnected(t *testing.T) {
	s := &Session{}
	if _, err := s.CaptureInput(); err == nil {
		t.Fatal("disconnected capture succeeded")
	}
	// Existing input callers retain Write's original stdin-only contract;
	// capture is deliberately stricter because a snippet must not reconnect.
	buf := &bytes.Buffer{}
	s.stdin = &serializedInput{WriteCloser: nopWriteCloser{buf}}
	if _, err := s.Write([]byte("key")); err != nil || buf.String() != "key" {
		t.Fatalf("existing input contract changed: %q, %v", buf.String(), err)
	}
	if _, err := s.CaptureInput(); err == nil {
		t.Fatal("capture accepted a disconnected session with stale stdin")
	}
}

func TestCapturedInputPreservesTerminalReplies(t *testing.T) {
	old := &gatedInput{entered: make(chan struct{}), release: make(chan struct{}), written: make(chan string, 2)}
	next := &bytes.Buffer{}
	s := New(Config{}, Options{}, 80, 24)
	s.stdin, s.status.State = &serializedInput{WriteCloser: old}, Connected
	w, err := s.CaptureInput()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := w.Write([]byte("paste")); done <- err }()
	<-old.entered
	s.Term.Reply([]byte("reply"))
	// A queued protocol reply belongs to the old connection, even if a new
	// connection has become available before the paste finishes.
	s.mu.Lock()
	s.stdin = &serializedInput{WriteCloser: nopWriteCloser{next}}
	s.mu.Unlock()
	close(old.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"paste", "reply"} {
		select {
		case got := <-old.written:
			if got != want {
				t.Fatalf("write %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("terminal reply was dropped, waiting for %q", want)
		}
	}
	if old.String() != "pastereply" || next.Len() != 0 {
		t.Fatalf("reply moved connections: old=%q new=%q", old.String(), next.String())
	}
}
