package session

import (
	"context"
	"errors"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Pool shares one authenticated SSH connection per host among sessions,
// as OpenSSH's ControlMaster does: a second terminal, a split or a files
// pane on a host already connected opens a channel (a shell, the SFTP
// subsystem) on the connection there is, without dialing or asking for a
// password again. Connections are counted: the last session to let go of
// one closes it. When one drops, every session on it reconnects, and the
// first to do so dials the one new connection the others then share, so
// the credentials are asked for once.
type Pool struct {
	mu    sync.Mutex
	conns map[string]*shared
	// dial makes a connection; Dial, or a stand-in in tests.
	dial func(ctx context.Context, cfg Config) (*ssh.Client, error)
}

type shared struct {
	key    string
	client *ssh.Client
	err    error
	ready  chan struct{} // closed once dialed (or failed)
	refs   int
	broken bool
}

// NewPool returns an empty pool.
func NewPool() *Pool { return &Pool{conns: map[string]*shared{}, dial: Dial} }

// Acquire returns the pool's connection under key, dialing it with cfg
// when there is none (or the one there was broke). Sessions that ask
// while it is being dialed wait for that dial and share what it gives,
// its error too. release lets go of the connection; it is safe to call
// more than once.
func (p *Pool) Acquire(ctx context.Context, key string, cfg Config) (client *ssh.Client, release func(), err error) {
	for {
		p.mu.Lock()
		sh := p.conns[key]
		owner := false
		if sh == nil {
			sh = &shared{key: key, ready: make(chan struct{})}
			p.conns[key] = sh
			owner = true
		}
		sh.refs++
		p.mu.Unlock()
		if owner {
			c, err := p.dial(ctx, cfg)
			p.mu.Lock()
			sh.client, sh.err = c, err
			if err != nil && p.conns[key] == sh {
				delete(p.conns, key)
			}
			p.mu.Unlock()
			close(sh.ready)
		} else {
			select {
			case <-sh.ready:
			case <-ctx.Done():
				p.release(sh)
				return nil, nil, ctx.Err()
			}
		}
		if sh.err != nil {
			p.release(sh)
			if !owner && ctx.Err() == nil && errors.Is(sh.err, context.Canceled) {
				// The session dialing it was closed meanwhile: dial anew.
				continue
			}
			return nil, nil, sh.err
		}
		p.mu.Lock()
		broken := sh.broken
		p.mu.Unlock()
		if broken {
			// It broke between the dial and now: dial again.
			p.release(sh)
			continue
		}
		var once sync.Once
		return sh.client, func() { once.Do(func() { p.release(sh) }) }, nil
	}
}

func (p *Pool) release(sh *shared) {
	p.mu.Lock()
	sh.refs--
	last := sh.refs <= 0
	if last && p.conns[sh.key] == sh {
		delete(p.conns, sh.key)
	}
	p.mu.Unlock()
	if last && sh.client != nil {
		sh.client.Close()
	}
}

// Broken reports a connection lost: the pool forgets it, so that the
// next Acquire dials a new one, and closes it, so that every session on
// it notices.
func (p *Pool) Broken(client *ssh.Client) {
	if client == nil {
		return
	}
	p.mu.Lock()
	for k, sh := range p.conns {
		if sh.client == client {
			sh.broken = true
			delete(p.conns, k)
		}
	}
	p.mu.Unlock()
	client.Close()
}

// Shared reports how many sessions hold the connection under key, 0 when
// there is none.
func (p *Pool) Shared(key string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sh := p.conns[key]; sh != nil {
		return sh.refs
	}
	return 0
}
