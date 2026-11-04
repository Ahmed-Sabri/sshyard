package assets

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestHostOwnedAuthenticationAndPreallocatedID(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, auth := range []AuthMethod{AuthPassword, AuthKey} {
		id := NewID()
		h, err := s.AddHost(Host{ID: id, Address: "example.com", User: "demo", Auth: auth, CredentialID: "unused", AskPassphrase: true})
		if err != nil {
			t.Fatal(err)
		}
		if h.ID != id || h.Auth != auth || h.CredentialID != "" || h.AskPassphrase != (auth == AuthKey) {
			t.Fatal(h)
		}
		if _, err := s.AddHost(h); !errors.Is(err, ErrExists) {
			t.Fatal("duplicate ID accepted", err)
		}
	}
	if _, err := s.AddHost(Host{ID: "../invalid", Address: "example.com", User: "demo"}); err == nil {
		t.Fatal("invalid ID accepted")
	}
}
