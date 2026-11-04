package keychain

import (
	"bytes"
	"errors"
	"testing"
)

func TestSealRoundTripAndBinding(t *testing.T) {
	o := options(t)
	k := openTest(t, o)
	plain := []byte("conversation with a secret: hunter2")
	sealed, err := k.Seal("ai-history/v1", plain, []byte("host/1"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("hunter2")) {
		t.Fatal("plain text in sealed data")
	}
	got, err := k.Unseal("ai-history/v1", sealed, []byte("host/1"))
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("unseal %q, %v", got, err)
	}
	if again, _ := k.Seal("ai-history/v1", plain, []byte("host/1")); bytes.Equal(again, sealed) {
		t.Fatal("nonce reused")
	}
	// Another aad, another purpose, a changed byte: none opens.
	for name, try := range map[string]func() error{
		"aad":     func() error { _, err := k.Unseal("ai-history/v1", sealed, []byte("host/2")); return err },
		"purpose": func() error { _, err := k.Unseal("other", sealed, []byte("host/1")); return err },
		"flipped": func() error {
			bad := append([]byte(nil), sealed...)
			bad[len(bad)-1] ^= 1
			_, err := k.Unseal("ai-history/v1", bad, []byte("host/1"))
			return err
		},
		"short": func() error { _, err := k.Unseal("ai-history/v1", sealed[:10], []byte("host/1")); return err },
	} {
		if err := try(); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The same master key, opened again, reads it; another scope does not.
	if err := k.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Unseal("ai-history/v1", sealed, []byte("host/1")); !errors.Is(err, ErrLocked) {
		t.Fatalf("closed store unsealed: %v", err)
	}
	k2, err := Open(o)
	if err != nil {
		t.Fatal(err)
	}
	defer k2.Close()
	if got, err := k2.Unseal("ai-history/v1", sealed, []byte("host/1")); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("reopened unseal %q, %v", got, err)
	}
	if _, err := New(NewMemory()).Seal("x", plain, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("memory backend sealed: %v", err)
	}
}
