package keychain

import (
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type blockedMaster struct {
	Backend
	entered chan struct{}
	release chan struct{}
	reads   atomic.Int32
}

func (s *blockedMaster) Get(key string) ([]byte, error) {
	if s.reads.Add(1) == 1 {
		close(s.entered)
	}
	<-s.release
	return s.Backend.Get(key)
}

func pendingStore(t *testing.T) (*Keychain, OpenOptions, *blockedMaster) {
	t.Helper()
	o := options(t)
	k := openTest(t, o)
	if err := k.Set("secret", []byte("value")); err != nil {
		t.Fatal(err)
	}
	k.Close()
	s := &blockedMaster{Backend: o.System, entered: make(chan struct{}), release: make(chan struct{})}
	o.System = s
	return openLockedTest(t, o), o, s
}

func promptly(t *testing.T, run func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); run() }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("operation waited for system authorization")
	}
}

func TestPendingUnlockKeepsReadsResponsiveAndSharesAcquisition(t *testing.T) {
	k, _, s := pendingStore(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := k.Unlock(); err != nil {
				t.Error(err)
			}
		})
	}
	<-s.entered
	// Always release the worker even if the responsiveness check fails.
	t.Cleanup(func() {
		select {
		case <-s.release:
		default:
			close(s.release)
		}
	})
	promptly(t, func() {
		if k.Unlocked() || k.Has("secret") {
			t.Error("pending store reports unlocked")
		}
		if _, err := k.Get("secret"); !errors.Is(err, ErrLocked) {
			t.Errorf("pending read: %v", err)
		}
		if err := k.Set("secret", []byte("new")); !errors.Is(err, ErrLocked) {
			t.Errorf("pending write: %v", err)
		}
		if _, err := k.Seal("test", nil, nil); !errors.Is(err, ErrLocked) {
			t.Errorf("pending seal: %v", err)
		}
	})
	close(s.release)
	wg.Wait()
	if !k.Unlocked() || s.reads.Load() != 1 {
		t.Fatalf("unshared acquisition: %d reads", s.reads.Load())
	}
	mustGet(t, k, "secret", []byte("value"))
}

func TestCloseDuringUnlockRetainsLockAndDiscardsResult(t *testing.T) {
	k, o, s := pendingStore(t)
	before, err := os.ReadFile(o.Path)
	if err != nil {
		t.Fatal(err)
	}
	unlockDone := make(chan error, 1)
	go func() { unlockDone <- k.Unlock() }()
	<-s.entered
	t.Cleanup(func() {
		select {
		case <-s.release:
		default:
			close(s.release)
		}
	})
	closeDone := make(chan error, 1)
	go func() { closeDone <- k.Close() }()
	// closed is published before Close waits for the pending authorization.
	deadline := time.Now().Add(time.Second)
	for {
		k.mu.Lock()
		closed := k.closed
		k.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Close did not mark the store closed")
		}
		time.Sleep(time.Millisecond)
	}
	if other, err := OpenLocked(o); other != nil || !errors.Is(err, ErrBusy) {
		t.Fatalf("pending authorization released lifetime lock: %v", err)
	}
	promptly(t, func() {
		if k.Unlocked() || !errors.Is(k.Set("secret", nil), ErrLocked) {
			t.Error("closed pending store available")
		}
	})
	close(s.release)
	if err := <-unlockDone; !errors.Is(err, ErrLocked) {
		t.Fatalf("late unlock: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(o.Path)
	if err != nil || string(before) != string(after) || k.Unlocked() {
		t.Fatalf("late unlock changed closed store: %v", err)
	}
	openLockedTest(t, o)
}
