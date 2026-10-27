package updater

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrDisabled reports a build that cannot update itself: a development
// build, or one installed by a package manager.
var ErrDisabled = errors.New("updater: updates are not enabled for this build")

// Release is an update found by a Backend.
type Release interface {
	Version() string
	Notes() string
	Install(ctx context.Context, progress func(downloaded, total int64)) error
}

// Backend checks for and installs releases; mygo.Updater in the app, a
// fake in tests.
type Backend interface {
	Enabled() bool
	// Check returns the newer release, or nil when up to date.
	Check(ctx context.Context) (Release, error)
	Relaunch()
}

// State is where the update client is.
type State int

const (
	Idle State = iota
	Disabled
	Checking
	UpToDate
	Available
	Downloading
	Ready
	Failed
)

// Status is a snapshot of the client for the interface.
type Status struct {
	State      State
	Version    string // of the release available, downloading or ready
	Notes      string
	Downloaded int64
	Total      int64
	Err        error
	Checked    time.Time
}

// Percent is the download's progress, 0 to 100.
func (s Status) Percent() int {
	if s.Total <= 0 {
		return 0
	}
	p := int(s.Downloaded * 100 / s.Total)
	return min(max(p, 0), 100)
}

// Service runs checks and installs off the main thread, telling OnChange.
type Service struct {
	backend  Backend
	onChange func()

	mu      sync.Mutex
	status  Status
	release Release
	busy    bool
}

// New returns a service over backend; onChange, when not nil, is called
// from any goroutine after the status changes.
func New(b Backend, onChange func()) *Service {
	s := &Service{backend: b, onChange: onChange}
	if !b.Enabled() {
		s.status.State = Disabled
	}
	return s
}

// Status returns the current status.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *Service) set(fn func(st *Status)) {
	s.mu.Lock()
	fn(&s.status)
	s.mu.Unlock()
	if s.onChange != nil {
		s.onChange()
	}
}

// Check looks for a release and blocks until it knows.
func (s *Service) Check(ctx context.Context) Status {
	if !s.backend.Enabled() {
		s.set(func(st *Status) { *st = Status{State: Disabled, Err: ErrDisabled} })
		return s.Status()
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return s.Status()
	}
	s.busy = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.busy = false; s.mu.Unlock() }()

	s.set(func(st *Status) { *st = Status{State: Checking} })
	rel, err := s.backend.Check(ctx)
	now := time.Now()
	switch {
	case errors.Is(err, ErrDisabled):
		s.set(func(st *Status) { *st = Status{State: Disabled, Err: err, Checked: now} })
	case err != nil:
		s.set(func(st *Status) { *st = Status{State: Failed, Err: err, Checked: now} })
	case rel == nil:
		s.set(func(st *Status) { *st = Status{State: UpToDate, Checked: now} })
	default:
		s.mu.Lock()
		s.release = rel
		s.mu.Unlock()
		s.set(func(st *Status) {
			*st = Status{State: Available, Version: rel.Version(), Notes: rel.Notes(), Checked: now}
		})
	}
	return s.Status()
}

// Install downloads and installs the release Check found.
func (s *Service) Install(ctx context.Context) Status {
	s.mu.Lock()
	rel := s.release
	if rel == nil || s.busy {
		s.mu.Unlock()
		return s.Status()
	}
	s.busy = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.busy = false; s.mu.Unlock() }()

	version, notes := rel.Version(), rel.Notes()
	s.set(func(st *Status) { *st = Status{State: Downloading, Version: version, Notes: notes} })
	err := rel.Install(ctx, func(d, t int64) {
		s.set(func(st *Status) { st.Downloaded, st.Total = d, t })
	})
	if err != nil {
		s.set(func(st *Status) { *st = Status{State: Failed, Version: version, Err: err} })
		return s.Status()
	}
	s.set(func(st *Status) { st.State, st.Err = Ready, nil })
	return s.Status()
}

// Relaunch restarts the app into the installed release.
func (s *Service) Relaunch() {
	if s.Status().State == Ready {
		s.backend.Relaunch()
	}
}
