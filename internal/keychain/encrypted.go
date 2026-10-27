package keychain

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/chacha20poly1305"
)

const MasterKey = "master/v1"
const fileMagic = "SSHYARD-SECRETS\x00"
const fileVersion byte = 1
const maxStoreSize = 64 << 20

// OpenOptions separates the per-app lifetime lock from an overridden data path.
type OpenOptions struct {
	Path     string
	LockPath string
	Scope    string
	System   Backend
}

type encryptedStore struct {
	path   string
	aad    []byte
	key    []byte
	sealed []byte
	lock   io.Closer
}

// Open acquires the lifetime lock before reading or creating the sole system key.
// A failure never replaces existing ciphertext or regenerates a missing master.
func Open(o OpenOptions) (_ *Keychain, err error) {
	if o.Path == "" || o.LockPath == "" || o.Scope == "" {
		return nil, &OpError{Op: "lock", Err: errors.New("missing persistent store options")}
	}
	lock, err := acquireLock(o.LockPath)
	if err != nil {
		return nil, &OpError{Op: "lock", Err: err}
	}
	b := &encryptedStore{path: o.Path, lock: lock, aad: append([]byte(fileMagic), fileVersion)}
	b.aad = append(b.aad, []byte(o.Scope)...)
	defer func() {
		if err != nil {
			err = errors.Join(err, b.close())
		}
	}()
	sealed, rerr := readEncrypted(o.Path)
	exists := !errors.Is(rerr, os.ErrNotExist)
	if rerr != nil && exists {
		return nil, &OpError{Op: "file-read", Err: rerr}
	}
	if o.System == nil {
		return nil, &OpError{Op: "master-read", Err: ErrUnavailable}
	}
	b.key, err = master(o.System, exists)
	if err != nil {
		return nil, err
	}
	if exists {
		b.sealed = sealed
		values, e := b.decrypt()
		wipe(values)
		if e != nil {
			return nil, &OpError{Op: "file-decrypt", Err: e}
		}
	} else if err = b.commit(map[string][]byte{}); err != nil {
		return nil, err
	}
	return New(b), nil
}

func master(system Backend, exists bool) ([]byte, error) {
	key, err := system.Get(MasterKey)
	if err == nil {
		if len(key) != chacha20poly1305.KeySize {
			clear(key)
			return nil, &OpError{Op: "master-read", Err: ErrMasterInvalid}
		}
		return key, nil
	}
	clear(key)
	if !errors.Is(err, ErrNotFound) {
		return nil, &OpError{Op: "master-read", Err: err}
	}
	if exists {
		return nil, &OpError{Op: "master-read", Err: ErrMasterMissing}
	}
	key = make([]byte, chacha20poly1305.KeySize)
	defer clear(key)
	if _, err := rand.Read(key); err != nil {
		return nil, &OpError{Op: "master-write", Err: err}
	}
	if err := system.Set(MasterKey, key); err != nil {
		return nil, &OpError{Op: "master-write", Err: err}
	}
	check, err := system.Get(MasterKey)
	if err != nil {
		clear(check)
		return nil, &OpError{Op: "master-verify", Err: err}
	}
	if !bytes.Equal(key, check) {
		clear(check)
		return nil, &OpError{Op: "master-verify", Err: ErrMasterInvalid}
	}
	return check, nil
}

func (b *encryptedStore) decrypt() (map[string][]byte, error) {
	if len(b.key) != chacha20poly1305.KeySize {
		return nil, ErrLocked
	}
	h := len(fileMagic) + 1
	if len(b.sealed) < h+chacha20poly1305.NonceSizeX+chacha20poly1305.Overhead || !bytes.HasPrefix(b.sealed, []byte(fileMagic)) {
		return nil, ErrCorrupt
	}
	if b.sealed[len(fileMagic)] != fileVersion {
		return nil, ErrVersion
	}
	aead, err := chacha20poly1305.NewX(b.key)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, b.sealed[h:h+chacha20poly1305.NonceSizeX], b.sealed[h+chacha20poly1305.NonceSizeX:], b.aad)
	if err != nil {
		return nil, ErrCorrupt
	}
	defer clear(plain)
	var values map[string][]byte
	if json.Unmarshal(plain, &values) != nil || values == nil {
		wipe(values)
		return nil, ErrCorrupt
	}
	return values, nil
}
func wipe(values map[string][]byte) {
	for key, value := range values {
		clear(value)
		delete(values, key)
	}
}
func (b *encryptedStore) Get(key string) ([]byte, error) {
	values, err := b.decrypt()
	if err != nil {
		return nil, err
	}
	defer wipe(values)
	value, ok := values[key]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), value...), nil
}
func (b *encryptedStore) Set(key string, value []byte) error {
	return b.apply([]Change{{Key: key, Value: value}})
}
func (b *encryptedStore) Delete(key string) error { return b.apply([]Change{{Key: key, Delete: true}}) }
func (b *encryptedStore) apply(changes []Change) error {
	values, err := b.decrypt()
	if err != nil {
		return err
	}
	defer wipe(values)
	for _, c := range changes {
		if c.Key == "" {
			return errors.New("keychain: empty secret key")
		}
		clear(values[c.Key])
		if c.Delete {
			delete(values, c.Key)
		} else {
			values[c.Key] = append([]byte(nil), c.Value...)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	return b.commit(values)
}
func (b *encryptedStore) commit(values map[string][]byte) error {
	plain, err := json.Marshal(values)
	if err != nil {
		return &OpError{Op: "file-write", Err: err}
	}
	defer clear(plain)
	if len(plain) > maxStoreSize-128 {
		return &OpError{Op: "file-write", Err: fmt.Errorf("encrypted store exceeds size limit")}
	}
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	if _, err = rand.Read(nonce); err != nil {
		return &OpError{Op: "file-write", Err: err}
	}
	aead, err := chacha20poly1305.NewX(b.key)
	if err != nil {
		return err
	}
	sealed := append(append([]byte(fileMagic), fileVersion), nonce...)
	sealed = aead.Seal(sealed, nonce, plain, b.aad)
	if err := writeEncrypted(b.path, sealed); err != nil {
		return &OpError{Op: "file-write", Err: err}
	}
	b.sealed = sealed
	return nil
}
func (b *encryptedStore) close() error {
	clear(b.key)
	b.key = nil
	b.sealed = nil
	if b.lock == nil {
		return nil
	}
	lock := b.lock
	b.lock = nil
	return lock.Close()
}
