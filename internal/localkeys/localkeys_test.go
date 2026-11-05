package localkeys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/Licoy/sshyard/internal/session"
	"golang.org/x/crypto/ssh"
)

func writeKey(t *testing.T, path, passphrase string) ssh.PublicKey {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var block *pem.Block
	var err error
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	sp, _ := ssh.NewPublicKey(pub)
	return sp
}

func ids(keys []session.LocalKey) []string {
	var out []string
	for _, k := range keys {
		out = append(out, session.KeyID(k.Public))
	}
	return out
}

// TestFind: the default key files when the config names none for the
// host; else only those it names, by the host's alias or its address,
// public halves of encrypted keys and agent-only keys included.
func TestFind(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".ssh")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ed := writeKey(t, filepath.Join(dir, "id_ed25519"), "")
	rsa := writeKey(t, filepath.Join(dir, "id_rsa"), "pw")
	work := writeKey(t, filepath.Join(dir, "work"), "")
	// An agent-only key: its public half alone, named by the config.
	agentOnly := writeKey(t, filepath.Join(dir, "vault"), "")
	if err := os.WriteFile(filepath.Join(dir, "vault.pub"), ssh.MarshalAuthorizedKey(agentOnly), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, "vault"))
	cfg := "Host prod\n  HostName 10.0.0.5\n  IdentityFile ~/.ssh/work\n  IdentityFile ~/.ssh/vault\n  IdentitiesOnly yes\n"
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	f := Find(home, "me", "other", "10.0.0.9")
	got := ids(f.Keys)
	if f.IdentitiesOnly || len(got) != 2 || got[0] != session.KeyID(ed) || got[1] != session.KeyID(rsa) {
		t.Fatalf("defaults: %d keys, IdentitiesOnly %v", len(got), f.IdentitiesOnly)
	}
	if !f.Keys[1].Encrypted || f.Keys[0].Encrypted || f.Keys[0].Explicit {
		t.Errorf("defaults: %+v", f.Keys)
	}

	for _, name := range []string{"prod", "anything"} {
		addr := "10.0.0.5"
		if name == "anything" {
			addr = "prod" // the alias as the address
		}
		f = Find(home, "me", name, addr)
		got = ids(f.Keys)
		if !f.IdentitiesOnly || len(got) != 2 || got[0] != session.KeyID(work) || got[1] != session.KeyID(agentOnly) {
			t.Fatalf("%s: %d keys, IdentitiesOnly %v", name, len(got), f.IdentitiesOnly)
		}
		if !f.Keys[0].Explicit || f.Keys[1].PEM != nil {
			t.Errorf("%s: %+v", name, f.Keys)
		}
	}

	if f := Find(t.TempDir(), "me", "x", "x"); len(f.Keys) != 0 {
		t.Errorf("no ~/.ssh: %d keys", len(f.Keys))
	}
}

// TestState: what is kept survives a reopen, and is written only when it
// changes.
func TestState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localkeys.json")
	s := Open(path)
	if err := s.SetLast("h1", "k1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remember(Remembered{ID: "k1", Path: "/k", Fingerprint: "SHA256:x"}); err != nil {
		t.Fatal(err)
	}
	s = Open(path)
	if s.Last("h1") != "k1" || !s.IsRemembered("k1") || len(s.Remembered()) != 1 {
		t.Fatalf("reopened: %+v", s.d)
	}
	if err := s.Forget("k1"); err != nil {
		t.Fatal(err)
	}
	if err := s.ForgetHost("h1"); err != nil {
		t.Fatal(err)
	}
	s = Open(path)
	if s.Last("h1") != "" || s.IsRemembered("k1") {
		t.Errorf("after forgetting: %+v", s.d)
	}
}
