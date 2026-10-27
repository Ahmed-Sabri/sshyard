package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/keys"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// writeConfig lays out the acceptance config in home/.ssh: five Host
// blocks (Host *, a ProxyJump through an alias, a passphrase key, a
// *.corp pattern and a host whose key cannot be read) and an Include.
func writeConfig(t *testing.T, home string, bastion, db *sshtest.Server) keys.Key {
	t.Helper()
	dir := filepath.Join(home, ".ssh")
	os.MkdirAll(filepath.Join(dir, "conf.d"), 0o700)
	k, err := keys.Generate("import-test", []byte("pp"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "id_enc"), k.PEM, 0o600)
	os.WriteFile(filepath.Join(dir, "id_broken"), []byte("not a key"), 0o600)
	bh, bp := splitAddr(t, bastion.Addr)
	dh, dp := splitAddr(t, db.Addr)
	cfg := fmt.Sprintf(`# acceptance
Include conf.d/*
Host *
  User demo
  ServerAliveInterval 30
Host bastion
  HostName %s
  Port %d
Host db
  HostName %s
  Port %d
  ProxyJump bastion
  IdentityFile ~/.ssh/id_enc
  LocalForward 8080 localhost:80
Host *.corp
  User corp
Host broken
  HostName 10.0.0.9
  IdentityFile ~/.ssh/id_broken
`, bh, bp, dh, dp)
	os.WriteFile(filepath.Join(dir, "config"), []byte(cfg), 0o600)
	os.WriteFile(filepath.Join(dir, "conf.d", "web"), []byte("Host web\n  HostName 10.0.0.8\n  Port 2200\n"), 0o600)
	return k
}

func TestImportSSHConfig(t *testing.T) {
	bastion, db := startServer(t), startServer(t)
	bastion.DirectTCPIP = true
	a, tt := newTestApp(t)
	m := locales.In("en")
	k := writeConfig(t, a.p.HomeDir, bastion, db)
	db.Password = "" // the key is the only way in
	db.AuthorizedKey = k.Public
	kc := a.kc.Backend().(*keychain.Memory)

	// The welcome page offers the import.
	mustClick(t, tt, "Import from SSH Config")
	if a.imp == nil || !a.importing {
		t.Fatal("import sheet not open")
	}
	tt.Frame()
	for _, s := range []string{
		"4 to import · 1 skipped",
		"Skipped: a wildcard pattern, not one host",
		"Not imported: ServerAliveInterval, LocalForward",
		"via bastion",
		"Passphrase of id_enc",
		"Import (4)",
	} {
		if !tt.HasText(s) {
			t.Errorf("preview lacks %q: %q", s, tt.Texts())
		}
	}
	if !hasTextContaining(tt, "Key file not readable") {
		t.Error("the unreadable key is not flagged")
	}
	// The encrypted key's passphrase, typed into its field.
	key := a.imp.keys[filepath.Join(a.p.HomeDir, ".ssh", "id_enc")]
	if key == nil || !key.encrypted {
		t.Fatalf("encrypted key %+v", key)
	}
	key.pass = "wrong"
	if _, ok := a.Import(m); ok {
		t.Fatal("a wrong passphrase was accepted")
	}
	key.pass = "pp"
	if res, ok := a.Import(m); !ok || res.added != 4 || res.skipped != 1 {
		t.Fatalf("%+v: %s", res, a.imp.err)
	}
	a.importing = false
	waitFor(t, tt, "imported", func() bool { return a.imp == nil })
	if n := a.store.Len(); n != 4 {
		t.Fatalf("%d hosts", n)
	}
	byName := map[string]assets.Host{}
	for _, h := range a.store.List() {
		byName[h.Name] = h
	}
	b, d := byName["bastion"], byName["db"]
	if b.Auth != assets.AuthAsk || b.User != "demo" {
		t.Errorf("bastion %+v", b)
	}
	if len(d.JumpChain) != 1 || d.JumpChain[0] != b.ID || d.Auth != assets.AuthCredential {
		t.Fatalf("db %+v", d)
	}
	if w := byName["web"]; w.Address != "10.0.0.8" || w.Port != 2200 || w.User != "demo" {
		t.Errorf("web %+v", w)
	}
	if br := byName["broken"]; br.Auth != assets.AuthAsk {
		t.Errorf("broken %+v", br)
	}
	creds := a.store.Credentials()
	if len(creds) != 1 || creds[0].Name != "id_enc" {
		t.Fatalf("credentials %+v", creds)
	}
	kcKeys := strings.Join(kc.Keys(), " ")
	for _, kind := range []keychain.Kind{keychain.PrivateKey, keychain.Passphrase} {
		if !strings.Contains(kcKeys, keychain.Cred(creds[0].ID, kind)) {
			t.Errorf("keychain lacks %s: %s", kind, kcKeys)
		}
	}

	// The imported chain connects: bastion by password, db by the key.
	a.Connect(d.ID, openAt{})
	c := a.focused()
	waitFor(t, tt, "bastion password", func() bool { return a.prompting })
	a.prompt.value = "secret"
	a.SubmitPrompt()
	for range 2 {
		waitFor(t, tt, "host key", func() bool { return a.hostKey != nil })
		mustClick(t, tt, "Trust and Connect")
	}
	waitFor(t, tt, "connected through the imported chain", func() bool { return c.s.Status().State == session.Connected })
	if bastion.Tunnels() < 1 || db.Logins() != 1 {
		t.Errorf("tunnels %d, logins %d", bastion.Tunnels(), db.Logins())
	}

	// A second import finds every host again: skip keeps the count.
	a.OpenImport(m, "")
	tt.Frame()
	if !tt.HasText("0 to import · 5 skipped") || !tt.HasText("Same address as db") {
		t.Fatalf("re-import preview: %q", tt.Texts())
	}
	// Nothing to save: the button says so and is off; skipping all
	// changes nothing.
	if !tt.HasText("Import (0)") {
		t.Errorf("button: %q", tt.Texts())
	}
	if res, ok := a.Import(m); !ok || res.skipped != 5 || res.added+res.overwritten != 0 {
		t.Fatalf("skip all: %+v %v", res, ok)
	}
	if a.store.Len() != 4 || len(a.store.Credentials()) != 1 {
		t.Fatalf("%d hosts, %d credentials after skipping", a.store.Len(), len(a.store.Credentials()))
	}
	// Overwrite keeps the host; keep both adds one.
	a.OpenImport(m, "")
	actions := importActions(m)
	for _, r := range a.imp.rows {
		r.pick = r.e.Alias == "web" || r.e.Alias == "broken"
		switch r.e.Alias {
		case "web":
			r.action, r.name = actions[1], "web-renamed"
		case "broken":
			r.action = actions[2]
		}
	}
	if _, ok := a.Import(m); !ok {
		t.Fatal(a.imp.err)
	}
	if got, _ := a.store.Get(byName["web"].ID); got.Name != "web-renamed" {
		t.Errorf("overwrite: %+v", got)
	}
	if a.store.Len() != 5 {
		t.Errorf("keep both: %d hosts", a.store.Len())
	}
}

// TestImportJumpToNewHost: a ProxyJump naming no imported or saved
// host makes one.
func TestImportJumpToNewHost(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	dir := filepath.Join(a.p.HomeDir, ".ssh")
	os.MkdirAll(dir, 0o700)
	saved, _ := a.store.Add(assets.Host{Name: "edge", Address: "10.1.0.1", Port: 22, User: "ops", Auth: assets.AuthAsk})
	os.WriteFile(filepath.Join(dir, "config"), []byte("Host app\n  HostName 10.1.0.5\n  User me\n  ProxyJump ops@10.1.0.1,root@gw.example:2022\n"), 0o600)
	a.OpenImport(m, "")
	tt.Frame()
	if _, ok := a.Import(m); !ok {
		t.Fatal(a.imp.err)
	}
	var app, gw assets.Host
	for _, h := range a.store.List() {
		switch h.Address {
		case "10.1.0.5":
			app = h
		case "gw.example":
			gw = h
		}
	}
	if gw.ID == "" || gw.Port != 2022 || gw.User != "root" {
		t.Fatalf("new jump host %+v", gw)
	}
	if len(app.JumpChain) != 2 || app.JumpChain[0] != saved.ID || app.JumpChain[1] != gw.ID {
		t.Fatalf("chain %v (edge %s, gw %s)", app.JumpChain, saved.ID, gw.ID)
	}
}

func TestImportNoConfig(t *testing.T) {
	a, tt := newTestApp(t)
	a.OpenImport(locales.In("en"), "")
	tt.Frame()
	if !hasTextContaining(tt, "There is no SSH config at") {
		t.Errorf("%q", tt.Texts())
	}
}

func TestQuickConnect(t *testing.T) {
	srv := startServer(t)
	a, tt := newTestApp(t)
	m := locales.In("en")
	kc := a.kc.Backend().(*keychain.Memory)
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())

	// ⌘K (Ctrl+Shift+K) opens it, and so does the + menu.
	tt.Key(appMods, ui.KeyK)
	tt.Frame()
	if a.quick == nil {
		t.Fatal("shortcut did not open quick connect")
	}
	a.quick, a.quicking = nil, false
	tt.Frame()
	mustClick(t, tt, "Add")
	mustClick(t, tt, "Quick Connect…")
	tt.Frame()
	if a.quick == nil {
		t.Fatal("menu did not open quick connect")
	}
	// Errors stay in the sheet.
	a.quick.target = "ssh -i key demo@h"
	if a.QuickConnect(m) || a.quick.err != "-i is not supported here." {
		t.Errorf("option: %q", a.quick.err)
	}
	a.quick.target = "demo@" + srv.Addr
	a.quick.auth = a.quickChoices(m)[0].label
	a.quick.password = "secret"
	if !a.QuickConnect(m) {
		t.Fatal(a.quick.err)
	}
	c := a.focused()
	waitFor(t, tt, "connected", func() bool { return c != nil && c.s.Status().State == session.Connected })
	tt.Frame()
	if !tt.HasText("Temporary") || a.store.Len() != 0 || len(a.temps) != 1 {
		t.Fatalf("temp tab: %d hosts, %d temps, %q", a.store.Len(), len(a.temps), tt.Texts())
	}
	a.SplitFocused(layout.Right)
	second := a.focused()
	waitFor(t, tt, "split connected", func() bool { return second.s.Status().State == session.Connected })
	a.CloseSession(c.id)
	tt.Frame()
	if len(a.temps) != 1 {
		t.Fatal("temp dropped while a pane still used it")
	}
	a.CloseSession(second.id)
	tt.Frame()
	if len(a.temps) != 0 || len(a.typed) != 0 || a.store.Len() != 0 || len(kc.Keys()) != 0 {
		t.Fatalf("left behind: %d temps, %d typed, %d hosts, keychain %v", len(a.temps), len(a.typed), a.store.Len(), kc.Keys())
	}

	// Save as Host turns a temporary tab into a saved host.
	a.OpenQuickConnect()
	a.quick.target = "ssh -p " + strings.Split(srv.Addr, ":")[1] + " demo@127.0.0.1"
	a.quick.password = "secret"
	if !a.QuickConnect(m) {
		t.Fatal(a.quick.err)
	}
	c = a.focused()
	waitFor(t, tt, "connected again", func() bool { return c.s.Status().State == session.Connected })
	a.SaveTempAs(c.id)
	tt.Frame()
	if !tt.HasText("Save as Host") {
		t.Fatalf("%q", tt.Texts())
	}
	a.form.name = "kept"
	if !a.SaveForm(m) {
		t.Fatal(a.form.err)
	}
	if a.store.Len() != 1 || len(a.temps) != 0 || isTemp(c.host.ID) {
		t.Fatalf("promote: %d hosts, %d temps, pane on %s", a.store.Len(), len(a.temps), c.host.ID)
	}
	tt.Frame()
	if tt.HasText("Temporary") {
		t.Error("badge still shown")
	}
}

// hasTextContaining reports whether some text on screen contains s.
func hasTextContaining(tt *ui.Tester, s string) bool {
	for _, x := range tt.Texts() {
		if strings.Contains(x, s) {
			return true
		}
	}
	return false
}
