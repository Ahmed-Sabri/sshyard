package acp

import (
	"context"
	"errors"
	"sync"
	"time"

	sdk "github.com/coder/acp-go-sdk"
)

// A turn owns callbacks and terminals until their remote cleanup completes.
// Session.mu guards stopped/interrupted and admission to requests.
type activeTurn struct {
	ctx         context.Context
	cancel      context.CancelFunc
	stopped     bool
	interrupted bool
	requests    sync.WaitGroup
	terms       []*termProc
	drain       sync.Once
	notify      sync.Once
	notifyErr   error
	cleanupErr  error
}

func (s *Session) beginTurn(ctx context.Context) (*activeTurn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.sid == "" {
		return nil, errSession
	}
	if s.interrupted {
		return nil, ErrInterruptedSession
	}
	if s.turn != nil {
		return nil, errors.New("agent turn is already running")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tctx, cancel := context.WithCancel(ctx)
	t := &activeTurn{ctx: tctx, cancel: cancel}
	s.turn = t
	return t, nil
}

func (s *Session) callbackContext(ctx context.Context, id sdk.SessionId) (context.Context, func(), error) {
	s.mu.Lock()
	t := s.turn
	if s.closing || s.sid == "" || id != s.sid {
		s.mu.Unlock()
		return nil, nil, errSession
	}
	if t == nil || t.stopped || t.ctx.Err() != nil {
		s.mu.Unlock()
		return nil, nil, context.Canceled
	}
	t.requests.Add(1)
	s.mu.Unlock()
	cctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(t.ctx, cancel)
	if t.ctx.Err() != nil {
		cancel()
	}
	return cctx, func() { stop(); cancel(); t.requests.Done() }, nil
}

// drainTurn closes admission before waiting, so no request can outlive it.
func (s *Session) drainTurn(t *activeTurn) {
	t.drain.Do(func() {
		s.mu.Lock()
		t.stopped = true
		terms := append([]*termProc(nil), t.terms...)
		s.mu.Unlock()
		t.cancel()
		s.cancelPerms()
		for _, term := range terms {
			term.cancel()
		}
		t.requests.Wait()
		for _, term := range terms {
			<-term.done
			// Plain cancellation is expected; wrapped failures can report that
			// remote termination could not be confirmed and must remain visible.
			if term.err != nil && term.err != context.Canceled && term.err != context.DeadlineExceeded {
				t.cleanupErr = errors.Join(t.cleanupErr, term.err)
			}
		}
	})
}

func (s *Session) stopTurn(t *activeTurn) error {
	s.mu.Lock()
	t.stopped = true
	t.interrupted = true
	s.interrupted = true
	sid, conn := s.sid, s.conn
	s.mu.Unlock()
	t.cancel()
	// Notification does not depend on the cancelled Prompt context, and is sent
	// synchronously even when the SDK response races with cancellation.
	t.notify.Do(func() {
		if sid != "" && conn != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			t.notifyErr = conn.Cancel(ctx, sdk.CancelNotification{SessionId: sid})
		}
	})
	s.drainTurn(t)
	return errors.Join(t.notifyErr, t.cleanupErr)
}

func (s *Session) finishTurn(t *activeTurn) {
	s.drainTurn(t)
	s.mu.Lock()
	if s.turn == t {
		s.turn = nil
	}
	s.mu.Unlock()
}
