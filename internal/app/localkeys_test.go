package app

import (
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/Licoy/sshyard/locales"
	"golang.org/x/crypto/ssh"
)

// homeKey writes ~/.ssh/<name> of a's home, encrypted with passphrase
// unless it is "", and returns its public half.
func homeKey(t *testing.T, a *App, name, passphrase string) ssh.PublicKey {
	t.Helper()
	priv, pub := newKey(t)
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
	dir := filepath.Join(a.homeDir(), ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return pub
}

func askHost(t *testing.T, a *App, srv *sshtest.Server, name string) assets.Host {
	t.Helper()
	host, port := splitAddr(t, srv.Addr)
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	h, err := a.Store().Add(assets.Host{Name: name, Address: host, Port: port, User: "demo", Auth: assets.AuthAsk})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// TestLocalKeyPassphrase: a host that asks for its password logs in with
// the key of this computer the server accepts, asking its passphrase,
// again when it is wrong; the passphrase is remembered only once it
// logged in, and the key is the one tried first next time.
func TestLocalKeyPassphrase(t *testing.T) {
	a, tt := newTestApp(t)
	pub := homeKey(t, a, "id_ed25519", "open sesame")
	id := session.KeyID(pub)
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.AuthorizedKey = pub
	h := askHost(t, a, srv, "keyed")
	a.Connect(h.ID, openAt{})
	c := a.focused()
	waitFor(t, tt, "passphrase prompt", func() bool { return a.prompting && a.prompt.localKey != nil })
	if a.prompt.localKey.ID != id || !tt.HasText("Unlock the key id_ed25519") || !tt.HasText("Skip this key") ||
		!tt.HasText("Remember passphrase (encrypted on this computer)") {
		t.Fatalf("prompt %+v: %q", a.prompt, tt.Texts())
	}
	a.prompt.value, a.prompt.remember = "wrong", true
	a.SubmitPrompt()
	waitForIn(t, tt, slowWait, "wrong passphrase", func() bool { return a.prompting && a.prompt.err != "" })
	if !strings.Contains(a.prompt.err, "Wrong passphrase") || !a.prompt.remember {
		t.Errorf("wrong passphrase: %q, remember %v", a.prompt.err, a.prompt.remember)
	}
	if a.kc.Has(keychain.LocalKeyPassphrase(id)) {
		t.Error("a wrong passphrase was remembered")
	}
	a.prompt.value = "open sesame"
	a.SubmitPrompt()
	waitForIn(t, tt, slowWait, "connected", func() bool { return c.s.Status().State == session.Connected })
	waitFor(t, tt, "remembered", func() bool { return a.lk.IsRemembered(id) })
	if b, err := a.kc.Get(keychain.LocalKeyPassphrase(id)); err != nil || string(b) != "open sesame" {
		t.Errorf("remembered %q, %v", b, err)
	}
	if a.lk.Last(h.ID) != id {
		t.Error("the key that logged in was not noted")
	}

	// Another run: the remembered passphrase serves without asking.
	a.mu.Lock()
	delete(a.typed, localKeyPrompt(id))
	a.mu.Unlock()
	h2 := askHost(t, a, srv, "again")
	a.Connect(h2.ID, openAt{})
	c2 := a.focused()
	waitForIn(t, tt, slowWait, "connected again", func() bool { return c2.s.Status().State == session.Connected })
	if a.prompting {
		t.Error("asked for a remembered passphrase")
	}

	// The settings list it, and forget it.
	a.openSettings(prefCredentials)
	tt.Frame()
	if !tt.HasText("Passphrase of id_ed25519 remembered") {
		t.Fatalf("settings: %q", tt.Texts())
	}
	mustClick(t, tt, "Forget passphrase")
	waitFor(t, tt, "forgotten", func() bool { return !a.lk.IsRemembered(id) })
	if a.kc.Has(keychain.LocalKeyPassphrase(id)) {
		t.Error("the forgotten passphrase is still stored")
	}
}

// TestLocalKeysThenPassword: keys the server refuses lead to the
// password prompt, which says how many were tried; a key skipped is not
// offered again, and with none left the password is asked.
func TestLocalKeysThenPassword(t *testing.T) {
	a, tt := newTestApp(t)
	homeKey(t, a, "id_ed25519", "")
	locked := homeKey(t, a, "id_rsa", "pw")
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	h := askHost(t, a, srv, "refused")
	a.Connect(h.ID, openAt{})
	c := a.focused()
	waitFor(t, tt, "password prompt", func() bool { return a.prompting })
	// Both keys were offered; the encrypted one, refused, never asked.
	if a.prompt.localKey != nil || a.prompt.err != "" || !tt.HasText("Tried 2 keys on this computer; the server accepted none.") {
		t.Fatalf("prompt %+v: %q", a.prompt, tt.Texts())
	}
	a.prompt.value = "secret"
	a.SubmitPrompt()
	waitFor(t, tt, "connected", func() bool { return c.s.Status().State == session.Connected })

	// The server accepts the encrypted key: skipping it asks for the
	// password, as no other key is left.
	srv.AuthorizedKey = locked
	h2 := askHost(t, a, srv, "skipped")
	a.Connect(h2.ID, openAt{})
	c2 := a.focused()
	waitFor(t, tt, "passphrase prompt", func() bool { return a.prompting && a.prompt.localKey != nil })
	mustClick(t, tt, "Skip this key")
	waitFor(t, tt, "password prompt", func() bool { return a.prompting && a.prompt.localKey == nil })
	if !a.skipKeys[session.KeyID(locked)] {
		t.Error("the key skipped is not noted")
	}
	a.prompt.value = "secret"
	a.SubmitPrompt()
	waitFor(t, tt, "connected", func() bool { return c2.s.Status().State == session.Connected })
}

// TestLocalKeysOff: with the setting off, a host asks for its password
// at once; MCP may use the keys only with its own switch on.
func TestLocalKeysOff(t *testing.T) {
	a, tt := newTestApp(t)
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.AuthorizedKey = homeKey(t, a, "id_ed25519", "")
	h := askHost(t, a, srv, "ask")
	if err := a.mcpLogin(h); !errors.Is(err, errMCPNeedsInput) {
		t.Errorf("MCP without its switch: %v", err)
	}
	s := a.Settings()
	s.LocalKeysMCP = true
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if err := a.mcpLogin(h); err != nil {
		t.Errorf("MCP with its switch: %v", err)
	}
	if cr, err := a.mcpCredentials(h); err != nil || len(cr.LocalKeys) != 1 || !cr.TryAgent {
		t.Errorf("MCP credentials: %d keys, %v", len(cr.LocalKeys), err)
	}

	s.LocalKeys = false
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if err := a.mcpLogin(h); !errors.Is(err, errMCPNeedsInput) {
		t.Errorf("MCP with local keys off: %v", err)
	}
	if got := a.authText(locales.In("en"), h); got != "Ask for password each time" {
		t.Errorf("label with the setting off: %q", got)
	}
	a.Connect(h.ID, openAt{})
	waitFor(t, tt, "password prompt", func() bool { return a.prompting })
	if a.prompt.note != "" || srv.Logins() != 0 {
		t.Errorf("keys tried with the setting off: %+v", a.prompt)
	}
}
