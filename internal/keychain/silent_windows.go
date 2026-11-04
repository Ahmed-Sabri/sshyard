package keychain

// The Credential Manager never asks before the user's own programs read it.
func silently(get func() ([]byte, error)) ([]byte, error) { return get() }
