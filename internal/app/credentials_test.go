package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/pem"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/keys"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

var en = locales.In("en")

// keyServer is a server whose ~/.ssh/authorized_keys (under its SFTP
// root) lets keys log in, and that takes password "pw" too.
func keyServer(t *testing.T) (*sshtest.Server, string) {
	t.Helper()
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "pw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	root := t.TempDir()
	srv.SFTPRoot = root
	srv.AuthorizedKeysFile = filepath.Join(root, ".ssh", "authorized_keys")
	return srv, root
}

func authorize(t *testing.T, srv *sshtest.Server, pubs ...ssh.PublicKey) {
	t.Helper()
	os.MkdirAll(filepath.Dir(srv.AuthorizedKeysFile), 0o700)
	var b []byte
	for _, p := range pubs {
		b = append(b, ssh.MarshalAuthorizedKey(p)...)
	}
	if err := os.WriteFile(srv.AuthorizedKeysFile, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func openSSHKey(t *testing.T, priv ed25519.PrivateKey, pass string) []byte {
	t.Helper()
	var blk *pem.Block
	var err error
	if pass == "" {
		blk, err = ssh.MarshalPrivateKey(priv, "t")
	} else {
		blk, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "t", []byte(pass))
	}
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(blk)
}

func addSrvHost(t *testing.T, a *App, srv *sshtest.Server, name string, auth assets.AuthMethod, cred string) assets.Host {
	t.Helper()
	host, port := splitAddr(t, srv.Addr)
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	h, err := a.Store().Add(assets.Host{Name: name, Address: host, Port: port, User: "demo", Auth: auth, CredentialID: cred})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func waitConnected(t *testing.T, a *App, tt *ui.Tester, ids ...int) {
	t.Helper()
	for _, id := range ids {
		c := a.conns[id]
		waitFor(t, tt, "connected", func() bool {
			st := c.s.Status()
			if st.State == session.Failed {
				t.Fatalf("connection failed: %v", st.Err)
			}
			return st.State == session.Connected
		})
	}
}

// noSecrets checks that nothing secret is anywhere in the data
// directory.
func noSecrets(t *testing.T, dir string, secrets ...string) {
	t.Helper()
	secrets = append(secrets, "BEGIN OPENSSH", "PRIVATE KEY", "PuTTY-User-Key")
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := os.ReadFile(p)
		for _, s := range secrets {
			if bytes.Contains(b, []byte(s)) {
				t.Errorf("%s holds %q", p, s)
			}
		}
		return nil
	})
}

// TestCredentialSheet: a password credential made in Settings keeps its
// password in the encrypted local credential store only; changing its kind to a pasted key drops
// the password.
func TestCredentialSheet(t *testing.T) {
	a, tt := newTestApp(t)
	a.openSettings(prefCredentials)
	tt.SetSize(1200, 2200)
	tt.Frame()
	if !tt.HasText("Credentials") {
		t.Fatalf("no credentials section: %q", tt.Texts())
	}
	mustClick(t, tt, "New Credential")
	tt.Frame()
	if !a.credEditing || !tt.HasText("Password") {
		t.Fatalf("sheet not open: %q", tt.Texts())
	}
	mustClick(t, tt, "Save")
	if a.credForm == nil || a.credForm.err == "" {
		t.Fatal("an empty credential saved")
	}
	a.credForm.name, a.credForm.password = "ops", "hunter2-secret"
	mustClick(t, tt, "Save")
	if a.credEditing || len(a.Store().Credentials()) != 1 {
		t.Fatalf("not saved: %+v", a.credForm)
	}
	cr := a.Store().Credentials()[0]
	if b, _ := a.kc.Get(keychain.Cred(cr.ID, keychain.Password)); string(b) != "hunter2-secret" {
		t.Fatalf("keychain holds %q", b)
	}
	tt.Frame()
	if !tt.HasText("ops") {
		t.Errorf("settings misses the credential: %q", tt.Texts())
	}
	// The name is unique.
	a.openCredForm(nil, nil)
	a.credForm.name, a.credForm.password = "ops", "x"
	tt.Frame()
	if a.SaveCredForm(en) || a.credForm.err != "That name is already in use." {
		t.Errorf("duplicate saved, err %q", a.credForm.err)
	}
	a.closeCredForm()
	// To a key, pasted.
	priv, pub := newKey(t)
	a.openCredForm(&cr, nil)
	tt.Frame()
	a.credForm.kind = 1
	tt.SetClipboard(string(openSSHKey(t, priv, "")))
	tt.Frame()
	mustClick(t, tt, "Paste")
	tt.Frame()
	if !tt.HasText("ssh-ed25519 · " + ssh.FingerprintSHA256(pub)) {
		t.Fatalf("pasted key not shown: %q", tt.Texts())
	}
	mustClick(t, tt, "Save")
	cr, _ = a.Store().Credential(cr.ID)
	if a.credEditing || cr.Kind != assets.CredKey || cr.Fingerprint != ssh.FingerprintSHA256(pub) || cr.Encrypted {
		t.Fatalf("credential %+v, form %+v", cr, a.credForm)
	}
	if a.kc.Has(keychain.Cred(cr.ID, keychain.Password)) || !a.kc.Has(keychain.Cred(cr.ID, keychain.PrivateKey)) {
		t.Error("keychain holds the wrong secrets")
	}
	noSecrets(t, a.p.DataDir, "hunter2-secret")
}

// TestCredentialErrors: what a bad key says.
func TestCredentialErrors(t *testing.T) {
	a, tt := newTestApp(t)
	priv, pub := newKey(t)
	a.openCredForm(nil, nil)
	f := a.credForm
	f.name, f.kind = "k", 1
	tt.Frame()
	for _, c := range []struct {
		data []byte
		want string
	}{
		{ssh.MarshalAuthorizedKey(pub), "This is a public key: load the private key instead."},
		{[]byte("hello"), "This is not a private key. OpenSSH, PEM and PuTTY .ppk keys are read."},
	} {
		f.setKey(c.data, "x")
		tt.Frame()
		if !tt.HasText(c.want) || a.SaveCredForm(en) {
			t.Errorf("%q: %q", c.want, tt.Texts())
		}
	}
	// An encrypted key: the passphrase field shows; a wrong one is said.
	f.setKey(openSSHKey(t, priv, "right"), "id_ed25519")
	tt.Frame()
	if !tt.HasText("Encrypted with a passphrase") || !tt.HasText("Key passphrase") {
		t.Fatalf("%q", tt.Texts())
	}
	f.passphrase = "wrong"
	if a.SaveCredForm(en) || f.err != "Wrong passphrase for this key." {
		t.Errorf("err %q", f.err)
	}
	f.passphrase = ""
	if a.SaveCredForm(en) || f.err != "This key is encrypted: enter its passphrase." {
		t.Errorf("err %q", f.err)
	}
	// Asked each run, it saves unread.
	f.askPass = true
	if !a.SaveCredForm(en) {
		t.Fatalf("err %q", f.err)
	}
	cr := a.Store().Credentials()[0]
	if !cr.Encrypted || !cr.AskPassphrase || a.kc.Has(keychain.Cred(cr.ID, keychain.Passphrase)) {
		t.Errorf("credential %+v", cr)
	}
}

// TestCredentialEditRotatesKey: two hosts share a key credential; when
// the credential takes a new key, both log in with it.
func TestCredentialEditRotatesKey(t *testing.T) {
	srv, _ := keyServer(t)
	a, tt := newTestApp(t)
	oldPriv, oldPub := newKey(t)
	newPriv, newPub := newKey(t)
	authorize(t, srv, oldPub)
	a.openCredForm(nil, nil)
	a.credForm.name, a.credForm.kind = "deploy", 1
	a.credForm.setKey(openSSHKey(t, oldPriv, ""), "old")
	if !a.SaveCredForm(en) {
		t.Fatal(a.credForm.err)
	}
	cr := a.Store().Credentials()[0]
	h1 := addSrvHost(t, a, srv, "one", assets.AuthCredential, cr.ID)
	h2 := addSrvHost(t, a, srv, "two", assets.AuthCredential, cr.ID)
	// The server takes only the new key now.
	authorize(t, srv, newPub)
	a.openCredForm(&cr, nil)
	tt.Frame()
	if !tt.HasText("Used by one, two") {
		t.Errorf("sheet misses its hosts: %q", tt.Texts())
	}
	// A new key, encrypted, its passphrase saved.
	a.credForm.setKey(openSSHKey(t, newPriv, "pp"), "new")
	a.credForm.passphrase = "pp"
	tt.Frame()
	mustClick(t, tt, "Save")
	if a.credEditing {
		t.Fatalf("%q: %q", a.credForm.err, tt.Texts())
	}
	var ids []int
	for _, h := range []assets.Host{h1, h2} {
		id, err := a.Connect(h.ID, openAt{})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	waitConnected(t, a, tt, ids...)
	if a.prompting {
		t.Error("prompted for the saved passphrase")
	}
	noSecrets(t, a.p.DataDir, "pp\"")
}

// TestNewCredentialFromHostEditor: the host editor's "New credential…"
// opens the credential sheet and comes back with it chosen.
func TestNewCredentialFromHostEditor(t *testing.T) {
	a, tt := newTestApp(t)
	a.openEditor(nil)
	tt.Frame()
	a.form.name, a.form.address, a.form.user = "web", "10.0.0.5", "root"
	f := a.form
	if !tt.HasText("Ask for password each time") {
		t.Fatal(tt.Texts())
	}
	mustClick(t, tt, "Ask for password each time")
	tt.Frame()
	mustClick(t, tt, en.NewCredentialChoice())
	tt.Frame()
	if !a.credEditing || a.editing || a.credForm.back != f {
		t.Fatalf("credential sheet not shown: editing %v cred %v", a.editing, a.credEditing)
	}
	a.credForm.name, a.credForm.password = "ops", "s3cret"
	mustClick(t, tt, "Save")
	tt.Frame()
	if !a.editing || a.form != f || f.auth != "ops · Password" {
		t.Fatalf("editor back: %v, auth %q", a.editing, f.auth)
	}
	mustClick(t, tt, "Save")
	if a.Store().Len() != 1 {
		t.Fatalf("host not saved: %q %q", f.err, tt.Texts())
	}
	h := a.Store().List()[0]
	if h.Auth != assets.AuthCredential || h.CredentialID != a.Store().Credentials()[0].ID {
		t.Errorf("host %+v", h)
	}
	// Cancel from the credential sheet returns to the editor too.
	a.openEditor(&h)
	tt.Frame()
	f = a.form
	a.openCredForm(nil, f)
	a.editing = false
	tt.Frame()
	mustClick(t, tt, "Cancel")
	tt.Frame()
	if !a.editing || a.form != f {
		t.Error("cancel lost the host editor")
	}
}

// TestDeleteCredential: a credential in use names its hosts and moves
// them before it goes; its secrets leave the encrypted local credential store.
func TestDeleteCredential(t *testing.T) {
	a, tt := newTestApp(t)
	cr := addCred(t, a, "ops", "pw")
	a.kc.Set(keychain.Cred(cr.ID, keychain.Passphrase), []byte("left over"))
	a.typed[keychain.Cred(cr.ID, keychain.Password)] = "typed"
	srv, _ := keyServer(t)
	h1 := addSrvHost(t, a, srv, "one", assets.AuthCredential, cr.ID)
	h2 := addSrvHost(t, a, srv, "two", assets.AuthCredential, cr.ID)
	if err := a.DeleteCredential(cr.ID, nil); err == nil {
		t.Fatal("a credential in use was deleted")
	}
	a.askDeleteCred(en, cr.ID)
	tt.Frame()
	if !tt.HasText("ops is used by one, two. Choose how they log in from now on, then delete it.") {
		t.Fatalf("%q", tt.Texts())
	}
	a.rebindChoice = en.AuthAgent()
	mustClick(t, tt, "Switch and Delete")
	tt.Frame()
	if _, ok := a.Store().Credential(cr.ID); ok {
		t.Fatal("not deleted")
	}
	for _, id := range []string{h1.ID, h2.ID} {
		if h, _ := a.Store().Get(id); h.Auth != assets.AuthAgent || h.CredentialID != "" {
			t.Errorf("host %+v not moved", h)
		}
	}
	for _, k := range []keychain.Kind{keychain.Password, keychain.PrivateKey, keychain.Passphrase} {
		if a.kc.Has(keychain.Cred(cr.ID, k)) {
			t.Errorf("%s left in the encrypted local credential store", k)
		}
	}
	if _, ok := a.typed[keychain.Cred(cr.ID, keychain.Password)]; ok {
		t.Error("typed password kept")
	}
	// Unused: a plain confirmation.
	cr2 := addCred(t, a, "spare", "pw2")
	a.askDeleteCred(en, cr2.ID)
	tt.Frame()
	if !tt.HasText("Delete spare? Its secret is removed from the encrypted local credential store.") {
		t.Fatalf("%q", tt.Texts())
	}
	mustClick(t, tt, "Delete")
	if _, ok := a.Store().Credential(cr2.ID); ok || a.kc.Has(keychain.Cred(cr2.ID, keychain.Password)) {
		t.Error("spare kept")
	}
}

// TestDeleteProxyForwardSecrets: a proxy's and a forward's passwords go
// with them.
func TestDeleteProxyForwardSecrets(t *testing.T) {
	a, _ := newTestApp(t)
	p, err := a.Store().SaveProxy(assets.Proxy{Name: "corp", Kind: assets.ProxySOCKS5, Address: "10.0.0.1", User: "u"})
	if err != nil {
		t.Fatal(err)
	}
	a.kc.Set(keychain.ProxyPassword(p.ID), []byte("proxy-pw"))
	h, err := a.Store().Add(assets.Host{Name: "h", Address: "10.0.0.2", User: "u"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := a.Store().SaveForward(assets.Forward{Name: "d", Kind: assets.ForwardDynamic, HostID: h.ID, ListenAddr: "127.0.0.1", ListenPort: 1080, User: "c"})
	if err != nil {
		t.Fatal(err)
	}
	a.kc.Set(keychain.ForwardPassword(f.ID), []byte("fwd-pw"))
	if err := a.DeleteProxy(p.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteForward(f.ID); err != nil {
		t.Fatal(err)
	}
	if a.kc.Has(keychain.ProxyPassword(p.ID)) || a.kc.Has(keychain.ForwardPassword(f.ID)) {
		t.Error("a password outlived its entry")
	}
}

// TestInstallKey: installing a key credential on a host that logs in
// with a password; the key alone then logs in, a second install adds
// nothing, and the host switches to the key. A host whose ~/.ssh others
// may write refuses the key, and stays as it was.
func TestInstallKey(t *testing.T) {
	srv, root := keyServer(t)
	a, tt := newTestApp(t)
	priv, pub := newKey(t)
	a.openCredForm(nil, nil)
	a.credForm.name, a.credForm.kind, a.credForm.user = "deploy", 1, "demo"
	a.credForm.setKey(openSSHKey(t, priv, ""), "id")
	if !a.SaveCredForm(en) {
		t.Fatal(a.credForm.err)
	}
	key := a.Store().Credentials()[0]
	pwCred := addCred(t, a, "pw", "pw")
	h := addSrvHost(t, a, srv, "box", assets.AuthCredential, pwCred.ID)
	a.openInstall(h.ID)
	tt.Frame()
	if !tt.HasText("Install Public Key") || !tt.HasText("deploy · Key") {
		t.Fatalf("%q", tt.Texts())
	}
	mustClick(t, tt, "Install")
	waitFor(t, tt, "installed", func() bool { return a.install.res != nil })
	if r := a.install.res; !r.ok() || !r.added {
		t.Fatalf("result %+v", r)
	}
	tt.Frame()
	for _, s := range []string{"The key was added to authorized_keys.", "A login with the key alone works.", "Log in to box with deploy from now on?"} {
		if !tt.HasText(s) {
			t.Errorf("missing %q: %q", s, tt.Texts())
		}
	}
	b, _ := os.ReadFile(filepath.Join(root, ".ssh", "authorized_keys"))
	if !strings.Contains(string(b), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))) {
		t.Errorf("authorized_keys: %s", b)
	}
	mustClick(t, tt, "Switch")
	if h2, _ := a.Store().Get(h.ID); h2.Auth != assets.AuthCredential || h2.CredentialID != key.ID {
		t.Errorf("host %+v", h2)
	}
	// Again: nothing added.
	h, _ = a.Store().Get(h.ID)
	if r := a.InstallKey(context.Background(), h, key.ID, "", ""); !r.ok() || r.added {
		t.Errorf("second install %+v", r)
	}
	b2, _ := os.ReadFile(filepath.Join(root, ".ssh", "authorized_keys"))
	if !bytes.Equal(b, b2) {
		t.Errorf("authorized_keys grew: %s", b2)
	}
	// StrictModes: a writable ~/.ssh is refused; the hints show and the
	// host is not switched. Windows has no Unix mode bits, and the test
	// server does not apply StrictModes there.
	if runtime.GOOS == "windows" {
		return
	}
	srv2, root2 := keyServer(t)
	os.MkdirAll(filepath.Join(root2, ".ssh"), 0o700)
	os.Chmod(filepath.Join(root2, ".ssh"), 0o777)
	h3 := addSrvHost(t, a, srv2, "loose", assets.AuthAsk, "")
	a.openInstall(h3.ID)
	tt.Frame()
	mustClick(t, tt, "Install")
	waitFor(t, tt, "password asked", func() bool { return a.install.needPassword })
	tt.Frame()
	a.install.password = "pw"
	mustClick(t, tt, "Install")
	waitFor(t, tt, "result", func() bool { return a.install.res != nil && !a.install.busy })
	if r := a.install.res; r.err != nil || r.verifyErr == nil {
		t.Fatalf("result %+v", r)
	}
	tt.Frame()
	if !tt.HasText(en.InstallHints()) || tt.HasText("Switch") {
		t.Errorf("%q", tt.Texts())
	}
	if h, _ := a.Store().Get(h3.ID); h.Auth != assets.AuthAsk {
		t.Errorf("host switched: %+v", h)
	}
}

// TestPPKLogin: a PuTTY key made by puttygen logs in once imported.
func TestPPKLogin(t *testing.T) {
	if _, err := exec.LookPath("puttygen"); err != nil {
		t.Skip("puttygen not installed")
	}
	srv, _ := keyServer(t)
	a, tt := newTestApp(t)
	for _, v := range []string{"2", "3"} {
		dir := t.TempDir()
		ppk := filepath.Join(dir, "k.ppk")
		pf := filepath.Join(dir, "pass")
		os.WriteFile(pf, []byte("ppk pass\n"), 0o600)
		out, err := exec.Command("puttygen", "-t", "ed25519", "-o", ppk, "--new-passphrase", pf, "--ppk-param", "version="+v).CombinedOutput()
		if err != nil {
			t.Fatalf("puttygen: %v %s", err, out)
		}
		data, _ := os.ReadFile(ppk)
		a.openCredForm(nil, nil)
		a.credForm.name, a.credForm.kind = "ppk"+v, 1
		a.credForm.setKey(data, "k.ppk")
		tt.Frame()
		if !tt.HasText("PuTTY key, kept in OpenSSH form") {
			t.Fatalf("%q", tt.Texts())
		}
		a.credForm.passphrase = "ppk pass"
		if !a.SaveCredForm(en) {
			t.Fatal(a.credForm.err)
		}
		cr, _ := a.Store().Credential(a.Store().Credentials()[len(a.Store().Credentials())-1].ID)
		pemKey, _ := a.kc.Get(keychain.Cred(cr.ID, keychain.PrivateKey))
		k, err := keys.Import(pemKey, []byte("ppk pass"))
		if err != nil {
			t.Fatal(err)
		}
		authorize(t, srv, k.Public)
		h := addSrvHost(t, a, srv, "ppk-host"+v, assets.AuthCredential, cr.ID)
		id, err := a.Connect(h.ID, openAt{})
		if err != nil {
			t.Fatal(err)
		}
		waitConnected(t, a, tt, id)
	}
}

// A host never connected to shows its key in the install sheet, and the
// install goes on once it is trusted.
func TestInstallKeyUnknownHost(t *testing.T) {
	srv, root := keyServer(t)
	a, tt := newTestApp(t)
	priv, _ := newKey(t)
	a.openCredForm(nil, nil)
	a.credForm.name, a.credForm.kind, a.credForm.user = "deploy", 1, "demo"
	a.credForm.setKey(openSSHKey(t, priv, ""), "id")
	if !a.SaveCredForm(en) {
		t.Fatal(a.credForm.err)
	}
	pwCred := addCred(t, a, "pw", "pw")
	host, port := splitAddr(t, srv.Addr)
	h, err := a.Store().Add(assets.Host{Name: "fresh", Address: host, Port: port, User: "demo", Auth: assets.AuthCredential, CredentialID: pwCred.ID})
	if err != nil {
		t.Fatal(err)
	}
	a.openInstall(h.ID)
	tt.Frame()
	mustClick(t, tt, "Install")
	waitFor(t, tt, "host key asked", func() bool { return a.install.unknown != nil })
	tt.Frame()
	if !tt.HasText(en.TrustAndConnect()) || a.install.res != nil {
		t.Fatalf("%q", tt.Texts())
	}
	mustClick(t, tt, en.TrustAndConnect())
	waitFor(t, tt, "installed", func() bool { return a.install.res != nil })
	if r := a.install.res; !r.ok() || !r.added {
		t.Fatalf("result %+v", r)
	}
	if _, err := os.Stat(filepath.Join(root, ".ssh", "authorized_keys")); err != nil {
		t.Fatal(err)
	}
}

// TestInstallKeyEffectiveUser: the key is installed and checked for the
// account the host's login reaches (its credential's user name over the
// host's), and a key credential with a user name of its own elsewhere is
// not offered as a switch.
func TestInstallKeyEffectiveUser(t *testing.T) {
	srv, root := keyServer(t) // takes "demo" only
	a, tt := newTestApp(t)
	pwCred, err := a.Store().SaveCredential(assets.Credential{Name: "pw", Kind: assets.CredPassword, User: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	a.kc.Set(keychain.Cred(pwCred.ID, keychain.Password), []byte("pw"))
	h := addSrvHost(t, a, srv, "box", assets.AuthCredential, pwCred.ID)
	h.User = "nobody"
	if h, err = a.Store().Update(h); err != nil {
		t.Fatal(err)
	}
	if u := a.loginUser(h); u != "demo" {
		t.Fatalf("login user %q", u)
	}
	priv, pub := newKey(t)
	a.openCredForm(nil, nil)
	a.credForm.name, a.credForm.kind = "plain", 1
	a.credForm.setKey(openSSHKey(t, priv, ""), "id")
	if !a.SaveCredForm(en) {
		t.Fatal(a.credForm.err)
	}
	var plain assets.Credential
	for _, cr := range a.Store().Credentials() {
		if cr.Name == "plain" {
			plain = cr
		}
	}
	// The key credential has no user name: after a switch the host would
	// log in as its own user, nobody, not demo the key went to. So no
	// switch, and the reason.
	a.openInstall(h.ID)
	a.install.cred = credLabel(en, plain)
	tt.Frame()
	mustClick(t, tt, "Install")
	waitFor(t, tt, "installed", func() bool { return a.install.res != nil && !a.install.busy })
	r := a.install.res
	if !r.ok() || !r.added || r.user != "demo" || r.switchUser != "nobody" || r.keyUser != "" || r.hostUser != "nobody" || r.canSwitch() {
		t.Fatalf("install %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".ssh", "authorized_keys")); !strings.Contains(string(b), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))) {
		t.Fatalf("authorized_keys: %s", b)
	}
	tt.Frame()
	if !tt.HasText(en.InstallOtherHostUser("demo", "nobody")) || tt.HasText(en.SwitchAuth()) {
		t.Errorf("no-user key: %q", tt.Texts())
	}
	if err := a.SwitchToKey(h.ID, plain.ID, "demo"); err == nil {
		t.Error("switched to a login as nobody")
	}
	if got, _ := a.Store().Get(h.ID); got.CredentialID != pwCred.ID {
		t.Errorf("host changed: %+v", got)
	}
	// With the host's own user demo too, the switch is offered, and the
	// host then logs in with the key.
	h.User = "demo"
	if h, err = a.Store().Update(h); err != nil {
		t.Fatal(err)
	}
	a.openInstall(h.ID)
	a.install.cred = credLabel(en, plain)
	tt.Frame()
	mustClick(t, tt, "Install")
	waitFor(t, tt, "installed again", func() bool { return a.install.res != nil && !a.install.busy })
	if r := a.install.res; !r.ok() || r.added || !r.canSwitch() {
		t.Fatalf("same user %+v", r)
	}
	tt.Frame()
	mustClick(t, tt, en.SwitchAuth())
	if got, _ := a.Store().Get(h.ID); got.CredentialID != plain.ID || a.loginUser(got) != "demo" {
		t.Fatalf("not switched: %+v", got)
	}
	a.Connect(h.ID, openAt{})
	kc := a.focused()
	waitFor(t, tt, "key login", func() bool { return kc.s.Status().State == session.Connected })
	a.CloseSession(kc.id)
	h.User = "nobody"
	h.Auth, h.CredentialID = assets.AuthCredential, pwCred.ID
	if h, err = a.Store().Update(h); err != nil {
		t.Fatal(err)
	}
	// A key credential that logs in as someone else: installed and
	// checked for demo, but no switch.
	plain.User = "root"
	if _, err := a.Store().SaveCredential(plain); err != nil {
		t.Fatal(err)
	}
	a.openInstall(h.ID)
	a.install.cred = credLabel(en, plain)
	tt.Frame()
	mustClick(t, tt, "Install")
	waitFor(t, tt, "installed", func() bool { return a.install.res != nil && !a.install.busy })
	if r := a.install.res; !r.ok() || r.user != "demo" || r.keyUser != "root" {
		t.Fatalf("result %+v", r)
	}
	tt.Frame()
	if !tt.HasText(en.InstallOtherUser("demo", "root")) || tt.HasText("Switch") {
		t.Errorf("%q", tt.Texts())
	}
}
