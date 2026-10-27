package keychain

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSetGetDelete(t *testing.T) {
	m := NewMemory()
	k := New(m)
	if !k.Available() {
		t.Fatal("memory keychain unavailable")
	}
	if _, err := k.Get(Cred("h1", Password)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := k.Set(Cred("h1", Password), []byte("s3cret")); err != nil {
		t.Fatal(err)
	}
	if v, err := k.Get(Cred("h1", Password)); err != nil || string(v) != "s3cret" {
		t.Fatalf("got %q %v", v, err)
	}
	if !k.Has(Cred("h1", Password)) || k.Has(Cred("h1", PrivateKey)) {
		t.Error("Has")
	}
	if _, ok := m.m["cred/h1/password"]; !ok {
		t.Errorf("entry key: %v", m.m)
	}
	if err := k.Delete(Cred("h1", Password)); err != nil {
		t.Fatal(err)
	}
	if k.Has(Cred("h1", Password)) {
		t.Error("deleted secret remains")
	}
	if err := k.Delete(Cred("h1", Password)); err != nil {
		t.Errorf("deleting a missing secret: %v", err)
	}
}

func TestChunking(t *testing.T) {
	m := NewMemory()
	k := New(m)
	k.chunk = 100
	big := []byte(strings.Repeat("-----BEGIN OPENSSH PRIVATE KEY-----", 10)) // 350 bytes
	if err := k.Set(Cred("h", PrivateKey), big); err != nil {
		t.Fatal(err)
	}
	if m.Len() != 5 { // header + 4 chunks
		t.Errorf("%d entries", m.Len())
	}
	for key, v := range m.m {
		if len(v) > 100 {
			t.Errorf("%s has %d bytes", key, len(v))
		}
	}
	got, err := k.Get(Cred("h", PrivateKey))
	if err != nil || !bytes.Equal(got, big) {
		t.Fatalf("round trip %v", err)
	}
	// Shrinking to one entry removes the chunks.
	if err := k.Set(Cred("h", PrivateKey), []byte("small")); err != nil {
		t.Fatal(err)
	}
	if m.Len() != 1 {
		t.Errorf("stale chunks: %v", m.m)
	}
	// Growing again, then deleting, leaves nothing.
	k.Set(Cred("h", PrivateKey), big)
	k.Set(Cred("h", Password), []byte("pw"))
	if err := k.DeleteCred("h"); err != nil {
		t.Fatal(err)
	}
	if m.Len() != 0 {
		t.Errorf("left %v", m.m)
	}
	// A value that looks like a header is still stored safely.
	tricky := append(append([]byte{}, chunkMagic...), '3')
	k.Set(Cred("h", Password), tricky)
	if v, err := k.Get(Cred("h", Password)); err != nil || !bytes.Equal(v, tricky) {
		t.Errorf("tricky %q %v", v, err)
	}
}

func TestRealChunkSize(t *testing.T) {
	m := NewMemory()
	k := New(m)
	key := bytes.Repeat([]byte("k"), 6000)
	if err := k.Set(Cred("h", PrivateKey), key); err != nil {
		t.Fatal(err)
	}
	for name, v := range m.m {
		if len(v) > MaxChunk {
			t.Errorf("%s: %d > %d", name, len(v), MaxChunk)
		}
	}
	if v, _ := k.Get(Cred("h", PrivateKey)); !bytes.Equal(v, key) {
		t.Error("6000-byte key")
	}
}

func TestUnavailable(t *testing.T) {
	m := NewMemory()
	m.Fail = ErrUnavailable
	k := New(m)
	if k.Available() {
		t.Error("failing keychain available")
	}
	if err := k.Set(Cred("h", Password), []byte("x")); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Set: %v", err)
	}
}

func TestMapErr(t *testing.T) {
	if !errors.Is(mapErr(errors.ErrUnsupported), ErrUnavailable) {
		t.Error("ErrUnsupported not mapped")
	}
	if mapErr(nil) != nil {
		t.Error("nil")
	}
}

func TestKeys(t *testing.T) {
	if Cred("c1", PrivateKey) != "cred/c1/key" || ProxyPassword("p1") != "proxy/p1/password" || ForwardPassword("f1") != "forward/f1/password" {
		t.Fatal("keychain keys changed")
	}
	if AIKey("p1") != "ai/p1/apikey" || AIAgentEnv("codex", "API_KEY") != "ai/codex/env/API_KEY" {
		t.Fatal("AI keychain keys changed")
	}
}
