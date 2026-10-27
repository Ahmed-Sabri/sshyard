package session

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// UnknownHostError reports a host whose key is not in known_hosts yet.
// The UI shows its fingerprint and, when the user trusts it, calls
// KnownHosts.Add and connects again.
type UnknownHostError struct {
	Host        string // host:port as dialed
	Key         ssh.PublicKey
	Fingerprint string // SHA256:...
}

func (e *UnknownHostError) Error() string {
	return fmt.Sprintf("session: unknown host key for %s (%s %s)", e.Host, e.Key.Type(), e.Fingerprint)
}

// HostKeyChangedError reports a host whose key does not match the one in
// known_hosts: possibly an attack, so SSHYard refuses to connect and does
// not offer to replace the key.
type HostKeyChangedError struct {
	Host        string
	Key         ssh.PublicKey
	Fingerprint string
	Line        int // the line of known_hosts with the old key
	File        string
}

func (e *HostKeyChangedError) Error() string {
	return fmt.Sprintf("session: host key for %s changed (now %s %s; known_hosts %s:%d)", e.Host, e.Key.Type(), e.Fingerprint, e.File, e.Line)
}

// KnownHosts is a known_hosts file in OpenSSH's format, SSHYard's own,
// which it adds keys the user trusts to.
type KnownHosts struct {
	path string
	mu   sync.Mutex
	cb   ssh.HostKeyCallback
}

// OpenKnownHosts opens the known_hosts file at path, creating it.
func OpenKnownHosts(path string) (*KnownHosts, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	k := &KnownHosts{path: path}
	return k, k.reload()
}

// Path is the file's path.
func (k *KnownHosts) Path() string { return k.path }

func (k *KnownHosts) reload() error {
	cb, err := knownhosts.New(k.path)
	if err != nil {
		return err
	}
	k.mu.Lock()
	k.cb = cb
	k.mu.Unlock()
	return nil
}

// Callback is the ssh.HostKeyCallback that checks keys against the file.
func (k *KnownHosts) Callback() ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		k.mu.Lock()
		cb := k.cb
		k.mu.Unlock()
		err := cb(hostname, remote, key)
		var ke *knownhosts.KeyError
		if errors.As(err, &ke) {
			fp := ssh.FingerprintSHA256(key)
			if len(ke.Want) == 0 {
				return &UnknownHostError{Host: hostname, Key: key, Fingerprint: fp}
			}
			return &HostKeyChangedError{Host: hostname, Key: key, Fingerprint: fp, Line: ke.Want[0].Line, File: ke.Want[0].Filename}
		}
		return err
	}
}

// Contents is the file as it is.
func (k *KnownHosts) Contents() ([]byte, error) { return os.ReadFile(k.path) }

// Merge adds the lines of data, another known_hosts file, that this one
// does not have, and returns how many. Comments, blank lines and lines
// that do not parse are left out, so the file stays readable.
func (k *KnownHosts) Merge(data []byte) (int, error) {
	cur, err := os.ReadFile(k.path)
	if err != nil {
		return 0, err
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(cur), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	var add strings.Builder
	n := 0
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || have[l] {
			continue
		}
		if _, _, _, _, _, err := ssh.ParseKnownHosts([]byte(l)); err != nil {
			continue
		}
		have[l] = true
		add.WriteString(l + "\n")
		n++
	}
	if n == 0 {
		return 0, nil
	}
	f, err := os.OpenFile(k.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	prefix := ""
	if len(cur) > 0 && !strings.HasSuffix(string(cur), "\n") {
		prefix = "\n"
	}
	if _, err := f.WriteString(prefix + add.String()); err != nil {
		f.Close()
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	return n, k.reload()
}

// Add trusts key for host (host:port as dialed).
func (k *KnownHosts) Add(host string, key ssh.PublicKey) error {
	line := knownhosts.Line([]string{knownhosts.Normalize(host)}, key)
	f, err := os.OpenFile(k.path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return k.reload()
}
