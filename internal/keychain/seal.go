package keychain

import (
	"bytes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"

	"golang.org/x/crypto/chacha20poly1305"
)

// Data kept outside secrets.enc that must not reach the disk in plain
// text (as the AI conversations) is sealed under a key derived from the
// master key for its purpose. The master key never leaves the package.

const sealMagic = "SSHYARD-SEALED\x00"
const sealVersion byte = 1

// sealer is a backend that seals data under its master key.
type sealer interface {
	seal(purpose string, plain, aad []byte) ([]byte, error)
	unseal(purpose string, sealed, aad []byte) ([]byte, error)
}

// Seal encrypts plain for purpose, bound to aad, which Unseal must be
// given again. It fails with ErrLocked while the store is locked, and
// with ErrUnavailable on a backend without a master key.
func (k *Keychain) Seal(purpose string, plain, aad []byte) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return nil, &OpError{Op: "closed", Err: ErrLocked}
	}
	s, ok := k.b.(sealer)
	if !ok {
		return nil, ErrUnavailable
	}
	return s.seal(purpose, plain, aad)
}

// Unseal decrypts what Seal gave for purpose and aad; ErrCorrupt when it
// was not, or was changed since.
func (k *Keychain) Unseal(purpose string, sealed, aad []byte) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return nil, &OpError{Op: "closed", Err: ErrLocked}
	}
	s, ok := k.b.(sealer)
	if !ok {
		return nil, ErrUnavailable
	}
	return s.unseal(purpose, sealed, aad)
}

// derive is the cipher for purpose: XChaCha20-Poly1305 under a key HKDF
// derives from the master key.
func (b *encryptedStore) derive(purpose string) (cipher.AEAD, error) {
	if len(b.key) != chacha20poly1305.KeySize || purpose == "" {
		return nil, ErrLocked
	}
	key, err := hkdf.Key(sha256.New, b.key, nil, "sshyard/seal/"+purpose, chacha20poly1305.KeySize)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	return chacha20poly1305.NewX(key)
}

// sealAAD binds sealed data to its format, this store's scope, its
// purpose and the caller's aad.
func (b *encryptedStore) sealAAD(purpose string, aad []byte) []byte {
	out := append([]byte(sealMagic), sealVersion)
	out = append(out, b.aad...)
	out = append(out, 0)
	out = append(out, purpose...)
	out = append(out, 0)
	return append(out, aad...)
}

func (b *encryptedStore) seal(purpose string, plain, aad []byte) ([]byte, error) {
	aead, err := b.derive(purpose)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append(append([]byte(sealMagic), sealVersion), nonce...)
	return aead.Seal(out, nonce, plain, b.sealAAD(purpose, aad)), nil
}

func (b *encryptedStore) unseal(purpose string, sealed, aad []byte) ([]byte, error) {
	aead, err := b.derive(purpose)
	if err != nil {
		return nil, err
	}
	h := len(sealMagic) + 1
	if len(sealed) < h+chacha20poly1305.NonceSizeX+chacha20poly1305.Overhead || !bytes.HasPrefix(sealed, []byte(sealMagic)) {
		return nil, ErrCorrupt
	}
	if sealed[len(sealMagic)] != sealVersion {
		return nil, ErrVersion
	}
	plain, err := aead.Open(nil, sealed[h:h+chacha20poly1305.NonceSizeX], sealed[h+chacha20poly1305.NonceSizeX:], b.sealAAD(purpose, aad))
	if err != nil {
		return nil, ErrCorrupt
	}
	return plain, nil
}
