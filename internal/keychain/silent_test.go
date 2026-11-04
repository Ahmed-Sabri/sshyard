package keychain

import (
	"errors"
	"sync/atomic"
	"testing"
)

// silentMemory reads silently only when allowed, and counts the reads that
// could have asked the user.
type silentMemory struct {
	*Memory
	allowed atomic.Bool
	asked   atomic.Int32
}

func (s *silentMemory) Get(key string) ([]byte, error) {
	s.asked.Add(1)
	return s.Memory.Get(key)
}

func (s *silentMemory) GetSilently(key string) ([]byte, error) {
	if !s.allowed.Load() {
		return nil, errors.New("would ask")
	}
	return s.Memory.Get(key)
}

func TestTryUnlockOnlyWithoutAsking(t *testing.T) {
	o := options(t)
	mem := o.System.(*Memory)
	k := openTest(t, o)
	if err := k.Set("secret", []byte("value")); err != nil {
		t.Fatal(err)
	}
	k.Close()
	s := &silentMemory{Memory: mem}
	o.System = s

	k = openLockedTest(t, o)
	if err := k.TryUnlock(); err == nil || k.Unlocked() {
		t.Fatalf("unlocked without permission: %v", err)
	}
	s.allowed.Store(true)
	if err := k.TryUnlock(); err != nil || !k.Unlocked() {
		t.Fatalf("silent unlock: %v", err)
	}
	mustGet(t, k, "secret", []byte("value"))
	if s.asked.Load() != 0 {
		t.Fatalf("%d reads could ask the user", s.asked.Load())
	}
}

func TestTryUnlockNeverCreatesOrAsks(t *testing.T) {
	o := options(t)
	s := &silentMemory{Memory: NewMemory()}
	s.allowed.Store(true)
	o.System = s
	k := openLockedTest(t, o)
	if err := k.TryUnlock(); !errors.Is(err, ErrInteractionRequired) || k.Unlocked() || s.Len() != 0 {
		t.Fatalf("empty store: %v, %d entries", err, s.Len())
	}
	o = options(t)
	k = openTest(t, o)
	if err := k.Set("secret", []byte("value")); err != nil {
		t.Fatal(err)
	}
	k.Close()
	k = openLockedTest(t, o)
	if err := k.TryUnlock(); !errors.Is(err, ErrInteractionRequired) || k.Unlocked() {
		t.Fatalf("backend without silent reads: %v", err)
	}
}
