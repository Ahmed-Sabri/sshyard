package acp

import (
	"sync"
	"unicode/utf8"
)

// ring keeps the last cap bytes written. It is diagnostics only.
type ring struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func (r *ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if r.cap > 0 && len(r.buf) > r.cap {
		r.buf = append([]byte(nil), r.buf[len(r.buf)-r.cap:]...)
		for len(r.buf) > 0 && !utf8.RuneStart(r.buf[0]) {
			r.buf = r.buf[1:]
		}
	}
	return len(p), nil
}

func (r *ring) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}

// tailBuf keeps the last limit bytes and the untruncated total.
// limit 0 keeps nothing.
type tailBuf struct {
	mu    sync.Mutex
	buf   []byte
	limit int
	n     int
}

func (t *tailBuf) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.n += len(p)
	if t.limit <= 0 || len(p) == 0 {
		return len(p), nil
	}
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.limit:]...)
		for len(t.buf) > 0 && !utf8.RuneStart(t.buf[0]) {
			t.buf = t.buf[1:]
		}
	}
	return len(p), nil
}

func (t *tailBuf) snapshot() (out string, truncated bool, total int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf), t.n > t.limit, t.n
}
