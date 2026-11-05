// Package localkeys finds the SSH keys of this computer that a host
// asking for its password offers first: the IdentityFiles ~/.ssh/config
// names for it, else the default key files. It also keeps, beside the
// app's data, which key last logged in to each host and which keys have
// a remembered passphrase; neither is a secret, and neither belongs in a
// backup, since the key files stay on this computer.
package localkeys

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/sshconfig"
	"golang.org/x/crypto/ssh"
)

// Defaults are the key files in ~/.ssh offered when the config names
// none for the host, as ssh does.
var Defaults = []string{"id_ed25519", "id_ecdsa", "id_rsa"}

// maxSize bounds a key file read; a private key is a few KB.
const maxSize = 64 << 10

// Found are the keys for one host.
type Found struct {
	Keys []session.LocalKey
	// IdentitiesOnly is the config's option for the host.
	IdentitiesOnly bool
}

// Find returns the keys for the host at address, named name in SSHYard,
// from the home directory home of localUser. Files that are missing or
// unreadable are left out.
func Find(home, localUser, name, address string) Found {
	var f Found
	dir := filepath.Join(home, ".ssh")
	named := false
	if cfg, err := sshconfig.Parse(filepath.Join(dir, "config"), sshconfig.Env{Home: home, LocalUser: localUser}); err == nil {
		e := resolve(cfg, name, address)
		f.IdentitiesOnly = e.IdentitiesOnly
		named = len(e.IdentityFiles) > 0
		for _, p := range e.IdentityFiles {
			if k, ok := read(p); ok {
				k.Explicit = true
				f.Keys = append(f.Keys, k)
			}
		}
	}
	if !named {
		for _, n := range Defaults {
			if k, ok := read(filepath.Join(dir, n)); ok {
				f.Keys = append(f.Keys, k)
			}
		}
	}
	return f
}

// resolve is the config's entry for the host: the Host block of its name
// in SSHYard when that block points at address, else address itself.
func resolve(cfg *sshconfig.Config, name, address string) sshconfig.Entry {
	for _, e := range cfg.Entries {
		if !e.Pattern && (e.Alias == name || e.Alias == address) && strings.EqualFold(e.HostName, address) {
			return e
		}
	}
	return cfg.Resolve(address)
}

// read reads the key file at path: its public half, from the file or,
// when it does not say, the .pub beside it. A public key alone, named by
// an IdentityFile, is one only the agent signs with.
func read(path string) (session.LocalKey, bool) {
	k := session.LocalKey{Path: path}
	b, err := readSmall(path)
	if err != nil {
		if k.Public = readPub(path + ".pub"); k.Public != nil {
			return k, true
		}
		return k, false
	}
	raw, err := ssh.ParseRawPrivateKey(b)
	var pm *ssh.PassphraseMissingError
	switch {
	case err == nil:
		s, err := ssh.NewSignerFromKey(raw)
		if err != nil {
			return k, false
		}
		k.Public, k.PEM = s.PublicKey(), b
	case errors.As(err, &pm):
		k.Public, k.PEM, k.Encrypted = pm.PublicKey, b, true
		if k.Public == nil {
			k.Public = readPub(path + ".pub")
		}
	default:
		if pub, _, _, _, err := ssh.ParseAuthorizedKey(b); err == nil {
			k.Public = pub
		}
	}
	return k, k.Public != nil
}

func readPub(path string) ssh.PublicKey {
	b, err := readSmall(path)
	if err != nil {
		return nil
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(b)
	if err != nil {
		return nil
	}
	return pub
}

func readSmall(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > maxSize {
		return nil, errors.New("localkeys: not a key file")
	}
	return io.ReadAll(io.LimitReader(f, maxSize))
}

// Remembered is a key whose passphrase is in the encrypted store
// (keychain.LocalKeyPassphrase).
type Remembered struct {
	// ID is the key's session.KeyID.
	ID          string `json:"id"`
	Path        string `json:"path"`
	Fingerprint string `json:"fingerprint"`
}

// State is what SSHYard keeps about the keys of this computer, in a JSON
// file. It is safe for concurrent use.
type State struct {
	mu   sync.Mutex
	path string
	d    stateData
}

type stateData struct {
	// Last is, by host ID, the KeyID that logged in to it last.
	Last       map[string]string `json:"last,omitempty"`
	Remembered []Remembered      `json:"remembered,omitempty"`
}

// Open reads the state at path; a missing or unreadable file is an empty
// state. With path "" the state is never written.
func Open(path string) *State {
	s := &State{path: path}
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			json.Unmarshal(b, &s.d)
		}
	}
	return s
}

// Last is the KeyID that logged in to host last, "" for none.
func (s *State) Last(host string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.Last[host]
}

// SetLast notes that the key id logged in to host.
func (s *State) SetLast(host, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.Last[host] == id {
		return nil
	}
	if s.d.Last == nil {
		s.d.Last = map[string]string{}
	}
	s.d.Last[host] = id
	return s.save()
}

// ForgetHost drops what is kept for a deleted host.
func (s *State) ForgetHost(host string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.d.Last[host]; !ok {
		return nil
	}
	delete(s.d.Last, host)
	return s.save()
}

// Remembered are the keys with a remembered passphrase.
func (s *State) Remembered() []Remembered {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.d.Remembered)
}

// IsRemembered reports whether the passphrase of the key id is remembered.
func (s *State) IsRemembered(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.ContainsFunc(s.d.Remembered, func(r Remembered) bool { return r.ID == id })
}

// Remember notes that the passphrase of r is remembered.
func (s *State) Remember(r Remembered) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Remembered = slices.DeleteFunc(s.d.Remembered, func(o Remembered) bool { return o.ID == r.ID })
	s.d.Remembered = append(s.d.Remembered, r)
	return s.save()
}

// Forget notes that the passphrase of the key id is no longer remembered.
func (s *State) Forget(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Remembered = slices.DeleteFunc(s.d.Remembered, func(o Remembered) bool { return o.ID == id })
	return s.save()
}

func (s *State) save() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return err
	}
	return settings.WriteFileAtomic(s.path, b, 0o600)
}
