// Package keychain keeps SSHYard's secrets (the passwords, private keys and
// passphrases of credentials, proxy and forward passwords, and AI provider
// API keys) in the operating system's keychain: the macOS
// Keychain, the Windows Credential Manager, or the Secret Service on
// Linux, through mygo.Secrets. SSHYard never writes a credential to disk
// but sealed in a backup under a password the user sets (package vault),
// and has no master password that unlocks the app.
//
// Keychains limit the size of a secret (mygo.MaxSecretSize, 2560 bytes),
// and a private key with its comment can exceed it, so a longer secret is
// split across several entries behind a small header entry.
package keychain

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
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
// ai/<profileID>/apikey, ai/<agentID>/env/<NAME> and mcp/token.

// Cred is the entry of a credential's secret: cred/<id>/<kind>.
func Cred(id string, kind Kind) string { return "cred/" + id + "/" + string(kind) }

// ProxyPassword is the entry of a proxy's password: proxy/<id>/password.
func ProxyPassword(id string) string { return "proxy/" + id + "/password" }

// ForwardPassword is the entry of the password a dynamic forward's
// clients authenticate with: forward/<id>/password.
func ForwardPassword(id string) string { return "forward/" + id + "/password" }

// MCPToken is the bearer token of the MCP endpoint.
const MCPToken = "mcp/token"

// AIKey is the API key of a built-in provider profile: ai/<profileID>/apikey.
func AIKey(profileID string) string { return "ai/" + profileID + "/apikey" }

// AIAgentEnv is an environment variable injected into an external agent
// from the keychain: ai/<agentID>/env/<NAME>. A3 reads it; nothing here
// puts the value into a process.
func AIAgentEnv(agentID, name string) string { return "ai/" + agentID + "/env/" + name }

var (
	// ErrNotFound reports that the keychain has no such secret.
	ErrNotFound = errors.New("keychain: secret not found")
	// ErrUnavailable reports that the system has no keychain SSHYard can
	// use (on Linux, no Secret Service is running).
	ErrUnavailable = errors.New("keychain: system keychain unavailable")
)

// Backend is a store of secrets: mygo.Secrets in the app, a map in tests.
// Get returns ErrNotFound for a missing key and ErrUnavailable when there
// is no keychain.
type Backend interface {
	Set(key string, value []byte) error
	Get(key string) ([]byte, error)
	Delete(key string) error
}

// MaxChunk is the largest entry the keychain stores.
const MaxChunk = 2560

// chunkMagic starts the header entry of a secret split in chunks; a NUL
// byte keeps it from being mistaken for a password typed by the user.
var chunkMagic = []byte("\x00sshyard-chunks:")

// Keychain stores secrets in a Backend, under the keys above.
type Keychain struct {
	b     Backend
	chunk int
}

// New returns a Keychain over b.
func New(b Backend) *Keychain { return &Keychain{b: b, chunk: MaxChunk} }

// Backend is where the keychain keeps its entries.
func (k *Keychain) Backend() Backend { return k.b }

func chunkKey(key string, i int) string { return key + "#" + strconv.Itoa(i) }

// Available reports whether the keychain can be used, by reading an entry
// that does not exist.
func (k *Keychain) Available() bool {
	_, err := k.b.Get("sshyard/probe")
	return err == nil || errors.Is(err, ErrNotFound)
}

// Set stores the secret of key, replacing what it had.
func (k *Keychain) Set(key string, value []byte) error {
	old := k.chunks(key)
	if len(value) <= k.chunk && !bytes.HasPrefix(value, chunkMagic) {
		if err := k.b.Set(key, value); err != nil {
			return err
		}
		k.deleteChunks(key, 0, old)
		return nil
	}
	n := (len(value) + k.chunk - 1) / k.chunk
	for i := range n {
		part := value[i*k.chunk : min(len(value), (i+1)*k.chunk)]
		if err := k.b.Set(chunkKey(key, i), part); err != nil {
			k.deleteChunks(key, 0, n)
			return err
		}
	}
	if err := k.b.Set(key, append(append([]byte{}, chunkMagic...), strconv.Itoa(n)...)); err != nil {
		k.deleteChunks(key, 0, n)
		return err
	}
	k.deleteChunks(key, n, old)
	return nil
}

// chunks returns the number of chunks key's secret is split in, 0 when it
// is not split.
func (k *Keychain) chunks(key string) int {
	v, err := k.b.Get(key)
	if err != nil || !bytes.HasPrefix(v, chunkMagic) {
		return 0
	}
	n, _ := strconv.Atoi(string(v[len(chunkMagic):]))
	return n
}

func (k *Keychain) deleteChunks(key string, from, to int) {
	for i := from; i < to; i++ {
		k.b.Delete(chunkKey(key, i))
	}
}

// Get returns the secret of key, or ErrNotFound.
func (k *Keychain) Get(key string) ([]byte, error) {
	v, err := k.b.Get(key)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(v, chunkMagic) {
		return v, nil
	}
	n, err := strconv.Atoi(string(v[len(chunkMagic):]))
	if err != nil || n <= 0 {
		return nil, fmt.Errorf("keychain: %s: corrupt chunk header", key)
	}
	var out []byte
	for i := range n {
		part, err := k.b.Get(chunkKey(key, i))
		if err != nil {
			return nil, fmt.Errorf("keychain: %s: chunk %d: %w", key, i, err)
		}
		out = append(out, part...)
	}
	return out, nil
}

// Has reports whether there is a secret of key; false when it cannot be
// read either. Use Exists where a read error matters.
func (k *Keychain) Has(key string) bool {
	ok, err := k.Exists(key)
	return ok && err == nil
}

// Exists reports whether there is a secret of key, and an error when the
// keychain could not be read (locked, denied), which is not "no secret".
func (k *Keychain) Exists(key string) (bool, error) {
	_, err := k.b.Get(key)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotFound):
		return false, nil
	}
	return false, err
}

// Delete removes the secret of key; a missing one is not an error.
func (k *Keychain) Delete(key string) error {
	n := k.chunks(key)
	if err := k.b.Delete(key); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	k.deleteChunks(key, 0, n)
	return nil
}

// DeleteCred removes every secret of a credential.
func (k *Keychain) DeleteCred(id string) error {
	var errs []error
	for _, kind := range Kinds {
		errs = append(errs, k.Delete(Cred(id, kind)))
	}
	return errors.Join(errs...)
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

// Memory is a Backend in memory, for tests and for the session-only
// fallback when the system has no keychain: what it holds is gone when
// the app quits.
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
