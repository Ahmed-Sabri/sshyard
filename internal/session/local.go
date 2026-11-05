package session

import (
	"context"
	"io"
	"strings"
)

// localPTY is a shell running on this computer in a pseudo-terminal: what
// it prints is read, what is typed written. Close ends it.
type localPTY interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	// Wait waits for the shell to end.
	Wait() error
}

// termEnv names the terminal to a local shell, as a terminal app does.
func termEnv(term string) []string {
	return []string{"TERM=" + term, "COLORTERM=truecolor", "TERM_PROGRAM=SSHYard"}
}

// mergeEnv keeps the last entry of each name, in the order the names
// first appear; names fold case when fold is set, as on Windows. A name
// may start with "=", as Windows' per-drive directories do.
func mergeEnv(env []string, fold bool) []string {
	at := map[string]int{}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name := kv
		if i := strings.IndexByte(kv[min(1, len(kv)):], '='); i >= 0 {
			name = kv[:i+1]
		}
		if fold {
			name = strings.ToUpper(name)
		}
		if i, ok := at[name]; ok {
			out[i] = kv
			continue
		}
		at[name] = len(out)
		out = append(out, kv)
	}
	return out
}

// startPTY starts the local shell; a variable so that tests can stand in.
var startPTY = startLocal

// connectLocal starts the shell of a local session.
func (s *Session) connectLocal() error {
	s.mu.Lock()
	cols, rows := s.cols, s.rows
	s.mu.Unlock()
	p, err := startPTY(cols, rows, s.opts.Term)
	if err != nil {
		return err
	}
	s.Term.ResetInputModes(true)
	go io.Copy(&termWriter{s}, p)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		p.Close()
		return context.Canceled
	}
	s.local, s.stdin = p, &serializedInput{WriteCloser: p}
	s.status.State, s.status.Err, s.status.Attempt = Connected, nil, 0
	s.status.Connects++
	s.mu.Unlock()
	s.changed()
	return nil
}

// serveLocal waits for the local shell to end, which is never a lost
// connection: it is not reconnected by itself.
func (s *Session) serveLocal(ctx context.Context, p localPTY) error {
	waited := make(chan struct{})
	go func() {
		p.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-ctx.Done():
	}
	s.Term.ResetInputModes(false)
	p.Close()
	s.mu.Lock()
	s.local, s.stdin = nil, nil
	s.mu.Unlock()
	return nil
}
