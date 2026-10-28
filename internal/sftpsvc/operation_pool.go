package sftpsvc

import (
	"context"
	"sync"
)

const maxPendingOperations = 4

// A reservation remains pending until SSH negotiation really returns. Live
// callers may wait for capacity; four abandoned opens reject new work instead
// of accumulating more goroutines behind uncancellable SSH channel opens.
type operationPool struct {
	mu        sync.Mutex
	pending   int
	abandoned int
	changed   chan struct{}
}

type operationReservation struct {
	pool      *operationPool
	done      bool
	abandoned bool
}

func (p *operationPool) reserve(ctx context.Context) (*operationReservation, error) {
	for {
		p.mu.Lock()
		if err := ctx.Err(); err != nil {
			p.mu.Unlock()
			return nil, err
		}
		if p.pending < maxPendingOperations {
			p.pending++
			p.mu.Unlock()
			return &operationReservation{pool: p}, nil
		}
		if p.abandoned == p.pending {
			p.mu.Unlock()
			return nil, ErrOperationsBusy
		}
		changed := p.changed
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (p *operationPool) wake() {
	close(p.changed)
	p.changed = make(chan struct{})
}

func (r *operationReservation) abandon() {
	p := r.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.done || r.abandoned {
		return
	}
	r.abandoned = true
	p.abandoned++
	p.wake()
}

func (r *operationReservation) release() {
	p := r.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.done {
		return
	}
	r.done = true
	p.pending--
	if r.abandoned {
		p.abandoned--
	}
	p.wake()
}
