// Package keychain stores business secrets in a local encrypted file, with only
// its random master key in the operating system keychain.
package keychain

import (
	"errors"
	"sync"
)

// Kind is the kind of a credential's secret.
type Kind string

const (
	Password   Kind = "password"
	PrivateKey Kind = "key"
	Passphrase Kind = "passphrase"
)

// Kinds are every kind of secret a credential has.
var Kinds = []Kind{Password, PrivateKey, Passphrase}

// The keys of the entries SSHYard keeps; there are no others:
// cred/<id>/<kind>, proxy/<id>/password, forward/<id>/password,
// snippet/<id>/content, ai/<profileID>/apikey, ai/<agentID>/env/<NAME> and mcp/token.

// Cred is the entry of a credential's secret: cred/<id>/<kind>.
// Host is a host-owned secret entry.
func Host(id string, kind Kind) string { return "host/" + id + "/" + string(kind) }

func Cred(id string, kind Kind) string { return "cred/" + id + "/" + string(kind) }

// ProxyPassword is the entry of a proxy's password: proxy/<id>/password.
func ProxyPassword(id string) string { return "proxy/" + id + "/password" }

// ForwardPassword is the entry of the password a dynamic forward's
// clients authenticate with: forward/<id>/password.
func ForwardPassword(id string) string { return "forward/" + id + "/password" }

// SnippetContent is the encrypted body of a saved command: snippet/<id>/content.
func SnippetContent(id string) string { return "snippet/" + id + "/content" }

// MCPToken is the bearer token of the MCP endpoint.
const MCPToken = "mcp/token"

// AIKey is the API key of a built-in provider profile: ai/<profileID>/apikey.
func AIKey(profileID string) string { return "ai/" + profileID + "/apikey" }

// AIAgentEnv is an environment variable injected into an external agent
// from the keychain: ai/<agentID>/env/<NAME>. A3 reads it; nothing here
// puts the value into a process.
func AIAgentEnv(agentID, name string) string { return "ai/" + agentID + "/env/" + name }

// Backend is the operating system secret store, or an explicitly injected test store.
type Backend interface {
	Set(key string, value []byte) error
	Get(key string) ([]byte, error)
	Delete(key string) error
}

// Keychain is the secret store facade. New is only for explicit test injection;
// production must use Open, which never falls back to memory.
type Keychain struct {
	mu     sync.Mutex
	b      Backend
	closed bool
}

func New(b Backend) *Keychain { return &Keychain{b: b} }
func (k *Keychain) Backend() Backend {
	if _, ok := k.b.(*encryptedStore); ok {
		return k
	}
	return k.b
}

func (k *Keychain) Get(key string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return nil, &OpError{Op: "closed", Err: ErrLocked}
	}
	return k.b.Get(key)
}
func (k *Keychain) Set(key string, value []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return &OpError{Op: "closed", Err: ErrLocked}
	}
	return k.b.Set(key, value)
}
func (k *Keychain) Delete(key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return &OpError{Op: "closed", Err: ErrLocked}
	}
	err := k.b.Delete(key)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// Has is for presentation only; use Exists when read failures matter.
func (k *Keychain) Has(key string) bool { ok, err := k.Exists(key); return ok && err == nil }
func (k *Keychain) Exists(key string) (bool, error) {
	v, err := k.Get(key)
	clear(v)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// Change is one mutation in an atomic encrypted-file commit.
type Change struct {
	Key    string
	Value  []byte
	Delete bool
}

// Apply commits production changes once. Explicit test backends keep their own
// Get/Set/Delete hooks, including injected failures, with best-effort rollback.
func (k *Keychain) Apply(changes []Change) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return &OpError{Op: "closed", Err: ErrLocked}
	}
	if b, ok := k.b.(*encryptedStore); ok {
		return b.apply(changes)
	}
	old := make([]Change, 0, len(changes))
	defer func() {
		for _, c := range old {
			clear(c.Value)
		}
	}()
	for _, c := range changes {
		v, err := k.b.Get(c.Key)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		old = append(old, Change{Key: c.Key, Value: v, Delete: errors.Is(err, ErrNotFound)})
	}
	for i, c := range changes {
		if err := applyOne(k.b, c); err != nil {
			errs := []error{err}
			for j := i; j >= 0; j-- {
				errs = append(errs, applyOne(k.b, old[j]))
			}
			return errors.Join(errs...)
		}
	}
	return nil
}
func applyOne(b Backend, c Change) error {
	if c.Delete {
		err := b.Delete(c.Key)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	return b.Set(c.Key, c.Value)
}
func (k *Keychain) DeleteCred(id string) error {
	changes := make([]Change, 0, len(Kinds))
	for _, kind := range Kinds {
		changes = append(changes, Change{Key: Cred(id, kind), Delete: true})
	}
	return k.Apply(changes)
}

// Close clears the cached master key and releases the lifetime lock.
func (k *Keychain) Close() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return nil
	}
	k.closed = true
	if b, ok := k.b.(*encryptedStore); ok {
		return b.close()
	}
	return nil
}

// Keys are the keys Memory holds, for tests.
func (m *Memory) Keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for k := range m.m {
		out = append(out, k)
	}
	return out
}

// Memory is an explicitly injected test Backend; production never falls back to it.
type Memory struct {
	mu sync.Mutex
	m  map[string][]byte
	// Fail, when set, is returned by every call.
	Fail error
}

// NewMemory returns an empty Memory.
func NewMemory() *Memory { return &Memory{m: map[string][]byte{}} }

func (m *Memory) Set(key string, v []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.m[key] = append([]byte(nil), v...)
	return nil
}

func (m *Memory) Get(key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	v, ok := m.m[key]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), v...), nil
}

func (m *Memory) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	delete(m.m, key)
	return nil
}

// Len is the number of entries, for tests.
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.m)
}
