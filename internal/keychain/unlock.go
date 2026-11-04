package keychain

// Unlock explicitly accesses the system keychain and verifies the encrypted
// store, retaining the master key only after successful verification.
func (k *Keychain) Unlock() error {
	k.unlockMu.Lock()
	defer k.unlockMu.Unlock()
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		return &OpError{Op: "closed", Err: ErrLocked}
	}
	b, ok := k.b.(*encryptedStore)
	if !ok || len(b.key) != 0 {
		k.mu.Unlock()
		return nil
	}
	system, exists := b.system, b.exists
	k.mu.Unlock()
	if system == nil {
		return &OpError{Op: "master-read", Err: ErrUnavailable}
	}
	key, err := master(system, exists)
	if err != nil {
		return err
	}
	defer clear(key)
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return &OpError{Op: "closed", Err: ErrLocked}
	}
	return b.unlock(key)
}

// Unlocked reports whether this open store is ready for secret operations.
// Explicitly injected test backends do not require unlocking.
func (k *Keychain) Unlocked() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return false
	}
	if b, ok := k.b.(*encryptedStore); ok {
		return len(b.key) != 0
	}
	return true
}
