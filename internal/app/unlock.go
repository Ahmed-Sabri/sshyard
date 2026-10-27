package app

import (
	"errors"
	"log"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/Licoy/sshyard/internal/keychain"
)

// unlockState keeps a completed store owned until the UI accepts it, or Close
// releases it; an OS authorization prompt itself cannot be cancelled.
type unlockState struct {
	mu       sync.Mutex
	closed   bool
	pending  *keychain.Keychain
	ready    atomic.Bool
	busy     bool
	err      error
	closeErr error
	modal    bool
	hold     focusHold
}

type lockedSecrets struct{}

func (lockedSecrets) Get(string) ([]byte, error) { return nil, keychain.ErrLocked }
func (lockedSecrets) Set(string, []byte) error   { return keychain.ErrLocked }
func (lockedSecrets) Delete(string) error        { return keychain.ErrLocked }

func (a *App) unlocked() bool { return a.unlock.ready.Load() }

func (a *App) retryUnlock() {
	a.unlock.mu.Lock()
	if a.unlock.closed || a.unlock.busy || a.unlocked() {
		a.unlock.mu.Unlock()
		return
	}
	a.unlock.busy = true
	a.unlock.err = nil
	a.unlock.modal = false
	a.unlock.mu.Unlock()
	dataErr := a.resolveStorage()
	go func() {
		var kc *keychain.Keychain
		err := dataErr
		if err == nil {
			kc, err = a.openSecrets()
		}
		a.unlock.mu.Lock()
		if a.unlock.closed {
			if kc != nil {
				a.closeSecrets(kc)
			}
			a.unlock.mu.Unlock()
			return
		}
		a.unlock.pending = kc
		a.post(func() { a.acceptUnlock(err) })
		a.unlock.mu.Unlock()
	}()
}

func (a *App) openSecrets() (*keychain.Keychain, error) {
	if a.p.DataDir == "" {
		return nil, &keychain.OpError{Op: "file-read", Err: errors.New("data directory unavailable")}
	}
	lock := a.p.SecretLockPath
	if lock == "" {
		lock = filepath.Join(a.p.DataDir, "secrets.lock")
	}
	scope := a.p.SecretScope
	if scope == "" {
		scope = "sshyard"
	}
	return keychain.Open(keychain.OpenOptions{Path: filepath.Join(a.p.DataDir, "secrets.enc"), LockPath: lock, Scope: scope, System: a.p.Secrets})
}

func (a *App) acceptUnlock(err error) {
	a.unlock.mu.Lock()
	defer a.unlock.mu.Unlock()
	if a.unlock.closed {
		return
	}
	a.unlock.busy = false
	if err != nil {
		a.unlock.err, a.unlock.modal = err, true
		return
	}
	if err := a.loadData(); err != nil {
		a.closeSecrets(a.unlock.pending)
		a.unlock.pending = nil
		a.unlock.err = &keychain.OpError{Op: "file-read", Err: err}
		a.unlock.modal = true
		return
	}
	a.kc = a.unlock.pending
	a.unlock.pending = nil
	a.keychainOK = true
	a.unlock.ready.Store(true)
	a.syncChoices()
	a.updateCheck = a.settings.CheckUpdates
	a.resolveFonts(false, false)
	if a.p.SetTheme != nil {
		a.p.SetTheme(a.settings.Theme)
	}
	if a.p.SetLocale != nil && a.settings.Language != "" {
		a.p.SetLocale(a.settings.Language)
	}
	a.startUnlocked()
}

// StoragePaths keeps the canonical lifetime lock separate from overridden data.
type StoragePaths struct{ DataDir, LockPath, Scope string }

func (a *App) resolveStorage() error {
	if a.p.ResolveStorage != nil {
		paths, err := a.p.ResolveStorage()
		if err != nil {
			return &keychain.OpError{Op: "file-read", Err: err}
		}
		a.p.DataDir, a.p.SecretLockPath, a.p.SecretScope = paths.DataDir, paths.LockPath, paths.Scope
	} else if a.p.StartupError != nil {
		return &keychain.OpError{Op: "file-read", Err: a.p.StartupError}
	}
	if a.p.DataDir == "" {
		return &keychain.OpError{Op: "file-read", Err: errors.New("data directory unavailable")}
	}
	return nil
}

// Cleanup errors remain observable without exposing system diagnostic strings.
// Callers hold unlock.mu, including the worker that receives a late result.
func (a *App) closeSecrets(kc *keychain.Keychain) {
	if err := kc.Close(); err != nil {
		a.unlock.closeErr = err
		code := "unknown"
		var system *keychain.SystemError
		if errors.As(err, &system) {
			code = strconv.Itoa(system.Code)
		}
		log.Printf("sshyard: credential store cleanup failed (stage=close, code=%s)", code)
	}
}
