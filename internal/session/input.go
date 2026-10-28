package session

import (
	"errors"
	"io"
	"sync"
)

// ErrInputBusy means another whole input write is still in progress. Input is
// refused rather than queued, so the UI never waits for a blocked writer's lock.
var ErrInputBusy = errors.New("session: input write in progress")

// CaptureInput binds input to the current connection. A later reconnect never
// redirects these writes to a different shell. Writes may block on remote flow
// control, so callers on the UI thread must write in a background goroutine.
func (s *Session) CaptureInput() (io.Writer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.State != Connected || s.stdin == nil {
		return nil, errors.New("session: not connected")
	}
	return s.stdin, nil
}

type serializedInput struct {
	io.WriteCloser
	mu sync.Mutex
}

func (w *serializedInput) Write(p []byte) (int, error) {
	if !w.mu.TryLock() {
		return 0, ErrInputBusy
	}
	defer w.mu.Unlock()
	return w.WriteCloser.Write(p)
}

// Terminal protocol replies already run off the UI thread and must wait for a
// whole paste to finish. Capture before launching so reconnect cannot redirect
// a reply intended for the old shell into the new one.
func (s *Session) replyInput(p []byte) {
	s.mu.Lock()
	w := s.stdin
	s.mu.Unlock()
	if w == nil {
		return
	}
	p = append([]byte(nil), p...)
	go func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.WriteCloser.Write(p)
	}()
}
