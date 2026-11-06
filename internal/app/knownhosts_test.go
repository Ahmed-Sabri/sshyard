package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/Licoy/sshyard/locales"
	"golang.org/x/crypto/ssh"
)

// TestHostKeyChangedReview: a host whose key changed is refused until the
// user opens the review from the pane, compares the keys and trusts the
// new one, which then takes the old one's place.
func TestHostKeyChangedReview(t *testing.T) {
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "vps", "secret")
	old := sshtest.NewHostKey().PublicKey()
	a.known.Add(srv.Addr, old)
	a.Connect(h.ID, openAt{})
	c := a.focused()
	waitFor(t, tt, "refused", func() bool { return session.Classify(c.s.Status().Err) == session.KindHostChanged })
	tt.Frame()
	if a.changingKey || tt.HasText("Trust New Key and Connect") {
		t.Fatal("the review opened by itself")
	}
	m := locales.In("en")
	saved := m.HostKeySaved(old.Type(), ssh.FingerprintSHA256(old))
	offered := m.HostKeyOffered(srv.HostKey.PublicKey().Type(), ssh.FingerprintSHA256(srv.HostKey.PublicKey()))

	mustClick(t, tt, "Review Host Key")
	tt.Frame()
	for _, s := range []string{"Host key changed", saved, offered, "ssh-keygen -lf"} {
		if !tt.HasText(s) {
			t.Fatalf("review lacks %q: %q", s, tt.Texts())
		}
	}
	before, _ := os.ReadFile(a.known.Path())
	mustClick(t, tt, "Cancel")
	tt.Frame()
	if a.keyChange != nil || tt.HasText(offered) {
		t.Fatal("review still open after Cancel")
	}
	if after, _ := os.ReadFile(a.known.Path()); string(after) != string(before) {
		t.Fatal("Cancel changed known_hosts")
	}

	mustClick(t, tt, "Review Host Key")
	tt.Frame()
	mustClick(t, tt, "Trust New Key and Connect")
	waitFor(t, tt, "connected", func() bool { return c.s.Status().State == session.Connected })
	keys := a.known.For(srv.Addr)
	if len(keys) != 1 || keys[0].Fingerprint != ssh.FingerprintSHA256(srv.HostKey.PublicKey()) {
		t.Fatalf("known keys %+v", keys)
	}
}

// TestKnownHostsPage: the settings list the trusted keys with the hosts
// using them, filter them, and remove one; a host's menu forgets its key.
func TestKnownHostsPage(t *testing.T) {
	a, tt := newTestApp(t)
	web, err := a.Store().Add(assets.Host{Name: "web", Address: "web.lan", Port: 22, User: "root", Auth: assets.AuthAsk})
	if err != nil {
		t.Fatal(err)
	}
	db, err := a.Store().Add(assets.Host{Name: "db", Address: "db.lan", Port: 2222, User: "root", Auth: assets.AuthAsk})
	if err != nil {
		t.Fatal(err)
	}
	a.known.Add("web.lan:22", sshtest.NewHostKey().PublicKey())
	a.known.Add("db.lan:2222", sshtest.NewHostKey().PublicKey())
	a.openSettings(prefKnownHosts)
	tt.Frame()
	for _, s := range []string{"Known Hosts", "web.lan", "[db.lan]:2222", "Used by web", "Used by db"} {
		if !tt.HasText(s) {
			t.Fatalf("page lacks %q: %q", s, tt.Texts())
		}
	}
	if n := countExact(tt, "Remove This Key"); n != 2 {
		t.Fatalf("%d remove buttons", n)
	}

	mustClick(t, tt, "Search hosts")
	tt.Type("db.lan")
	tt.Frame()
	if tt.HasText("web.lan") || !tt.HasText("[db.lan]:2222") {
		t.Fatalf("filtered: %q", tt.Texts())
	}
	mustClick(t, tt, "Remove This Key")
	tt.Frame()
	if l := a.known.List(); len(l) != 1 || l[0].Hosts[0] != "web.lan" {
		t.Fatalf("after remove %+v", l)
	}
	if !tt.HasText("No matching hosts") {
		t.Fatalf("no empty filter note: %q", tt.Texts())
	}

	m := locales.In("en")
	forget := func(h assets.Host) menuEntry {
		for _, e := range a.hostEntries(m, h) {
			if e.Label == m.ForgetHostKey() {
				return e
			}
		}
		t.Fatal("no Forget Host Key in the host menu")
		return menuEntry{}
	}
	if !forget(db).Disabled {
		t.Error("Forget Host Key enabled for a host with no key")
	}
	e := forget(web)
	if e.Disabled {
		t.Fatal("Forget Host Key disabled for a known host")
	}
	e.Do()
	tt.Frame()
	if len(a.known.List()) != 0 || !tt.HasText("Forgot the host key of web") {
		t.Fatalf("forget: %+v; %q", a.known.List(), strings.Join(tt.Texts(), " | "))
	}
	tt.Frame()
	if !tt.HasText("No host keys are trusted yet") {
		t.Fatalf("empty page: %q", tt.Texts())
	}
}

// TestInstallKeyHostKeyChanged: installing a key on a host whose key
// changed shows both keys and goes on once the new one is trusted.
func TestInstallKeyHostKeyChanged(t *testing.T) {
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
	h, err := a.Store().Add(assets.Host{Name: "rebuilt", Address: host, Port: port, User: "demo", Auth: assets.AuthCredential, CredentialID: pwCred.ID})
	if err != nil {
		t.Fatal(err)
	}
	a.known.Add(srv.Addr, sshtest.NewHostKey().PublicKey())
	a.openInstall(h.ID)
	tt.Frame()
	mustClick(t, tt, "Install")
	waitFor(t, tt, "host key change shown", func() bool { return a.install.changed != nil })
	tt.Frame()
	if !tt.HasText(en.HostKeyChanged("rebuilt")) || a.install.res != nil {
		t.Fatalf("%q", tt.Texts())
	}
	mustClick(t, tt, en.TrustNewKeyAndConnect())
	waitFor(t, tt, "installed", func() bool { return a.install.res != nil })
	if r := a.install.res; !r.ok() || !r.added {
		t.Fatalf("result %+v", r)
	}
	if _, err := os.Stat(filepath.Join(root, ".ssh", "authorized_keys")); err != nil {
		t.Fatal(err)
	}
	if keys := a.known.For(srv.Addr); len(keys) != 1 || keys[0].Fingerprint != ssh.FingerprintSHA256(srv.HostKey.PublicKey()) {
		t.Fatalf("known keys %+v", keys)
	}
}
