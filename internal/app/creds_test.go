package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"github.com/egoist/mygo/ui"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sshtest"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func newKey(t *testing.T) (ed25519.PrivateKey, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return priv, sp
}

// TestSharedPassphraseAskedOnce: two hosts share a key credential whose
// passphrase is asked each run; one prompt serves both, and what is
// typed is never saved.
func TestSharedPassphraseAskedOnce(t *testing.T) {
	priv, pub := newKey(t)
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("open sesame"))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.AuthorizedKey = pub
	a, tt := newTestApp(t)
	cr, err := a.Store().SaveCredential(assets.Credential{Name: "deploy", Kind: assets.CredKey, User: "demo", AskPassphrase: true, Encrypted: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.kc.Set(keychain.Cred(cr.ID, keychain.PrivateKey), pem.EncodeToMemory(block)); err != nil {
		t.Fatal(err)
	}
	host, port := splitAddr(t, srv.Addr)
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	var hosts []assets.Host
	for _, n := range []string{"one", "two"} {
		// The credential's user logs in, not the host's.
		h, err := a.Store().Add(assets.Host{Name: n, Address: host, Port: port, User: "nobody", Auth: assets.AuthCredential, CredentialID: cr.ID})
		if err != nil {
			t.Fatal(err)
		}
		hosts = append(hosts, h)
		a.Connect(h.ID, openAt{})
	}
	waitFor(t, tt, "both failed", func() bool {
		for _, c := range a.conns {
			if c.s.Status().State != session.Failed {
				return false
			}
		}
		return true
	})
	waitFor(t, tt, "passphrase prompt", func() bool { return a.prompting })
	if !a.prompt.passphrase || !tt.HasText("Kept in memory for this session only, never saved.") {
		t.Fatalf("prompt %+v: %q", a.prompt, tt.Texts())
	}
	a.prompt.value = "wrong"
	a.SubmitPrompt()
	// Each try derives the key from the passphrase: slow on purpose.
	waitForIn(t, tt, slowWait, "wrong passphrase", func() bool { return a.prompting && a.prompt.err != "" })
	a.prompt.value = "open sesame"
	a.SubmitPrompt()
	waitForIn(t, tt, slowWait, "both connected", func() bool {
		for _, c := range a.conns {
			if c.s.Status().State != session.Connected {
				return false
			}
		}
		return true
	})
	if a.prompting {
		t.Error("asked again for the second host")
	}
	if a.kc.Has(keychain.Cred(cr.ID, keychain.Passphrase)) {
		t.Error("the passphrase asked each run was saved")
	}
}

// TestAgentAuth: a host logging in with the SSH agent signs with its keys
// and is never prompted; an empty agent says so.
func TestAgentAuth(t *testing.T) {
	priv, pub := newKey(t)
	kr := agent.NewKeyring()
	old := session.DialAgent
	t.Cleanup(func() { session.DialAgent = old })
	session.DialAgent = func() (io.ReadWriteCloser, error) {
		c1, c2 := net.Pipe()
		go agent.ServeAgent(kr, c2)
		return c1, nil
	}
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.AuthorizedKey = pub
	a, tt := newTestApp(t)
	host, port := splitAddr(t, srv.Addr)
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	h, err := a.Store().Add(assets.Host{Name: "agent", Address: host, Port: port, User: "demo", Auth: assets.AuthAgent})
	if err != nil {
		t.Fatal(err)
	}
	a.Connect(h.ID, openAt{})
	c := a.focused()
	// The state turns Failed before a frame shows why: wait for the text.
	waitFor(t, tt, "empty agent", func() bool {
		return c.s.Status().State == session.Failed && tt.HasText("The SSH agent has no keys. Add one with ssh-add.")
	})
	if a.prompting {
		t.Fatalf("prompting for an agent host: %q", tt.Texts())
	}
	if err := kr.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatal(err)
	}
	c.s.Reconnect()
	waitFor(t, tt, "connected", func() bool { return c.s.Status().State == session.Connected })
}

// TestOldDataDiscarded: a data file of the old schema is wiped (no
// migration); unrelated system entries are not touched during construction.
func TestOldDataDiscarded(t *testing.T) {
	dir := t.TempDir()
	old := `{"version":1,"hosts":[{"id":"h1","name":"old","address":"x","port":22,"user":"u","auth":"password"}]}`
	if err := os.WriteFile(filepath.Join(dir, "hosts.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	mem := keychain.NewMemory()
	kc := keychain.New(mem)
	kc.Set("host/h1/password", []byte("pw"))
	kc.Set("cred/keep/password", []byte("pw"))
	a, err := New(Platform{DataDir: dir, Secrets: mem, HomeDir: dir, CacheDir: filepath.Join(dir, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.Start()
	tt := ui.NewTester(a.View, 1200, 760)
	waitFor(t, tt, "metadata ready", a.ready)
	if a.Store().Len() != 0 {
		t.Errorf("%d hosts kept from the old file", a.Store().Len())
	}
	if !kc.Has("host/h1/password") || !kc.Has("cred/keep/password") {
		t.Errorf("keychain after the wipe: %q", mem.Keys())
	}
}

// TestDeleteHostInUse: a host that another jumps through is not deleted,
// and the toast names who uses it.
func TestDeleteHostInUse(t *testing.T) {
	a, tt := newTestApp(t)
	bastion, err := a.Store().Add(assets.Host{Name: "bastion", Address: "b.example", User: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store().Add(assets.Host{Name: "db", Address: "10.0.0.9", User: "root", JumpChain: []string{bastion.ID}}); err != nil {
		t.Fatal(err)
	}
	a.deleteID, a.deleting = bastion.ID, true
	tt.Frame()
	if !tt.HasText("Delete bastion? Its credential is kept for other hosts.") {
		t.Fatalf("confirm: %q", tt.Texts())
	}
	mustClick(t, tt, "Delete")
	tt.Frame()
	if _, ok := a.Store().Get(bastion.ID); !ok {
		t.Fatal("deleted a host in use")
	}
	if !tt.HasText("Still in use by db.") {
		t.Errorf("no in-use toast: %q", tt.Texts())
	}
}
