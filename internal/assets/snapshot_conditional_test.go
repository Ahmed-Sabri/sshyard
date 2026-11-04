package assets

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

func conditionalStore(t *testing.T) (*Store, Host) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.Add(Host{Name: "original", Address: "example.test", User: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	return s, h
}
func TestRestoreIfHostsUnchanged(t *testing.T) {
	for _, kind := range []string{"unchanged", "same-id", "unrelated"} {
		t.Run(kind, func(t *testing.T) {
			s, h := conditionalStore(t)
			baseline := s.List()
			imported := h
			imported.Name = "imported"
			switch kind {
			case "same-id":
				changed := h
				changed.Name = "concurrent"
				if _, err := s.Update(changed); err != nil {
					t.Fatal(err)
				}
			case "unrelated":
				if _, err := s.Add(Host{Name: "unrelated", Address: "other.test", User: "bob"}); err != nil {
					t.Fatal(err)
				}
			}
			before := s.Snapshot()
			err := s.RestoreIfHostsUnchanged(Snapshot{Hosts: []Host{imported}}, baseline)
			if kind == "unchanged" {
				if err != nil || s.List()[0].Name != "imported" {
					t.Fatalf("matching baseline failed: %v", err)
				}
			} else {
				if !errors.Is(err, ErrSnapshotChanged) || !reflect.DeepEqual(before, s.Snapshot()) {
					t.Fatal("changed baseline was overwritten")
				}
			}
			assertConditionalReopen(t, s)
		})
	}
}
func TestRestoreIfHostsUnchangedFailures(t *testing.T) {
	for _, kind := range []string{"validation", "write"} {
		t.Run(kind, func(t *testing.T) {
			s, h := conditionalStore(t)
			baseline := s.List()
			before := s.Snapshot()
			beforeDisk, err := os.ReadFile(s.path)
			if err != nil {
				t.Fatal(err)
			}
			h.Name = "imported"
			snap := Snapshot{Hosts: []Host{h}}
			if kind == "validation" {
				snap.Forwards = []Forward{{ID: NewID(), Kind: ForwardLocal, HostID: "missing", ListenPort: 1234, TargetAddr: "target", TargetPort: 80}}
			} else {
				if err := os.Remove(s.path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(s.path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			err = s.RestoreIfHostsUnchanged(snap, baseline)
			if err == nil || errors.Is(err, ErrSnapshotChanged) || !reflect.DeepEqual(before, s.Snapshot()) {
				t.Fatal("failed conditional restore changed store")
			}
			if kind == "validation" {
				afterDisk, err := os.ReadFile(s.path)
				if err != nil || !bytes.Equal(beforeDisk, afterDisk) {
					t.Fatal("failed validation changed disk")
				}
			}
		})
	}
}
func TestRestoreIfHostsUnchangedConcurrent(t *testing.T) {
	s, h := conditionalStore(t)
	baseline := s.List()
	start := make(chan struct{})
	results := make(chan error, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			candidate := h
			candidate.Name = "import-" + strconv.Itoa(i)
			results <- s.RestoreIfHostsUnchanged(Snapshot{Hosts: []Host{candidate}}, baseline)
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	won, changed := 0, 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrSnapshotChanged):
			changed++
		default:
			t.Fatalf("unexpected error %v", err)
		}
	}
	if won != 1 || changed != 15 {
		t.Fatalf("baseline reused concurrently: committed=%d rejected=%d", won, changed)
	}
	assertConditionalReopen(t, s)
}
func TestRestoreIfHostsUnchangedWaitsForWriter(t *testing.T) {
	s, h := conditionalStore(t)
	baseline := s.List()
	entered := make(chan struct{})
	release := make(chan struct{})
	updated := make(chan error, 1)
	// Store.Update calls now while holding the same mutex, providing a real writer barrier.
	s.now = func() time.Time { close(entered); <-release; return time.Now() }
	concurrent := h
	concurrent.Name = "concurrent"
	go func() { _, err := s.Update(concurrent); updated <- err }()
	<-entered
	attempted := make(chan struct{})
	restored := make(chan error, 1)
	go func() {
		close(attempted)
		candidate := h
		candidate.Name = "imported"
		restored <- s.RestoreIfHostsUnchanged(Snapshot{Hosts: []Host{candidate}}, baseline)
	}()
	<-attempted
	select {
	case <-restored:
		t.Fatal("conditional restore crossed in-flight writer")
	default:
	}
	close(release)
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	if err := <-restored; !errors.Is(err, ErrSnapshotChanged) {
		t.Fatalf("in-flight change not detected: %v", err)
	}
	if got, _ := s.Get(h.ID); got.Name != "concurrent" {
		t.Fatal("in-flight writer was overwritten")
	}
}

func assertConditionalReopen(t *testing.T, s *Store) {
	t.Helper()
	reopened, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	// JSON compares all persisted host fields without process-only time monotonic data.
	memory, err := json.Marshal(s.List())
	if err != nil {
		t.Fatal(err)
	}
	disk, err := json.Marshal(reopened.List())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(memory, disk) {
		t.Fatal("persisted hosts differ after reopen")
	}
}
