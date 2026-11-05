package main

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
)

func windowStoreForTest(t *testing.T, on bool) *layoutWindowStates {
	t.Helper()
	return &layoutWindowStates{path: filepath.Join(t.TempDir(), "window-state.json"), enabled: on, reportRead: func(error) {}, reportSave: func(error) {}}
}

func TestLayoutWindowStatesSwitch(t *testing.T) {
	s := windowStoreForTest(t, true)
	initial := map[string]mygo.WindowState{"main": {X: 30, Y: 40, Width: 1000, Height: 700}}
	if err := s.write(initial); err != nil {
		t.Fatal(err)
	}
	cache, err := s.LoadWindowStates()
	if err != nil || cache["main"] != initial["main"] {
		t.Fatalf("load %v, %v", cache, err)
	}
	if err := s.setEnabled(false); err != nil {
		t.Fatal(err)
	}
	if len(cache) != 0 {
		t.Fatal("MyGo process cache survived disable")
	}
	// Simulate captures by MyGo after disabling memory, including at shutdown.
	cache["main"] = initial["main"]
	if err := s.SaveWindowStates(initial); err != nil {
		t.Fatal(err)
	}
	if len(cache) != 0 {
		t.Fatal("disabled native save retained cached geometry")
	}
	off := &layoutWindowStates{path: s.path}
	states, err := off.LoadWindowStates()
	if err != nil || len(states) != 0 {
		t.Fatalf("disabled startup restored geometry: %v %v", states, err)
	}
	if err := s.setEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWindowStates(initial); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]mygo.WindowState
	if err := json.Unmarshal(data, &saved); err != nil || saved["main"] != initial["main"] {
		t.Fatalf("enabled save %s: %v", data, err)
	}
}

func TestLayoutWindowStatesProtectBadFile(t *testing.T) {
	for _, raw := range []string{`broken`, `{"main":{"width":0,"height":100}}`} {
		t.Run(raw, func(t *testing.T) {
			s := windowStoreForTest(t, true)
			reads := 0
			s.reportRead = func(error) { reads++ }
			if err := os.WriteFile(s.path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			cache, err := s.LoadWindowStates()
			if err != nil || len(cache) != 0 || reads != 1 {
				t.Fatalf("bad read: %v, %v, reported %d", cache, err, reads)
			}
			if err := s.SaveWindowStates(map[string]mygo.WindowState{}); err == nil {
				t.Fatal("damaged file was overwritten")
			}
			data, _ := os.ReadFile(s.path)
			if string(data) != raw {
				t.Fatalf("damaged evidence overwritten: %s", data)
			}
			if err := s.setEnabled(false); err != nil {
				t.Fatal(err)
			}
			if err := s.setEnabled(true); err != nil {
				t.Fatal(err)
			}
			if err := s.SaveWindowStates(map[string]mygo.WindowState{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLayoutWindowStatesErrors(t *testing.T) {
	s := windowStoreForTest(t, true)
	// A directory is unreadable as JSON and cannot be replaced by an atomic file.
	if err := os.Mkdir(s.path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.path, "keep"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	reads, writes := 0, 0
	s.reportRead = func(error) { reads++ }
	s.reportSave = func(error) { writes++ }
	if _, err := s.LoadWindowStates(); err != nil || reads != 1 {
		t.Fatalf("read error not visible: %v %d", err, reads)
	}
	s.blocked = nil
	s.states["main"] = mygo.WindowState{Width: 900, Height: 600}
	if err := s.setEnabled(false); err == nil || !s.enabled || len(s.states) != 1 {
		t.Fatal("failed toggle changed live state")
	}
	if err := s.SaveWindowStates(map[string]mygo.WindowState{}); err == nil || writes != 1 {
		t.Fatal("save error not visible")
	}
}

func TestLayoutWindowStatesDisabledNewWindow(t *testing.T) {
	s := windowStoreForTest(t, false)
	s.states = map[string]mygo.WindowState{"editor": {Width: 900, Height: 600}}
	previous := nativeLayout
	nativeLayout = s
	t.Cleanup(func() { nativeLayout = previous })
	prepareWindowState("editor")
	if len(s.states) != 0 {
		t.Fatal("new disabled editor would restore MyGo's cached geometry")
	}
	if _, err := os.Stat(s.path); !os.IsNotExist(err) {
		t.Fatalf("disabled store wrote file: %v", err)
	}
}

func TestLayoutWindowStatesRollback(t *testing.T) {
	for _, raw := range []string{
		"{\n  \"main\": {\"width\": 1000, \"height\": 700}, \"editor\": {\"x\": 42, \"width\": 900, \"height\": 600}\n}\n",
		"damaged original geometry\n",
	} {
		t.Run(raw, func(t *testing.T) {
			s := windowStoreForTest(t, true)
			if err := os.WriteFile(s.path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			cache, err := s.LoadWindowStates()
			if err != nil {
				t.Fatal(err)
			}
			s.normal = map[string]mygo.WindowState{"editor": {Width: 800, Height: 500}}
			states, normal, blocked := maps.Clone(cache), maps.Clone(s.normal), s.blocked
			undo, err := s.changeEnabled(false)
			if err != nil || undo == nil {
				t.Fatalf("disable transaction: %v", err)
			}
			if _, err := os.Stat(s.path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("disable retained file: %v", err)
			}
			// A failed settings write must restore the snapshot, not recapture only
			// currently open windows or reconstruct damaged JSON from an empty cache.
			cache["other"] = mygo.WindowState{Width: 400, Height: 300}
			if err := undo(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(s.path)
			if err != nil || string(data) != raw {
				t.Fatalf("original bytes lost: %q, %v", data, err)
			}
			if !s.enabled || s.blocked != blocked || !maps.Equal(cache, states) || !maps.Equal(s.normal, normal) {
				t.Fatalf("original native state lost: cache=%v normal=%v enabled=%v blocked=%v", cache, s.normal, s.enabled, s.blocked)
			}
			cache["identity"] = mygo.WindowState{Width: 10, Height: 10}
			if _, ok := s.states["identity"]; !ok {
				t.Fatal("rollback replaced MyGo shared map")
			}
		})
	}
}

func TestLayoutWindowStatesEnableRollbackMissingFile(t *testing.T) {
	s := windowStoreForTest(t, false)
	cache, err := s.LoadWindowStates()
	if err != nil {
		t.Fatal(err)
	}
	cache["editor"] = mygo.WindowState{Width: 800, Height: 600}
	s.blocked = errors.New("previous read failure")
	blocked := s.blocked
	undo, err := s.changeEnabled(true)
	if err != nil || undo == nil {
		t.Fatalf("enable transaction: %v", err)
	}
	if err := undo(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback created previously absent file: %v", err)
	}
	if s.enabled || s.blocked != blocked || cache["editor"].Width != 800 || s.normal != nil {
		t.Fatal("enable rollback lost original memory state")
	}
}

func TestLayoutWindowStatesTransactionReadFailure(t *testing.T) {
	s := windowStoreForTest(t, true)
	if err := os.Mkdir(s.path, 0o700); err != nil {
		t.Fatal(err)
	}
	s.states = map[string]mygo.WindowState{"editor": {Width: 800, Height: 600}}
	undo, err := s.changeEnabled(false)
	if err == nil || undo != nil || !s.enabled || s.states["editor"].Width != 800 {
		t.Fatal("failed snapshot read mutated state")
	}
}

func TestLayoutWindowStatesRollbackWriteFailure(t *testing.T) {
	s := windowStoreForTest(t, true)
	initial := map[string]mygo.WindowState{"editor": {Width: 800, Height: 600}}
	if err := s.write(initial); err != nil {
		t.Fatal(err)
	}
	cache, err := s.LoadWindowStates()
	if err != nil {
		t.Fatal(err)
	}
	undo, err := s.changeEnabled(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := undo(); err == nil {
		t.Fatal("rollback disk failure was hidden")
	}
	if !s.enabled || !maps.Equal(cache, initial) {
		t.Fatal("disk failure prevented restoring live state")
	}
}
