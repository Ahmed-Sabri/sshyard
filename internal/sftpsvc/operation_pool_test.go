package sftpsvc

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Done records the exact point at which reserve selects a queued caller's
// cancellation. This proves admission reached the wait without timing sleeps.
type queuedContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *queuedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

type reservationResult struct {
	r   *operationReservation
	err error
}

func fullOperationPool(t *testing.T) (*operationPool, []*operationReservation) {
	t.Helper()
	pool := &operationPool{changed: make(chan struct{})}
	var reservations []*operationReservation
	for range maxPendingOperations {
		r, err := pool.reserve(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		reservations = append(reservations, r)
		t.Cleanup(r.release)
	}
	return pool, reservations
}

func queueReservation(t *testing.T, pool *operationPool, ctx context.Context) <-chan reservationResult {
	t.Helper()
	queued := &queuedContext{Context: ctx, waiting: make(chan struct{})}
	result := make(chan reservationResult, 1)
	go func() { r, err := pool.reserve(queued); result <- reservationResult{r, err} }()
	select {
	case <-queued.waiting:
	case got := <-result:
		t.Fatalf("live capacity rejected instead of queued: %v", got.err)
	case <-time.After(time.Second):
		t.Fatal("caller did not reach admission queue")
	}
	return result
}

func TestLivePendingOperationsQueueBeyondQuota(t *testing.T) {
	pool, reservations := fullOperationPool(t)
	result := queueReservation(t, pool, t.Context())
	// Three abandoned opens must not reject the fifth caller while one live
	// negotiation can still release capacity normally.
	for _, r := range reservations[:3] {
		r.abandon()
	}
	reservations[3].release()
	select {
	case got := <-result:
		if got.err != nil || got.r == nil {
			t.Fatalf("queued admission: %v", got.err)
		}
		got.r.release()
	case <-time.After(time.Second):
		t.Fatal("released capacity did not wake queued caller")
	}
	pool.mu.Lock()
	pending, abandoned := pool.pending, pool.abandoned
	pool.mu.Unlock()
	if pending != 3 || abandoned != 3 {
		t.Fatalf("pending=%d abandoned=%d", pending, abandoned)
	}
}

func TestQueuedOperationCancellationDoesNotConsumeQuota(t *testing.T) {
	pool, _ := fullOperationPool(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := queueReservation(t, pool, ctx)
	cancel()
	select {
	case got := <-result:
		if !errors.Is(got.err, context.Canceled) || got.r != nil {
			t.Fatalf("cancelled queue: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("queued cancellation hung")
	}
	pool.mu.Lock()
	pending, abandoned := pool.pending, pool.abandoned
	pool.mu.Unlock()
	if pending != 4 || abandoned != 0 {
		t.Fatalf("waiting caller consumed quota: pending=%d abandoned=%d", pending, abandoned)
	}
}

func TestQueuedOperationsRejectWhenAllReservationsBecomeOrphans(t *testing.T) {
	pool, reservations := fullOperationPool(t)
	result := queueReservation(t, pool, t.Context())
	for _, r := range reservations {
		r.abandon()
	}
	select {
	case got := <-result:
		if !errors.Is(got.err, ErrOperationsBusy) || got.r != nil {
			t.Fatalf("orphan queue: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("all-orphan capacity did not reject queued caller")
	}
	pool.mu.Lock()
	pending, abandoned := pool.pending, pool.abandoned
	pool.mu.Unlock()
	if pending != 4 || abandoned != 4 {
		t.Fatalf("orphans released early: pending=%d abandoned=%d", pending, abandoned)
	}
	// Only the actual negotiation's completion makes its reservation reusable.
	reservations[0].release()
	fresh, err := pool.reserve(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	fresh.release()
}

func TestOperationReservationCancellationCompletionRace(t *testing.T) {
	pool := &operationPool{changed: make(chan struct{})}
	for range 100 {
		r, err := pool.reserve(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() { <-start; r.abandon() })
		wg.Go(func() { <-start; r.release() })
		close(start)
		wg.Wait()
		pool.mu.Lock()
		pending, abandoned := pool.pending, pool.abandoned
		pool.mu.Unlock()
		if pending != 0 || abandoned != 0 {
			t.Fatalf("reservation leaked: pending=%d abandoned=%d", pending, abandoned)
		}
	}
}

// controlledOpenConn completes one real NewSession admission only when the test
// releases it, so the fifth caller cannot fit before an earlier open returns.
type controlledOpenConn struct {
	ssh.Conn
	opened  chan struct{}
	release chan struct{}
	closed  chan struct{}
}

func (c *controlledOpenConn) OpenChannel(string, []byte) (ssh.Channel, <-chan *ssh.Request, error) {
	c.opened <- struct{}{}
	select {
	case <-c.release:
	case <-c.closed:
	}
	return nil, nil, os.ErrClosed
}
func (c *controlledOpenConn) Wait() error { <-c.closed; return os.ErrClosed }

func TestFifthSFTPOperationWaitsForLiveNegotiation(t *testing.T) {
	raw := &controlledOpenConn{opened: make(chan struct{}, 5), release: make(chan struct{}), closed: make(chan struct{})}
	channels := make(chan ssh.NewChannel)
	close(channels)
	requests := make(chan *ssh.Request)
	close(requests)
	conn := ssh.NewClient(raw, channels, requests)
	defer close(raw.closed)
	results := make(chan error, 5)
	for range maxPendingOperations {
		go func() { _, err := NewContext(t.Context(), conn); results <- err }()
	}
	for range maxPendingOperations {
		select {
		case <-raw.opened:
		case <-time.After(time.Second):
			t.Fatal("initial opens did not start")
		}
	}
	queued := &queuedContext{Context: t.Context(), waiting: make(chan struct{})}
	go func() { _, err := NewContext(queued, conn); results <- err }()
	select {
	case <-queued.waiting:
	case err := <-results:
		t.Fatalf("normal fifth open rejected: %v", err)
	case <-time.After(time.Second):
		t.Fatal("fifth open did not queue")
	}
	select {
	case <-raw.opened:
		t.Fatal("fifth open exceeded four pending negotiations")
	default:
	}
	select {
	case raw.release <- struct{}{}:
	case <-time.After(time.Second):
		t.Fatal("cannot release first negotiation")
	}
	select {
	case <-raw.opened:
	case <-time.After(time.Second):
		t.Fatal("fifth open did not use released reservation")
	}
	for range maxPendingOperations {
		select {
		case raw.release <- struct{}{}:
		case <-time.After(time.Second):
			t.Fatal("remaining negotiations stuck")
		}
	}
	for range 5 {
		select {
		case err := <-results:
			if !errors.Is(err, os.ErrClosed) || errors.Is(err, ErrOperationsBusy) {
				t.Fatalf("negotiation result: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("negotiation did not finish")
		}
	}
}
