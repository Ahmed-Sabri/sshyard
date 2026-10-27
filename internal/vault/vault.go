// Package vault seals data with a password, for the backups that take
// SSHYard's data, secrets included, out of the keychain: the data is
// encrypted under a random vault key (XChaCha20-Poly1305), and the vault
// key is wrapped under a key derived from the password (Argon2id). The
// password itself is never stored.
//
// A sealed file is JSON: a plaintext header (the format, its version, when
// it was made and the wrapped keys) and the encrypted body, whose
// authentication covers the header too, so neither can be changed alone.
package vault

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

// Format names a sealed file in its header.
const Format = "sshyard-vault"

// Version is the version of the format this package writes and reads.
const Version = 1

var (
	// ErrFormat reports data that is not a sealed file.
	ErrFormat = errors.New("vault: not an SSHYard backup")
	// ErrVersion reports a file of a newer format than this app reads.
	ErrVersion = errors.New("vault: made by a newer version of SSHYard")
	// ErrPassword reports a password that does not open the file (or a
	// file whose wrapped key was damaged; the two can not be told apart).
	ErrPassword = errors.New("vault: wrong password")
	// ErrCorrupt reports a file whose body was damaged or changed.
	ErrCorrupt = errors.New("vault: the file is damaged")
)

// Params are the cost of Argon2id: passes, memory in KiB, and lanes.
type Params struct {
	Time    uint32 `json:"t"`
	Memory  uint32 `json:"m"`
	Threads uint8  `json:"p"`
}

// DefaultParams are what Seal uses in the app: 3 passes over 64 MiB on 4
// lanes: well under a second on a current machine, and costly to guess
// against.
var DefaultParams = Params{Time: 3, Memory: 64 << 10, Threads: 4}

// The bounds of what Open derives with, so that a crafted header can not
// make it allocate gigabytes or run for minutes.
const (
	maxTime   = 16
	maxMemory = 1 << 20 // 1 GiB
	saltSize  = 16
)

func (p Params) ok() bool {
	return p.Time >= 1 && p.Time <= maxTime && p.Memory >= 8 && p.Memory <= maxMemory && p.Threads >= 1
}

// wrap is the vault key sealed under a key derived from a password.
type wrap struct {
	By     string `json:"by"` // "password"
	KDF    string `json:"kdf"`
	Params Params `json:"params"`
	Salt   []byte `json:"salt"`
	Nonce  []byte `json:"nonce"`
	Key    []byte `json:"key"`
}

type header struct {
	Format  string    `json:"format"`
	Version int       `json:"version"`
	Created time.Time `json:"created"`
	Wraps   []wrap    `json:"wraps"`
	Nonce   []byte    `json:"nonce"`
}

type file struct {
	Header json.RawMessage `json:"header"`
	Body   []byte          `json:"body"`
}

// wrapAAD binds a wrapped key to its use.
var wrapAAD = []byte(Format + " key")

func random(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported systems
	}
	return b
}

func derive(password []byte, salt []byte, p Params) []byte {
	return argon2.IDKey(password, salt, p.Time, p.Memory, p.Threads, chacha20poly1305.KeySize)
}

// Seal encrypts data under password, deriving with p.
func Seal(data, password []byte, p Params) ([]byte, error) {
	if len(password) == 0 {
		return nil, errors.New("vault: empty password")
	}
	if !p.ok() {
		return nil, fmt.Errorf("vault: bad parameters %+v", p)
	}
	key := random(chacha20poly1305.KeySize)
	w := wrap{By: "password", KDF: "argon2id", Params: p, Salt: random(saltSize), Nonce: random(chacha20poly1305.NonceSizeX)}
	kek, _ := chacha20poly1305.NewX(derive(password, w.Salt, p))
	w.Key = kek.Seal(nil, w.Nonce, key, wrapAAD)
	h := header{Format: Format, Version: Version, Created: time.Now().UTC().Truncate(time.Second), Wraps: []wrap{w}, Nonce: random(chacha20poly1305.NonceSizeX)}
	hb, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	aead, _ := chacha20poly1305.NewX(key)
	return json.Marshal(file{Header: hb, Body: aead.Seal(nil, h.Nonce, data, hb)})
}

// parse reads the header of a sealed file, and the exact bytes the body's
// authentication covers.
func parse(b []byte) (file, header, []byte, error) {
	var f file
	var h header
	if json.Unmarshal(b, &f) != nil || len(f.Header) == 0 {
		return f, h, nil, ErrFormat
	}
	// The header as it was sealed: compact, as json.Marshal wrote it.
	var aad bytes.Buffer
	if json.Compact(&aad, f.Header) != nil || json.Unmarshal(f.Header, &h) != nil || h.Format != Format {
		return f, h, nil, ErrFormat
	}
	if h.Version > Version {
		return f, h, nil, ErrVersion
	}
	if h.Version < 1 || len(h.Nonce) != chacha20poly1305.NonceSizeX {
		return f, h, nil, ErrFormat
	}
	return f, h, aad.Bytes(), nil
}

// Check reports whether b is a sealed file this package can open, without
// the password: ErrFormat, ErrVersion or nil.
func Check(b []byte) error {
	_, _, _, err := parse(b)
	return err
}

// Created is when the sealed file b was made.
func Created(b []byte) (time.Time, error) {
	_, h, _, err := parse(b)
	return h.Created, err
}

// Open decrypts a file Seal made, with its password.
func Open(b, password []byte) ([]byte, error) {
	f, h, aad, err := parse(b)
	if err != nil {
		return nil, err
	}
	var key []byte
	for _, w := range h.Wraps {
		if w.By != "password" || w.KDF != "argon2id" || !w.Params.ok() || len(w.Salt) < saltSize || len(w.Nonce) != chacha20poly1305.NonceSizeX {
			continue
		}
		kek, _ := chacha20poly1305.NewX(derive(password, w.Salt, w.Params))
		if k, err := kek.Open(nil, w.Nonce, w.Key, wrapAAD); err == nil && len(k) == chacha20poly1305.KeySize {
			key = k
			break
		}
	}
	if key == nil {
		return nil, ErrPassword
	}
	aead, _ := chacha20poly1305.NewX(key)
	data, err := aead.Open(nil, h.Nonce, f.Body, aad)
	if err != nil {
		return nil, ErrCorrupt
	}
	return data, nil
}
