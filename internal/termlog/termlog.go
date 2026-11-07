// Package termlog writes a terminal session's log: the lines of its
// output as the terminal showed them, in a text file of its own, under a
// folder of the host's. What is typed is never written, only what the
// session printed.
package termlog

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ErrBacklog means the log fell too far behind its file, which stopped
// taking what was written: the log stops rather than hold it all.
var ErrBacklog = errors.New("termlog: the log file is not keeping up")

// maxBacklog is how much output may wait for the file.
const maxBacklog = 8 << 20

// stampLayout is how the first and last lines of a log give the time.
const stampLayout = "2006-01-02 15:04:05 -07:00"

// Header is what the first line of a log says about its session.
type Header struct {
	// Name is the host's name, which also names its folder.
	Name string
	// Target is where it connects to, as user@host:port; "" for none.
	Target string
}

// Recorder writes one session's log. Line and Close may be called from
// any goroutine; Line never waits for the file.
type Recorder struct {
	path    string
	started time.Time
	f       *os.File
	// changed, when set, is told the size or the error changed, from any
	// goroutine.
	changed func()

	mu sync.Mutex
	// buf is what waits to be written, wrote the bytes written.
	buf   []byte
	wrote int64
	// cont is set after a wrapped line, which the next goes on.
	cont   bool
	err    error
	closed bool
	wake   chan struct{}
	done   chan struct{}
}

// Start creates a log for the session h names, at now, in a folder of
// its own under dir; changed, when set, is told as it grows or fails.
func Start(dir string, h Header, now time.Time, changed func()) (*Recorder, error) {
	folder := filepath.Join(dir, SafeName(h.Name))
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return nil, err
	}
	base := now.Format("2006-01-02_150405")
	var f *os.File
	var path string
	for i := 1; ; i++ {
		name := base
		if i > 1 {
			name += "-" + strconv.Itoa(i)
		}
		path = filepath.Join(folder, name+".log")
		var err error
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) || i == 100 {
			return nil, err
		}
	}
	r := &Recorder{path: path, started: now, f: f, changed: changed, wake: make(chan struct{}, 1), done: make(chan struct{})}
	head := "==> " + h.Name
	if h.Target != "" {
		head += " · " + h.Target
	}
	r.buf = append(r.buf, head+" · "+now.Format(stampLayout)+"\n"...)
	go r.run()
	r.signal()
	return r, nil
}

// Path is the log's file.
func (r *Recorder) Path() string { return r.path }

// Started is when the log began.
func (r *Recorder) Started() time.Time { return r.started }

// Size is how many bytes the log file holds.
func (r *Recorder) Size() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.wrote
}

// Err is why the log stopped writing, nil while it writes.
func (r *Recorder) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Line adds a line of output; wrapped says the next goes on in it, as
// vt.Terminal.SetLineHook tells.
func (r *Recorder) Line(text string, wrapped bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.err != nil {
		return
	}
	r.add(text, wrapped)
	if len(r.buf) > maxBacklog {
		r.buf, r.err = nil, ErrBacklog
		r.notify()
		return
	}
	r.signal()
}

// add appends a line to what waits, under mu.
func (r *Recorder) add(text string, wrapped bool) {
	r.buf = append(r.buf, text...)
	r.cont = wrapped
	if !wrapped {
		r.buf = append(r.buf, '\n')
	}
}

// Close ends the log at now with tail, the line the session was on, and
// waits for the file: the error is why it failed, if it did.
func (r *Recorder) Close(tail string, now time.Time) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		<-r.done
		return r.Err()
	}
	if r.err == nil {
		if tail != "" || r.cont {
			r.add(tail, false)
		}
		r.buf = append(r.buf, "<== "+now.Format(stampLayout)+"\n"...)
	}
	r.closed = true
	r.mu.Unlock()
	r.signal()
	<-r.done
	return r.Err()
}

func (r *Recorder) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Recorder) notify() {
	if r.changed != nil {
		r.changed()
	}
}

// run writes what waits each time there is some, until the log closes.
func (r *Recorder) run() {
	defer close(r.done)
	var p []byte
	for range r.wake {
		r.mu.Lock()
		p, r.buf = r.buf, p[:0]
		closed, failed := r.closed, r.err != nil
		r.mu.Unlock()
		var err error
		if len(p) > 0 && !failed {
			var n int
			n, err = r.f.Write(p)
			r.mu.Lock()
			r.wrote += int64(n)
			if err != nil && r.err == nil {
				r.err, r.buf = err, nil
			}
			r.mu.Unlock()
			r.notify()
		}
		if cap(p) > 64<<10 {
			p = nil
		}
		if closed {
			if cerr := r.f.Close(); cerr != nil {
				r.mu.Lock()
				if r.err == nil {
					r.err = cerr
				}
				r.mu.Unlock()
			}
			r.notify()
			return
		}
	}
}

// SafeName is name as a file or folder name on every system: what one of
// them refuses becomes "_", and it is never empty, a device's name, nor
// longer than 80 characters.
func SafeName(name string) string {
	var b strings.Builder
	for _, c := range name {
		if c < 0x20 || c == 0x7f || strings.ContainsRune(`<>:"/\|?*`, c) || c == utf8.RuneError {
			c = '_'
		}
		b.WriteRune(c)
	}
	s := strings.Trim(b.String(), " .")
	if rs := []rune(s); len(rs) > 80 {
		s = strings.TrimRight(string(rs[:80]), " .")
	}
	if s == "" {
		return "session"
	}
	stem := strings.ToUpper(strings.SplitN(s, ".", 2)[0])
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return "_" + s
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9' {
		return "_" + s
	}
	return s
}
