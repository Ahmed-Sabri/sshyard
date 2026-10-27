package keychain

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetGetDelete(t *testing.T) {
	m := NewMemory()
	k := New(m)
	if _, err := k.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := k.Set(Cred("h1", Password), []byte("secret")); err != nil {
		t.Fatal(err)
	}
	if value, err := k.Get(Cred("h1", Password)); err != nil || string(value) != "secret" {
		t.Fatalf("roundtrip: %q %v", value, err)
	}
	if !k.Has(Cred("h1", Password)) || k.Has(Cred("h1", PrivateKey)) {
		t.Fatal("Has")
	}
	if err := k.DeleteCred("h1"); err != nil {
		t.Fatal(err)
	}
	if err := k.Delete("missing"); err != nil {
		t.Fatal(err)
	}
	if m.Len() != 0 {
		t.Fatal("secret remains")
	}
	m.Fail = ErrUnavailable
	if err := k.Set("x", []byte("x")); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := k.Exists("x"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestSystemErrorMapping(t *testing.T) {
	if !errors.Is(mapErr(errors.ErrUnsupported), ErrUnavailable) || mapErr(nil) != nil {
		t.Fatal("unsupported mapping")
	}
	for _, code := range []int{-128, -25293, -25308, -25291, -99999} {
		source := fmt.Errorf("native failure (OSStatus %d)", code)
		err := mapErr(source)
		var se *SystemError
		if !errors.As(err, &se) || se.Code != code || !errors.Is(err, source) {
			t.Fatalf("lost system error: %v", err)
		}
		if errors.Is(err, ErrUnavailable) != (code == -25291) {
			t.Fatalf("unavailable: %d", code)
		}
	}
	for _, message := range []string{"failure (OSStatus -128) extra", "failure OSStatus -128", "failure (OSStatus nope)"} {
		var se *SystemError
		if errors.As(mapErr(errors.New(message)), &se) {
			t.Fatalf("accepted non-suffix: %s", message)
		}
	}
}

func TestKeys(t *testing.T) {
	if Cred("c1", PrivateKey) != "cred/c1/key" || ProxyPassword("p1") != "proxy/p1/password" || ForwardPassword("f1") != "forward/f1/password" || AIKey("p1") != "ai/p1/apikey" || AIAgentEnv("codex", "API_KEY") != "ai/codex/env/API_KEY" {
		t.Fatal("keys changed")
	}
}

func options(t *testing.T) OpenOptions {
	t.Helper()
	dir := t.TempDir()
	return OpenOptions{Path: filepath.Join(dir, "data", "secrets.enc"), LockPath: filepath.Join(dir, "app", "secrets.lock"), Scope: "test.sshyard", System: NewMemory()}
}
func openTest(t *testing.T, o OpenOptions) *Keychain {
	t.Helper()
	k, err := Open(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := k.Close(); err != nil {
			t.Error(err)
		}
	})
	return k
}
func mustGet(t *testing.T, k *Keychain, key string, want []byte) {
	t.Helper()
	got, err := k.Get(key)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("value mismatch for %s: %v", key, err)
	}
}
func TestEncryptedRoundTrip(t *testing.T) {
	o := options(t)
	k := openTest(t, o)
	secret := []byte(strings.Repeat("large-private-key-", 1000))
	key := Cred("c", PrivateKey)
	if err := k.Set(key, secret); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(o.Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(before, secret) || bytes.Contains(before, []byte(key)) {
		t.Fatal("plaintext on disk")
	}
	sys := o.System.(*Memory)
	if sys.Len() != 1 || sys.Keys()[0] != MasterKey {
		t.Fatal("business secret in system store")
	}
	master, _ := sys.Get(MasterKey)
	if len(master) != 32 {
		t.Fatal("master key size")
	}
	if err := k.Set(key, secret); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(o.Path)
	if bytes.Equal(before, after) {
		t.Fatal("nonce reused")
	}
	mustGet(t, k, key, secret)
	got, _ := k.Get(key)
	got[0] ^= 1
	mustGet(t, k, key, secret)
	if err := k.Close(); err != nil {
		t.Fatal(err)
	}
	if err := k.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Get(key); !errors.Is(err, ErrLocked) {
		t.Fatal(err)
	}
	if err := k.Set(key, secret); !errors.Is(err, ErrLocked) {
		t.Fatal(err)
	}
	reopened := openTest(t, o)
	mustGet(t, reopened, key, secret)
	if err := reopened.DeleteCred("c"); err != nil {
		t.Fatal(err)
	}
	if ok, err := reopened.Exists(key); ok || err != nil {
		t.Fatal("deleted key", err)
	}
}

func TestOpenRejectsLostOrInvalidMaster(t *testing.T) {
	for _, kind := range []string{"missing", "short", "wrong", "denied"} {
		t.Run(kind, func(t *testing.T) {
			o := options(t)
			k := openTest(t, o)
			if err := k.Set("secret", []byte("value")); err != nil {
				t.Fatal(err)
			}
			k.Close()
			before, _ := os.ReadFile(o.Path)
			sys := o.System.(*Memory)
			want := ErrMasterInvalid
			switch kind {
			case "missing":
				sys.Delete(MasterKey)
				want = ErrMasterMissing
			case "short":
				sys.Set(MasterKey, []byte("short"))
			case "wrong":
				sys.Set(MasterKey, bytes.Repeat([]byte{1}, 32))
				want = ErrCorrupt
			case "denied":
				sys.Fail = &SystemError{Code: -128, Err: errors.New("denied")}
				want = sys.Fail
			}
			got, err := Open(o)
			if got != nil || !errors.Is(err, want) {
				t.Fatalf("Open: %v %v", got, err)
			}
			after, _ := os.ReadFile(o.Path)
			if !bytes.Equal(before, after) {
				t.Fatal("existing store overwritten")
			}
		})
	}
}

func TestOpenRejectsCiphertextDamageAndScope(t *testing.T) {
	for _, kind := range []string{"truncated", "tampered", "version", "scope"} {
		t.Run(kind, func(t *testing.T) {
			o := options(t)
			k := openTest(t, o)
			k.Close()
			data, _ := os.ReadFile(o.Path)
			want := ErrCorrupt
			switch kind {
			case "truncated":
				data = data[:5]
			case "tampered":
				data[len(data)-1] ^= 1
			case "version":
				data[len(fileMagic)] = 2
				want = ErrVersion
			case "scope":
				o.Scope = "different.app"
			}
			if err := os.WriteFile(o.Path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if got, err := Open(o); got != nil || !errors.Is(err, want) {
				t.Fatalf("Open: %v %v", got, err)
			}
		})
	}
}
