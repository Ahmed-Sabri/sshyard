package keychain

import (
	"bytes"
	"errors"
	"os"
	"sync"
	"testing"
)

type trackedSystem struct {
	*Memory
	reads, writes, deletes int
}

func (s *trackedSystem) Get(key string) ([]byte, error) {
	s.reads++
	return s.Memory.Get(key)
}
func (s *trackedSystem) Set(key string, value []byte) error {
	s.writes++
	return s.Memory.Set(key, value)
}
func (s *trackedSystem) Delete(key string) error {
	s.deletes++
	return s.Memory.Delete(key)
}

func openLockedTest(t *testing.T, o OpenOptions) *Keychain {
	t.Helper()
	k, err := OpenLocked(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := k.Close(); err != nil {
			t.Error(err)
		}
	})
	return k
}

func TestOpenLockedEmptyNeverAccessesSystem(t *testing.T) {
	o := options(t)
	s := &trackedSystem{Memory: NewMemory()}
	o.System = s
	k := openLockedTest(t, o)
	if k.Unlocked() {
		t.Fatal("new store unlocked")
	}
	mustGetMissing(t, k, "missing")
	if exists, err := k.Exists("missing"); exists || err != nil {
		t.Fatalf("empty store existence: %v %v", exists, err)
	}
	if err := k.Delete("missing"); err != nil {
		t.Fatal(err)
	}
	if err := k.DeleteCred("missing"); err != nil {
		t.Fatal(err)
	}
	if err := k.Apply(nil); err != nil {
		t.Fatal(err)
	}
	if err := k.Set("secret", []byte("value")); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked write: %v", err)
	}
	if err := k.Apply([]Change{{Key: "a", Delete: true}, {Key: "b", Value: []byte("value")}}); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked mixed batch: %v", err)
	}
	if _, err := k.Seal("test", []byte("value"), nil); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked seal: %v", err)
	}
	if _, err := k.Unseal("test", nil, nil); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked unseal: %v", err)
	}
	if s.reads != 0 || s.writes != 0 || s.deletes != 0 {
		t.Fatalf("system accessed: %+v", s)
	}
	if _, err := os.Stat(o.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("encrypted file created: %v", err)
	}
	if second, err := OpenLocked(o); second != nil || !errors.Is(err, ErrBusy) {
		t.Fatalf("locked store lost lifetime lock: %v", err)
	}
}

func TestExplicitUnlockSaveAndReopen(t *testing.T) {
	o := options(t)
	s := &trackedSystem{Memory: NewMemory()}
	o.System = s
	k := openLockedTest(t, o)
	if err := k.Unlock(); err != nil {
		t.Fatal(err)
	}
	if !k.Unlocked() || s.reads != 2 || s.writes != 1 || s.deletes != 0 {
		t.Fatalf("first unlock: unlocked=%v reads=%d writes=%d deletes=%d", k.Unlocked(), s.reads, s.writes, s.deletes)
	}
	if err := k.Set("secret", []byte("value")); err != nil {
		t.Fatal(err)
	}
	sealed, err := k.Seal("test", []byte("value"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Close(); err != nil {
		t.Fatal(err)
	}
	if k.Unlocked() || !errors.Is(k.Unlock(), ErrLocked) {
		t.Fatal("closed store unlocked")
	}
	k = openLockedTest(t, o)
	if s.reads != 2 || s.writes != 1 {
		t.Fatal("reopen accessed system")
	}
	for _, operation := range []func() error{
		func() error { _, err := k.Get("secret"); return err },
		func() error { return k.Set("secret", []byte("new")) },
		func() error { return k.Delete("secret") },
		func() error { return k.Apply(nil) },
	} {
		if err := operation(); !errors.Is(err, ErrLocked) {
			t.Fatalf("existing locked store operation: %v", err)
		}
	}
	if err := k.Unlock(); err != nil {
		t.Fatal(err)
	}
	mustGet(t, k, "secret", []byte("value"))
	if plain, err := k.Unseal("test", sealed, nil); err != nil || string(plain) != "value" {
		t.Fatalf("reopened unseal: %v", err)
	}
	if s.reads != 3 || s.writes != 1 {
		t.Fatal("reopen regenerated master")
	}
}

func TestLockedUnlockFailurePreservesCiphertextAndRetries(t *testing.T) {
	for _, failure := range []string{"denied", "missing", "wrong", "corrupt", "empty"} {
		t.Run(failure, func(t *testing.T) {
			o := options(t)
			k := openTest(t, o)
			if err := k.Set("secret", []byte("value")); err != nil {
				t.Fatal(err)
			}
			k.Close()
			original, err := os.ReadFile(o.Path)
			if err != nil {
				t.Fatal(err)
			}
			s := o.System.(*Memory)
			originalKey, err := s.Get(MasterKey)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(originalKey)
			want := ErrCorrupt
			switch failure {
			case "denied":
				s.Fail = &SystemError{Code: -128, Err: errors.New("denied")}
				want = s.Fail
			case "missing":
				s.Delete(MasterKey)
				want = ErrMasterMissing
			case "wrong":
				s.Set(MasterKey, bytes.Repeat([]byte{7}, 32))
			case "corrupt", "empty":
				broken := append([]byte(nil), original...)
				if failure == "empty" {
					broken = nil
				} else {
					broken[len(broken)-1] ^= 1
				}
				if err := os.WriteFile(o.Path, broken, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(o.Path)
			if err != nil {
				t.Fatal(err)
			}
			k = openLockedTest(t, o)
			if err := k.Unlock(); !errors.Is(err, want) {
				t.Fatalf("unlock failure: %v", err)
			}
			if k.Unlocked() || len(k.b.(*encryptedStore).key) != 0 {
				t.Fatal("failed unlock retained candidate key")
			}
			after, err := os.ReadFile(o.Path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("failed unlock changed ciphertext: %v", err)
			}
			s.Fail = nil
			if err := s.Set(MasterKey, originalKey); err != nil {
				t.Fatal(err)
			}
			if failure == "corrupt" || failure == "empty" {
				// Corruption is fixed externally before reopening, not overwritten.
				k.Close()
				if err := os.WriteFile(o.Path, original, 0o600); err != nil {
					t.Fatal(err)
				}
				k = openLockedTest(t, o)
			}
			if err := k.Unlock(); err != nil {
				t.Fatal(err)
			}
			mustGet(t, k, "secret", []byte("value"))
		})
	}
}

func TestConcurrentExplicitUnlockOnlyAcquiresMasterOnce(t *testing.T) {
	o := options(t)
	s := &trackedSystem{Memory: NewMemory()}
	o.System = s
	k := openLockedTest(t, o)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			k.Unlocked()
			if err := k.Unlock(); err != nil {
				t.Error(err)
			}
			if err := k.Set("secret", []byte("value")); err != nil {
				t.Error(err)
			}
			mustGet(t, k, "secret", []byte("value"))
		})
	}
	wg.Wait()
	if !k.Unlocked() || s.reads != 2 || s.writes != 1 {
		t.Fatalf("concurrent unlock: reads=%d writes=%d", s.reads, s.writes)
	}
}

func TestInjectedBackendDoesNotRequireUnlock(t *testing.T) {
	k := New(NewMemory())
	if !k.Unlocked() || k.Unlock() != nil {
		t.Fatal("injected store requires unlock")
	}
}

func TestFirstExplicitUnlockFailureCanRetry(t *testing.T) {
	for _, stage := range []string{"read", "set", "verify", "mismatch"} {
		t.Run(stage, func(t *testing.T) {
			o := options(t)
			denied := &SystemError{Code: -128, Err: errors.New("denied")}
			s := &failSystem{Memory: NewMemory(), readErr: denied}
			switch stage {
			case "read":
				s.failReadAt = 1
			case "set":
				s.setErr = denied
			case "verify":
				s.failReadAt = 2
			case "mismatch":
				s.corruptVerify = true
			}
			o.System = s
			k := openLockedTest(t, o)
			want := error(denied)
			if stage == "mismatch" {
				want = ErrMasterInvalid
			}
			if err := k.Unlock(); !errors.Is(err, want) {
				t.Fatalf("unlock failure lost cause: %v", err)
			}
			if k.Unlocked() || len(k.b.(*encryptedStore).key) != 0 {
				t.Fatal("failed unlock retained candidate key")
			}
			if _, err := os.Stat(o.Path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed unlock created ciphertext: %v", err)
			}
			before, _ := s.Memory.Get(MasterKey)
			s.failReadAt, s.setErr, s.corruptVerify = 0, nil, false
			if err := k.Unlock(); err != nil {
				t.Fatal(err)
			}
			after, _ := s.Memory.Get(MasterKey)
			if len(before) > 0 && !bytes.Equal(before, after) {
				t.Fatal("retry replaced master")
			}
		})
	}
}
