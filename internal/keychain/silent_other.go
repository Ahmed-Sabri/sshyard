//go:build !darwin && !windows

package keychain

// The Secret Service may ask to unlock its collection, and cannot be told
// not to, so only an explicit unlock reads it.
func silently(func() ([]byte, error)) ([]byte, error) { return nil, ErrInteractionRequired }
