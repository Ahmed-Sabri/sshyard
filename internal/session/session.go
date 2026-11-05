package session

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/Licoy/sshyard/internal/vt"
	"golang.org/x/crypto/ssh"
)

// State is a session's connection state.
type State int

const (
	Idle State = iota
	Connecting
	Connected
	Reconnecting // waiting to retry after the connection was lost
	Disconnected // the connection was lost and is not retried
	Exited       // the remote shell exited
	Failed       // connecting failed in a way the user must address
	Closed       // the user closed the session
)

func (s State) String() string {
	return [...]string{"idle", "connecting", "connected", "reconnecting", "disconnected", "exited", "failed", "closed"}[s]
}

// Options are a session's behavior.
type Options struct {
	// KeepAlive is how often to probe the server; 0 turns probes off.
	KeepAlive time.Duration
	// KeepAliveMax is how many probes in a row may fail before the
	// connection counts as lost; 0 is 3.
	KeepAliveMax int
	// AutoReconnect reconnects after the connection is lost.
	AutoReconnect bool
	// MaxAttempts bounds automatic reconnects in a row; 0 is 8.
	MaxAttempts int
	// Backoff is the first retry delay, doubled each attempt up to
	// MaxBackoff; 0 are 1 and 30 seconds.
	Backoff, MaxBackoff time.Duration
	// Term is the TERM to request; "" is xterm-256color.
	Term string
	// Scrollback is the terminal's scrollback; 0 is vt.DefaultScrollback.
	Scrollback int
	// NoShell connects without a shell or PTY, for a files (SFTP) pane:
	// the connection is kept, probed and reconnected like a shell's.
	NoShell bool
	// Local runs the user's shell on this computer, in a pseudo-terminal,
	// instead of connecting anywhere: the Config is not used, and the
	// session has no Client. It ends, Exited, with the shell.
	Local bool
	// Shell is the shell a local session runs; zero is the first of
	// Shells.
	Shell Shell
}

// Status is a session's state, for the UI.
type Status struct {
	State State
	Err   error
	// Attempt is the reconnect attempt, from 1; Retry when it starts.
	Attempt int
	Retry   time.Time
	// Connects counts successful connections.
	Connects int
	// Hop is the host being dialed while connecting through jump
	// hosts: a jump host's name, or the host's own at the last hop.
	Hop string
}

// Session is a shell on a host. Its terminal stays the same across
// reconnects, so scrollback survives them.
type Session struct {
	cfg  Config
	opts Options
	Term *vt.Terminal

	// OnChange, when set, is called from any goroutine whenever the
	// terminal or the status changes: the app invalidates its window.
	OnChange func()

	mu      sync.Mutex
	status  Status
	client  *ssh.Client
	release func() // lets go of client: closes it, or hands it back to the pool
	shell   *ssh.Session
	local   localPTY
	stdin   *serializedInput
	cols    int
	rows    int
	cancel  context.CancelFunc
	wake    chan struct{}
	closed  bool
	started bool
	done    chan struct{}

	// probe, when set, is how often to measure keepalive round trip
	// while a metrics bar is on screen. Zero uses KeepAlive.
	probe time.Duration
	// latency is the last answered keepalive round trip.
	latency   time.Duration
	latencyOK bool
	// probeNote wakes keepAlive when SetProbe changes the interval.
	probeNote chan struct{}
	// tag marks the shell's environment (TagEnv), tagged once the server
	// took it, so that ShellCwd finds the shell among the server's
	// processes.
	tag    string
	tagged bool
}

// New returns a session of cols×rows; Start connects it.
func New(cfg Config, opts Options, cols, rows int) *Session {
	if opts.KeepAliveMax == 0 {
		opts.KeepAliveMax = 3
	}
	if opts.MaxAttempts == 0 {
		opts.MaxAttempts = 8
	}
	if opts.Backoff == 0 {
		opts.Backoff = time.Second
	}
	if opts.MaxBackoff == 0 {
		opts.MaxBackoff = 30 * time.Second
	}
	if opts.Term == "" {
		opts.Term = "xterm-256color"
	}
	s := &Session{cfg: cfg, opts: opts, cols: max(cols, 1), rows: max(rows, 1), wake: make(chan struct{}, 1), done: make(chan struct{}), probeNote: make(chan struct{}, 1), tag: newTag()}
	s.Term = vt.New(s.cols, s.rows)
	if opts.Scrollback > 0 {
		s.Term.SetScrollback(opts.Scrollback)
	}
	s.Term.Reply = s.replyInput
	return s
}

// Config returns the session's configuration.
func (s *Session) Config() Config { return s.cfg }

// Status returns the session's status.
func (s *Session) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Client returns the SSH client while connected, for SFTP.
func (s *Session) Client() *ssh.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.State != Connected {
		return nil
	}
	return s.client
}

// SetProbe sets how often a visible metrics bar measures keepalive round
// trip. d <= 0 keeps Options.KeepAlive (and sends nothing when that is
// 0). The same value again does not wake the probe, so a frame may call
// this every time it draws.
func (s *Session) SetProbe(d time.Duration) {
	s.mu.Lock()
	if s.probe == d {
		s.mu.Unlock()
		return
	}
	s.probe = d
	s.mu.Unlock()
	select {
	case s.probeNote <- struct{}{}:
	default:
	}
}

// probeEvery is the current keepalive period.
func (s *Session) probeEvery() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.probe > 0 {
		return s.probe
	}
	return s.opts.KeepAlive
}

// Latency is the last keepalive round trip. ok is false until one probe
// has been answered.
func (s *Session) Latency() (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latency, s.latencyOK
}

func (s *Session) changed() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

func (s *Session) setStatus(fn func(st *Status)) {
	s.mu.Lock()
	fn(&s.status)
	s.mu.Unlock()
	s.changed()
}

// Start connects the session in the background.
func (s *Session) Start() {
	s.mu.Lock()
	if s.started || s.closed {
		s.mu.Unlock()
		return
	}
	s.started = true
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.mu.Unlock()
	go s.run(ctx)
}

// Reconnect connects again now: after a failure, after the connection was
// lost, or while waiting for the next automatic attempt. The user does
// this after fixing what failed (trusting a host key, typing a password).
func (s *Session) Reconnect() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if !s.started {
		s.mu.Unlock()
		s.Start()
		return
	}
	st := s.status.State
	s.mu.Unlock()
	switch st {
	case Reconnecting:
		select {
		case s.wake <- struct{}{}:
		default:
		}
	case Disconnected, Failed, Exited:
		s.mu.Lock()
		s.started = false
		s.done = make(chan struct{})
		s.mu.Unlock()
		s.Start()
	}
}

// Close ends the session for good.
func (s *Session) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	cancel, shell, local := s.cancel, s.shell, s.local
	s.status.State = Closed
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if shell != nil {
		// Only this session's channel: the connection may serve others.
		shell.Close()
	}
	if local != nil {
		local.Close()
	}
	s.changed()
}

// Done is closed when the session's connection loop ends.
func (s *Session) Done() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

// Write sends input to the shell; it is dropped while disconnected.
func (s *Session) Write(p []byte) (int, error) {
	s.mu.Lock()
	w := s.stdin
	s.mu.Unlock()
	if w == nil {
		return 0, errors.New("session: not connected")
	}
	return w.Write(p)
}

// Resize changes the size of the terminal and of the remote PTY.
func (s *Session) Resize(cols, rows int) {
	cols, rows = max(cols, 1), max(rows, 1)
	s.mu.Lock()
	if cols == s.cols && rows == s.rows {
		s.mu.Unlock()
		return
	}
	s.cols, s.rows = cols, rows
	shell, local := s.shell, s.local
	s.mu.Unlock()
	s.Term.Resize(cols, rows)
	if shell != nil {
		shell.WindowChange(rows, cols)
	}
	if local != nil {
		local.Resize(cols, rows)
	}
	s.changed()
}

func (s *Session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// run connects, runs the shell and reconnects until the session ends.
func (s *Session) run(ctx context.Context) {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	defer close(done)
	attempt := 0
	for {
		state := Connecting
		if attempt > 0 {
			state = Reconnecting
		}
		s.setStatus(func(st *Status) { st.State, st.Attempt = state, attempt })
		err := s.connect(ctx)
		if s.isClosed() || ctx.Err() != nil {
			return
		}
		if err == nil {
			// Connected, and the shell ran until it ended.
			attempt = 0
			lost := s.serve(ctx)
			if s.isClosed() {
				return
			}
			if lost == nil {
				s.setStatus(func(st *Status) { st.State, st.Err = Exited, nil })
				return
			}
			err = lost
			if !s.opts.AutoReconnect {
				s.setStatus(func(st *Status) { st.State, st.Err = Disconnected, err })
				return
			}
		} else if !Classify(err).Retryable() || attempt == 0 && s.Status().Connects == 0 || !s.opts.AutoReconnect {
			// The first connection, or one the user has to fix, is not
			// retried by itself: the UI shows the error and a button.
			s.setStatus(func(st *Status) { st.State, st.Err = Failed, err })
			return
		}
		attempt++
		if attempt > s.opts.MaxAttempts {
			s.setStatus(func(st *Status) { st.State, st.Err = Disconnected, err })
			return
		}
		delay := s.opts.Backoff << (attempt - 1)
		if delay > s.opts.MaxBackoff || delay <= 0 {
			delay = s.opts.MaxBackoff
		}
		retry := time.Now().Add(delay)
		s.setStatus(func(st *Status) { st.State, st.Err, st.Attempt, st.Retry = Reconnecting, err, attempt, retry })
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// dial gets the session a connection: the pool's shared one, or its own.
func (s *Session) dial(ctx context.Context) (*ssh.Client, func(), error) {
	cfg := s.cfg
	if len(cfg.Jumps) > 0 {
		cfg.Progress = func(hop string) {
			s.setStatus(func(st *Status) { st.Hop = hop })
			if s.cfg.Progress != nil {
				s.cfg.Progress(hop)
			}
		}
	}
	defer s.setStatus(func(st *Status) { st.Hop = "" })
	if p := cfg.Pool; p != nil {
		return p.Acquire(ctx, cfg.Key, cfg)
	}
	client, err := Dial(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return client, func() { client.Close() }, nil
}

// broken reports the connection lost, to the pool that shares it.
func (s *Session) broken(client *ssh.Client) {
	if s.cfg.Pool != nil {
		s.cfg.Pool.Broken(client)
	}
}

func (s *Session) connect(ctx context.Context) error {
	if s.opts.Local {
		return s.connectLocal()
	}
	client, release, err := s.dial(ctx)
	if err != nil {
		return err
	}
	if s.opts.NoShell {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			release()
			return context.Canceled
		}
		s.client, s.release = client, release
		s.status.State, s.status.Err, s.status.Attempt = Connected, nil, 0
		s.status.Connects++
		s.mu.Unlock()
		s.changed()
		return nil
	}
	shell, err := client.NewSession()
	if err != nil {
		// A shared connection that cannot open a channel is gone.
		s.broken(client)
		release()
		return err
	}
	fail := func(err error) error {
		shell.Close()
		release()
		return err
	}
	s.mu.Lock()
	cols, rows := s.cols, s.rows
	s.mu.Unlock()
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 115200, ssh.TTY_OP_OSPEED: 115200}
	if err := shell.RequestPty(s.opts.Term, rows, cols, modes); err != nil {
		return fail(err)
	}
	// Servers take LC_* by default (AcceptEnv): one that does not just
	// leaves the shell untagged.
	tagged := shell.Setenv(TagEnv, s.tag) == nil
	stdin, err := shell.StdinPipe()
	if err != nil {
		return fail(err)
	}
	out := &termWriter{s}
	shell.Stdout = out
	shell.Stderr = out
	// A new shell starts with the terminal's input as at a reset: the
	// last program may have gone with the mouse or focus reports on.
	s.Term.ResetInputModes(true)
	if err := shell.Shell(); err != nil {
		return fail(err)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fail(context.Canceled)
	}
	s.client, s.release, s.shell, s.stdin, s.tagged = client, release, shell, &serializedInput{WriteCloser: stdin}, tagged
	s.status.State, s.status.Err, s.status.Attempt = Connected, nil, 0
	s.status.Connects++
	s.mu.Unlock()
	s.changed()
	return nil
}

// serve waits for the shell to end. It returns nil when the shell exited
// and the error when the connection was lost.
func (s *Session) serve(ctx context.Context) error {
	s.mu.Lock()
	client, shell, local := s.client, s.shell, s.local
	s.mu.Unlock()
	if local != nil {
		return s.serveLocal(ctx, local)
	}

	lost := make(chan error, 1)
	go s.keepAlive(ctx, client, lost)
	waited := make(chan error, 1)
	if shell != nil {
		go func() { waited <- shell.Wait() }()
	}
	connDone := make(chan error, 1)
	go func() { connDone <- client.Wait() }()

	var err error
	select {
	case err = <-waited:
		var exit *ssh.ExitError
		if err == nil || errors.As(err, &exit) {
			err = nil
		} else if err == io.EOF || errors.As(err, new(*ssh.ExitMissingError)) {
			err = errConnectionLost
		}
	case err = <-lost:
	case e := <-connDone:
		if s.isClosed() {
			break
		}
		err = errors.Join(errConnectionLost, e)
	case <-ctx.Done():
	}
	// What the program asked of the mouse goes with it.
	s.Term.ResetInputModes(false)
	if err != nil {
		// Lost: the other sessions on a shared connection lose it too.
		s.broken(client)
	}
	if shell != nil {
		shell.Close()
	}
	s.mu.Lock()
	release := s.release
	s.client, s.release, s.shell, s.stdin = nil, nil, nil, nil
	s.mu.Unlock()
	if release != nil {
		release()
	}
	return err
}

var errConnectionLost = errors.New("session: connection lost (EOF)")

// ErrKeepAliveTimeout reports a server that stopped answering keepalives.
var ErrKeepAliveTimeout = errors.New("session: server stopped responding (keepalive timeout)")

func (s *Session) keepAlive(ctx context.Context, client *ssh.Client, lost chan<- error) {
	var timer *time.Timer
	var timed <-chan time.Time
	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer = nil
		timed = nil
	}
	arm := func() {
		stopTimer()
		every := s.probeEvery()
		if every <= 0 {
			return
		}
		timer = time.NewTimer(every)
		timed = timer.C
	}
	arm()
	defer stopTimer()
	misses := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.probeNote:
			stopTimer()
			// Hiding the bar must not send a probe of its own.
			if s.probeEvery() <= 0 {
				continue
			}
		case <-timed:
			timer = nil
			timed = nil
		}
		every := s.probeEvery()
		if every <= 0 {
			continue
		}
		start := time.Now()
		reply := make(chan error, 1)
		go func() {
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			reply <- err
		}()
		select {
		case err := <-reply:
			if err != nil {
				lost <- errors.Join(errConnectionLost, err)
				return
			}
			misses = 0
			s.mu.Lock()
			s.latency = time.Since(start)
			s.latencyOK = true
			s.mu.Unlock()
			s.changed()
		case <-time.After(every):
			misses++
			if misses >= s.opts.KeepAliveMax {
				lost <- ErrKeepAliveTimeout
				return
			}
		case <-ctx.Done():
			return
		}
		arm()
	}
}

type termWriter struct{ s *Session }

func (w *termWriter) Write(p []byte) (int, error) {
	w.s.Term.Write(p)
	w.s.changed()
	return len(p), nil
}
