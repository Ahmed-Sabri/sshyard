package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"

	"github.com/Licoy/sshyard/internal/app"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/egoist/mygo"
)

// layoutWindowStates shares the map handed to MyGo so disabling memory also
// clears its process cache; MyGo still captures existing windows while off.
// All access, like MyGo's WindowStateStore, happens on the main thread.
type layoutWindowStates struct {
	path                   string
	enabled                bool
	states                 map[string]mygo.WindowState
	normal                 map[string]mygo.WindowState
	blocked                error
	reportRead, reportSave func(error)
	current                map[*mygo.Window]string
}

var nativeLayout *layoutWindowStates

func (s *layoutWindowStates) LoadWindowStates() (map[string]mygo.WindowState, error) {
	s.states = map[string]mygo.WindowState{}
	if s.path == "" {
		s.reportRead(s.blocked)
		return s.states, nil
	}
	if !s.enabled {
		return s.states, nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return s.states, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &s.states)
	}
	if err == nil {
		err = validWindowStates(s.states)
	}
	if err != nil {
		s.states = map[string]mygo.WindowState{}
		s.blocked = err // Never overwrite unreadable or damaged data automatically.
		s.reportRead(err)
	}
	if s.states == nil {
		s.states = map[string]mygo.WindowState{}
	}
	// Return the empty map on error too: MyGo would replace a nil/error result,
	// breaking the shared cache needed to turn restoration off in this process.
	return s.states, nil
}

func validWindowStates(states map[string]mygo.WindowState) error {
	for key, st := range states {
		if key == "" || st.Width <= 0 || st.Height <= 0 {
			return fmt.Errorf("invalid window state")
		}
	}
	return nil
}

func (s *layoutWindowStates) write(states map[string]mygo.WindowState) error {
	data, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		return err
	}
	return settings.WriteFileAtomic(s.path, data, 0o600)
}

func (s *layoutWindowStates) SaveWindowStates(states map[string]mygo.WindowState) error {
	if !s.enabled {
		if s.normal == nil {
			s.normal = map[string]mygo.WindowState{}
		}
		maps.Copy(s.normal, states)
		clear(s.states)
		return nil
	}
	if s.blocked != nil {
		return s.blocked
	}
	s.capture(states)
	if err := s.write(states); err != nil {
		s.reportSave(err)
		return err
	}
	return nil
}

func (s *layoutWindowStates) setEnabled(on bool) error {
	if on == s.enabled {
		return nil
	}
	states := map[string]mygo.WindowState{}
	if on {
		s.capture(states)
	}
	// Explicitly changing the preference may replace a previously damaged file.
	// Persist first so a failed switch keeps the current preference and cache.
	if on {
		if err := s.write(states); err != nil {
			return err
		}
	} else if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if !on {
		s.normal = maps.Clone(s.states)
	}
	clear(s.states)
	maps.Copy(s.states, states)
	s.enabled, s.blocked = on, nil
	return nil
}

// changeEnabled retains exact disk bytes and cache values until the caller has
// committed its settings and workspace, including damaged files and closed keys.
func (s *layoutWindowStates) changeEnabled(on bool) (func() error, error) {
	if on == s.enabled {
		return nil, nil
	}
	data, readErr := os.ReadFile(s.path)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return nil, readErr
	}
	states, normal := maps.Clone(s.states), maps.Clone(s.normal)
	enabled, blocked := s.enabled, s.blocked
	if err := s.setEnabled(on); err != nil {
		return nil, err
	}
	return func() error {
		var err error
		if readErr == nil {
			err = settings.WriteFileAtomic(s.path, data, 0o600)
		} else if err = os.Remove(s.path); errors.Is(err, fs.ErrNotExist) {
			err = nil
		}
		// Keep MyGo's shared map identity even when restoring after a disk failure.
		clear(s.states)
		maps.Copy(s.states, states)
		s.normal, s.enabled, s.blocked = maps.Clone(normal), enabled, blocked
		return err
	}, nil
}

// Both halves of the settings transaction touch MyGo's shared cache on its
// main thread, even if settings were applied by another application callback.
func changeRememberLayout(on bool) (func() error, error) {
	var undo func() error
	err := errors.New("window event loop unavailable")
	mygo.RunOnMain(func() { undo, err = nativeLayout.changeEnabled(on) })
	if err != nil || undo == nil {
		return nil, err
	}
	return func() error {
		err := errors.New("window event loop unavailable")
		mygo.RunOnMain(func() { err = undo() })
		return err
	}, nil
}

func (s *layoutWindowStates) capture(states map[string]mygo.WindowState) {
	for w, key := range s.current {
		st, ok := states[key]
		if !ok {
			st = s.states[key]
			if st.Width <= 0 || st.Height <= 0 {
				st = s.normal[key]
			}
		}
		st.Maximized, st.FullScreen = w.IsMaximized(), w.IsFullScreen()
		if !st.Maximized && !st.FullScreen && !w.IsMinimized() {
			b := w.Bounds()
			st.X, st.Y, st.Width, st.Height = b.X, b.Y, b.Width, b.Height
		}
		if st.Width > 0 && st.Height > 0 {
			states[key] = st
		}
	}
}

func prepareWindowState(key string) {
	if nativeLayout != nil && !nativeLayout.enabled {
		delete(nativeLayout.states, key)
	}
}

func rememberWindow(w *mygo.Window, key string) {
	if nativeLayout == nil || key == "" {
		return
	}
	if nativeLayout.normal == nil {
		nativeLayout.normal = map[string]mygo.WindowState{}
	}
	nativeLayout.normal[key] = nativeLayout.states[key]
	nativeLayout.current[w] = key
	w.OnClosed(func() { delete(nativeLayout.current, w) })
}

func installWindowStates(a *app.App) {
	paths, err := storagePaths()
	nativeLayout = &layoutWindowStates{
		path: filepath.Join(paths.DataDir, "window-state.json"), enabled: a.Settings().RememberLayout,
		states: map[string]mygo.WindowState{}, current: map[*mygo.Window]string{},
		reportRead: a.ReportLayoutReadError, reportSave: a.ReportLayoutError,
	}
	if err != nil {
		// Storage resolution already has its own startup UI; also prevent geometry
		// writes from reaching an unintended relative path.
		nativeLayout.blocked = err
		nativeLayout.path = ""
	}
	mygo.App.SetWindowStateStore(nativeLayout)
}
