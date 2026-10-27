package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/fonts"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
	"net"
	"strconv"
)

func newTestApp(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	dir := t.TempDir()
	a, err := New(Platform{DataDir: dir, Secrets: keychain.NewMemory(), HomeDir: dir, CacheDir: filepath.Join(dir, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	a.Start()
	tt := ui.NewTester(a.View, 1200, 760)
	tt.SetLocale("en")
	tt.Frame()
	return a, tt
}

// waitFor runs frames until cond holds.
func waitFor(t *testing.T, tt *ui.Tester, what string, cond func() bool) {
	t.Helper()
	waitForIn(t, tt, 5*time.Second, what, cond)
}

// slowWait is how long waitForIn gives work that is slow by design, such
// as a key's passphrase derivation (over a second under -race), so that
// a loaded machine does not fail the test.
const slowWait = time.Minute

// waitForIn runs frames until cond holds, for up to d.
func waitForIn(t *testing.T, tt *ui.Tester, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; texts %q", what, tt.Texts())
		}
		time.Sleep(10 * time.Millisecond)
		tt.Frame()
	}
}

func mustClick(t *testing.T, tt *ui.Tester, s string) {
	t.Helper()
	if err := tt.Click(s); err != nil {
		t.Fatalf("click %q: %v; texts %q", s, err, tt.Texts())
	}
}

func TestWelcome(t *testing.T) {
	_, tt := newTestApp(t)
	for _, s := range []string{"SSHYard", "No hosts yet", "Welcome to SSHYard"} {
		if !tt.HasText(s) {
			t.Errorf("missing %q in %q", s, tt.Texts())
		}
	}
}

func TestAddHostFlow(t *testing.T) {
	a, tt := newTestApp(t)
	a.openEditor(nil)
	tt.Frame()
	mustClick(t, tt, "Save")
	if a.Store().Len() != 0 || !a.editing {
		t.Fatal("empty form saved")
	}
	cr := addCred(t, a, "ops", "pw")
	tt.Frame()
	a.form.name, a.form.address, a.form.user = "web", "10.0.0.5", "root"
	// The auth select offers asking, the agent and the credential.
	for _, s := range []string{"Ask for password each time"} {
		if !tt.HasText(s) {
			t.Errorf("auth select misses %q: %q", s, tt.Texts())
		}
	}
	if a.form.auth != "Ask for password each time" || !a.form.cwdFollow {
		t.Errorf("new host defaults: auth %q, cwd follow %v", a.form.auth, a.form.cwdFollow)
	}
	a.form.auth = "ops · Password"
	mustClick(t, tt, "Save")
	if a.editing || a.Store().Len() != 1 {
		t.Fatalf("host not saved: editing %v, %d hosts; err %q", a.editing, a.Store().Len(), a.form.err)
	}
	h := a.Store().List()[0]
	if h.Auth != assets.AuthCredential || h.CredentialID != cr.ID || !h.CwdFollow {
		t.Errorf("saved host %+v", h)
	}
	// No secret is in the data file.
	if b, _ := os.ReadFile(filepath.Join(a.p.DataDir, "hosts.json")); strings.Contains(string(b), "pw\"") {
		t.Errorf("secret in hosts.json: %s", b)
	}
	a.openEditor(&h)
	tt.Frame()
	if a.form.auth != "ops · Password" {
		t.Errorf("editor shows auth %q", a.form.auth)
	}
	a.editing, a.form = false, nil
	if !tt.HasText("web") {
		t.Errorf("sidebar misses host: %q", tt.Texts())
	}
	// The store was written to disk.
	if _, err := os.Stat(filepath.Join(a.p.DataDir, "hosts.json")); err != nil {
		t.Error(err)
	}
	a.deleteID = h.ID
	a.DeleteHost(h.ID)
	if a.Store().Len() != 0 || !a.kc.Has(keychain.Cred(cr.ID, keychain.Password)) {
		t.Error("delete left the host, or took the credential it used")
	}
}

// addCred adds a password credential, its password in the keychain.
func addCred(t *testing.T, a *App, name, password string) assets.Credential {
	t.Helper()
	cr, err := a.Store().SaveCredential(assets.Credential{Name: name, Kind: assets.CredPassword})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.kc.Set(keychain.Cred(cr.ID, keychain.Password), []byte(password)); err != nil {
		t.Fatal(err)
	}
	return cr
}

func addHost(t *testing.T, a *App, srv *sshtest.Server, name, password string) assets.Host {
	t.Helper()
	host, port := splitAddr(t, srv.Addr)
	h := assets.Host{Name: name, Address: host, Port: port, User: "demo", Auth: assets.AuthAsk}
	if password != "" {
		h.Auth, h.CredentialID = assets.AuthCredential, addCred(t, a, name+"-cred", password).ID
	}
	h, err := a.Store().Add(h)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestConnectTrustTypeSplit(t *testing.T) {
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "box", "secret")
	tt.Frame()
	a.Connect(h.ID, openAt{})
	// The host key is unknown: the app asks.
	waitFor(t, tt, "host key prompt", func() bool { return tt.HasText("Unknown Host Key") })
	mustClick(t, tt, "Trust and Connect")
	c := a.focused()
	if c == nil {
		t.Fatal("no focused session")
	}
	waitFor(t, tt, "connected", func() bool { return c.s.Status().State == session.Connected })
	if b, err := os.ReadFile(a.known.Path()); err != nil || len(b) == 0 {
		t.Errorf("host key not saved: %v", err)
	}
	tt.Type("echo hi\r")
	waitFor(t, tt, "echo", func() bool { return strings.Contains(c.s.Term.ScreenText(), "hi") })

	// Split the focused pane: a second session opens beside it.
	a.SplitFocused(layout.Right)
	tt.Frame()
	if n := len(a.conns); n != 2 {
		t.Fatalf("%d sessions after a split", n)
	}
	waitFor(t, tt, "second connected", func() bool { return a.focused().s.Status().State == session.Connected })
	if a.focused() == c {
		t.Error("focus did not move to the new pane")
	}
	a.CloseFocused()
	tt.Frame()
	if len(a.conns) != 1 {
		t.Errorf("%d sessions after closing one", len(a.conns))
	}
}

func TestPasswordPrompt(t *testing.T) {
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "nopw", "")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	a.Connect(h.ID, openAt{})
	waitFor(t, tt, "password prompt", func() bool { return a.prompting })
	tt.Frame()
	if !tt.HasText("Enter the password for nopw to connect.") || tt.HasText("No password or key is saved for nopw.") {
		t.Errorf("ask host banner: %q", tt.Texts())
	}
	a.prompt.value = "wrong"
	a.SubmitPrompt()
	waitFor(t, tt, "auth failure prompt", func() bool { return a.prompting && a.prompt.err != "" })
	a.prompt.value = "secret"
	a.SubmitPrompt()
	c := a.focused()
	waitFor(t, tt, "connected", func() bool { return c.s.Status().State == session.Connected })
	// An "ask" host's password is never saved.
	if keys := a.kc.Backend().(*keychain.Memory).Keys(); len(keys) != 0 {
		t.Errorf("keychain holds %q", keys)
	}
	_ = h
}

func TestSettingsLanguageAndTheme(t *testing.T) {
	a, tt := newTestApp(t)
	var themes []string
	var locs []string
	a.p.SetTheme = func(m theme.Mode) { themes = append(themes, string(m)) }
	a.p.SetLocale = func(l string) { locs = append(locs, l); tt.SetLocale(l) }
	a.page = pageSettings
	tt.Frame()
	mustClick(t, tt, "Dark")
	if a.Settings().Theme != "dark" || len(themes) == 0 {
		t.Errorf("theme %q, calls %v", a.Settings().Theme, themes)
	}
	s := a.Settings()
	s.Language = "zh-Hans"
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(locs) == 0 || !tt.HasText(locales.In("zh-Hans").Settings()) {
		t.Errorf("Chinese UI missing: %v %q", locs, tt.Texts())
	}
}

func TestEdgeFor(t *testing.T) {
	cases := []struct {
		x, y float32
		want layout.Edge
	}{
		{50, 50, layout.Center}, {5, 50, layout.Left}, {95, 50, layout.Right}, {50, 5, layout.Top}, {50, 95, layout.Bottom},
	}
	for _, tc := range cases {
		if got := edgeFor(tc.x, tc.y, 100, 100); got != tc.want {
			t.Errorf("edgeFor(%v,%v) = %v, want %v", tc.x, tc.y, got, tc.want)
		}
	}
}

func splitAddr(t *testing.T, addr string) (string, int) {
	t.Helper()
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(p)
	return h, n
}

// connected starts a trusted session to srv and waits for it.
func connected(t *testing.T, a *App, tt *ui.Tester, srv *sshtest.Server) *conn {
	t.Helper()
	h := addHost(t, a, srv, "box", "secret")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	a.Connect(h.ID, openAt{})
	c := a.focused()
	waitFor(t, tt, "connected", func() bool { return c.s.Status().State == session.Connected })
	return c
}

func TestFindShortcut(t *testing.T) {
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	a, tt := newTestApp(t)
	c := connected(t, a, tt, srv)
	tt.Frame()
	tt.Type("echo needle\r")
	waitFor(t, tt, "echo", func() bool { return strings.Contains(c.s.Term.ScreenText(), "needle") })
	tt.Key(appMods, ui.KeyF)
	tt.Frame()
	if !a.finding {
		t.Fatal("find shortcut did not open the find bar")
	}
	tt.Type("needle")
	tt.Frame()
	tt.Frame()
	if len(c.tv.matches) < 2 { // the typed command and its output
		t.Errorf("%d matches for needle", len(c.tv.matches))
	}
}

func TestFilesPaneInSplit(t *testing.T) {
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	root := t.TempDir()
	srv.SFTPRoot = root
	os.WriteFile(filepath.Join(root, "remote.txt"), []byte("from server"), 0o644)
	a, tt := newTestApp(t)
	os.WriteFile(filepath.Join(a.p.HomeDir, "local.txt"), []byte("from client"), 0o644)
	term := connected(t, a, tt, srv)
	tt.Frame()
	// The terminal pane's header opens Files beside it, in the same tab.
	mustClick(t, tt, "Open Files for This Host")
	tab := a.ws.ActiveTab()
	if len(a.ws.Tabs) != 1 || len(tab.Panes()) != 2 || tab.Count(layout.Files) != 1 || tab.Count(layout.Terminal) != 1 {
		t.Fatalf("want a terminal and a files pane in one tab, got %d tabs, items %v", len(a.ws.Tabs), tab.Items())
	}
	if a.page != pageWorkspace {
		t.Fatalf("page %v, want the workspace", a.page)
	}
	fc := a.focused()
	if fc == nil || fc.kind != layout.Files || fc.fs == nil || fc.tv != nil {
		t.Fatalf("focused pane is not a files pane: %+v", fc)
	}
	if tab.Root.Axis != layout.Row || tab.Root.A.Item.Kind != layout.Terminal || tab.Root.B.Item.Kind != layout.Files {
		t.Errorf("files pane not on the right of the terminal")
	}
	waitFor(t, tt, "remote listing", func() bool {
		return fc.s.Status().State == session.Connected && tt.HasText("remote.txt") && tt.HasText("local.txt")
	})
	if !tt.HasText("Files · box") {
		t.Errorf("no files pane title in %q", tt.Texts())
	}
	// The terminal goes on beside it, on its own connection.
	if term.s.Status().State != session.Connected || term.s == fc.s {
		t.Error("terminal lost or shared its connection")
	}

	a.Upload(fc, []string{filepath.Join(a.p.HomeDir, "local.txt")}, fc.fs.remote.dir)
	waitFor(t, tt, "upload", func() bool {
		b, _ := os.ReadFile(filepath.Join(root, "local.txt"))
		return string(b) == "from client"
	})
	a.Download(fc, []string{fc.fs.remote.dir + "/remote.txt"}, fc.fs.local.dir)
	waitFor(t, tt, "download", func() bool {
		b, _ := os.ReadFile(filepath.Join(a.p.HomeDir, "remote.txt"))
		return string(b) == "from server"
	})
	fc.fs.transfers.Wait()
	fc.fs.queueOpen = true
	waitFor(t, tt, "transfer list", func() bool { return tt.HasText("Transfers") && tt.HasText("Done") && tt.HasText("remote.txt") })
	fc.fs.queueOpen = false
	tt.Frame()

	// New folder on the remote side, from its IconButton and dialog.
	mustClick(t, tt, "New Remote Folder")
	if fc.fs.ask == nil || fc.fs.ask.kind != askMkdir || !fc.fs.ask.remote {
		t.Fatal("new folder dialog not open")
	}
	tt.Frame()
	tt.Type("made")
	tt.Frame()
	mustClick(t, tt, "Create")
	waitFor(t, tt, "mkdir", func() bool {
		fi, err := os.Stat(filepath.Join(root, "made"))
		return err == nil && fi.IsDir() && tt.HasText("made")
	})

	// A files pane moves like any pane: to the left of the terminal.
	a.ws.Move(fc.id, tab.Root.A.ID, layout.Left)
	tt.Frame()
	if tab.Root.A.Item != fc.item() {
		t.Errorf("files pane not moved left: %v", tab.Items())
	}
	// Closing it leaves the terminal.
	a.CloseSession(fc.id)
	tt.Frame()
	if len(tab.Panes()) != 1 || tab.Focused().Kind != layout.Terminal {
		t.Errorf("after closing files: %v", tab.Items())
	}
}

func TestFilesPaneFromHost(t *testing.T) {
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.SFTPRoot = t.TempDir()
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "box", "secret")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	a.selected = h.ID
	tt.Frame()
	mustClick(t, tt, "Open Files")
	if len(a.ws.Tabs) != 1 || a.ws.ActiveTab().Focused().Kind != layout.Files {
		t.Fatalf("no files tab: %v", a.ws.Items())
	}
	fc := a.focused()
	waitFor(t, tt, "files connected", func() bool { return fc.fs.remote.client != nil })
	// Find is for terminals only.
	tt.Key(appMods, ui.KeyF)
	tt.Frame()
	if a.finding {
		t.Error("find opened on a files pane")
	}
	// A terminal opens beside it from its header.
	mustClick(t, tt, "Open Terminal on the Right")
	if tab := a.ws.ActiveTab(); tab.Count(layout.Terminal) != 1 || tab.Count(layout.Files) != 1 {
		t.Errorf("items %v", tab.Items())
	}
}

func TestSettingsToggle(t *testing.T) {
	a, tt := newTestApp(t)
	mustClick(t, tt, "Settings")
	if a.page != pageSettings || a.prefNow() != prefAppearance {
		t.Fatal("settings did not open")
	}
	mustClick(t, tt, "Back to Sessions")
	if a.page != pageWorkspace {
		t.Fatal("settings did not close")
	}
}

// TestPromptTakesTypingAtOnce: the password field of the prompt has the
// keyboard focus as soon as it shows, with no click, even though the
// terminal it is for asked for the focus when it opened.
func TestPromptTakesTypingAtOnce(t *testing.T) {
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "nopw", "")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	_ = h
	tt.Frame()
	mustClick(t, tt, "nopw")
	tt.Frame()
	mustClick(t, tt, "Connect")
	waitFor(t, tt, "password prompt", func() bool { return a.prompting })
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
	tt.Type("wrong")
	tt.Frame()
	if a.prompt == nil || a.prompt.value != "wrong" {
		v := ""
		if a.prompt != nil {
			v = a.prompt.value
		}
		t.Fatalf("typed into the prompt, field holds %q", v)
	}
	tt.Key(0, ui.KeyEnter)
	waitFor(t, tt, "auth failure prompt", func() bool { return a.prompting && a.prompt.err != "" })
	tt.Frame()
	tt.Type("secret")
	tt.Frame()
	if a.prompt == nil || a.prompt.value != "secret" {
		t.Fatalf("typed into the prompt again, field holds %q", a.prompt.value)
	}
	tt.Key(0, ui.KeyEnter)
	c := a.focused()
	waitFor(t, tt, "connected", func() bool { return c.s.Status().State == session.Connected })
}

// TestToolbarTrail checks the toolbar keeps a small gap before window
// buttons on the right and only its margin when they are on the left.
func TestToolbarTrail(t *testing.T) {
	for _, tc := range []struct {
		bar  ui.TitleBar
		want float32
	}{
		{ui.TitleBar{Height: 52, Left: 80}, 12},    // macOS: traffic lights on the left
		{ui.TitleBar{Height: 52, Right: 138}, 144}, // Windows: three 46px caption buttons
		{ui.TitleBar{}, 12},                        // no title bar room
	} {
		if got := toolbarTrail(tc.bar); got != tc.want {
			t.Errorf("toolbarTrail(%+v) = %v, want %v", tc.bar, got, tc.want)
		}
	}
}

// TestSplitReusesConnection: a password typed once, not saved, serves a
// split, a new tab and a files pane on the host, on its one connection;
// closing one leaves the others, and after a drop one prompt serves all.
func TestSplitReusesConnection(t *testing.T) {
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "nosave", "")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	a.Connect(h.ID, openAt{})
	waitFor(t, tt, "password prompt", func() bool { return a.prompting })
	a.prompt.value = "secret"
	a.SubmitPrompt()
	first := a.focused()
	waitFor(t, tt, "connected", func() bool { return first.s.Status().State == session.Connected })

	a.SplitFocused(layout.Right)
	a.SplitFocused(layout.Bottom)
	a.OpenFilesBeside()
	a.Connect(h.ID, openAt{})
	tt.Frame()
	if len(a.conns) != 5 {
		t.Fatalf("%d panes", len(a.conns))
	}
	waitFor(t, tt, "all connected", func() bool {
		for _, c := range a.conns {
			if c.s.Status().State != session.Connected {
				return false
			}
		}
		return true
	})
	if a.prompting || srv.Logins() != 1 {
		t.Fatalf("prompting %v, %d logins; want no prompt and one login", a.prompting, srv.Logins())
	}
	if n := a.pool.Shared(a.poolKey(h)); n != 5 {
		t.Errorf("%d sessions share the connection, want 5", n)
	}
	a.CloseSession(first.id)
	waitFor(t, tt, "released", func() bool { return a.pool.Shared(a.poolKey(h)) == 4 })
	for _, c := range a.conns {
		if c.s.Status().State != session.Connected {
			t.Errorf("pane %d lost its connection when another closed", c.id)
		}
	}
	if !a.keychainOK || len(a.kc.Backend().(*keychain.Memory).Keys()) != 0 {
		t.Error("the typed password ended up in the keychain")
	}
}

// TestOnePromptForSharedFailure: panes on a host opened before its
// password is known fail together and ask once.
func TestOnePromptForSharedFailure(t *testing.T) {
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "nosave", "")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	a.Connect(h.ID, openAt{})
	a.Connect(h.ID, openAt{})
	waitFor(t, tt, "both failed", func() bool {
		for _, c := range a.conns {
			if c.s.Status().State != session.Failed {
				return false
			}
		}
		return true
	})
	waitFor(t, tt, "password prompt", func() bool { return a.prompting })
	a.prompt.value = "secret"
	a.SubmitPrompt()
	waitFor(t, tt, "all connected", func() bool {
		for _, c := range a.conns {
			if c.s.Status().State != session.Connected {
				return false
			}
		}
		return true
	})
	for i := 0; i < 5; i++ {
		tt.Frame()
	}
	if a.prompting {
		t.Error("asked a second time")
	}
	if srv.Logins() != 1 {
		t.Errorf("%d logins", srv.Logins())
	}
}

func TestSearchClear(t *testing.T) {
	a, tt := newTestApp(t)
	for _, n := range []string{"web", "db"} {
		if _, err := a.Store().Add(assets.Host{Name: n, Address: n + ".example", User: "root", Auth: assets.AuthAsk}); err != nil {
			t.Fatal(err)
		}
	}
	tt.Frame()
	if err := tt.Click("Clear search"); err == nil {
		t.Error("a clear button in an empty search")
	}
	mustClick(t, tt, "Search hosts")
	tt.Type("web")
	tt.Frame()
	if a.search != "web" || tt.HasText("db") {
		t.Fatalf("search %q, texts %q", a.search, tt.Texts())
	}
	mustClick(t, tt, "Clear search")
	tt.Frame()
	if a.search != "" || !tt.HasText("db") {
		t.Fatalf("not cleared: %q", a.search)
	}
	// The field has the keyboard again.
	tt.Type("db")
	tt.Frame()
	if a.search != "db" {
		t.Errorf("typing after clearing gave %q", a.search)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.search != "" {
		t.Errorf("Escape left %q", a.search)
	}
}

func TestFontSettings(t *testing.T) {
	old := listFonts
	t.Cleanup(func() { listFonts = old })
	listFonts = func() []fonts.Family {
		return []fonts.Family{{Name: "Test Mono", Mono: true}, {Name: "Test Sans"}}
	}
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	a, tt := newTestApp(t)
	waitFor(t, tt, "fonts listed", func() bool { return a.fonts.loaded })
	h := addHost(t, a, srv, "box", "secret")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	a.Connect(h.ID, openAt{})
	c := a.focused()
	if c.tv.family != "monospace" {
		t.Errorf("default terminal family %q", c.tv.family)
	}
	a.openSettings(prefAppearance)
	tt.Frame()
	if !tt.HasText("Interface font") {
		t.Errorf("appearance misses the interface font: %q", tt.Texts())
	}
	mustClick(t, tt, "Terminal")
	tt.Frame()
	for _, s := range []string{"Terminal font", "Monospaced fonts only"} {
		if !tt.HasText(s) {
			t.Errorf("terminal settings miss %q: %q", s, tt.Texts())
		}
	}
	s := a.Settings()
	s.UIFont, s.TerminalFont, s.TerminalFontSize = "Test Sans", "Test Mono", 16
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	a.page = pageWorkspace
	tt.Frame()
	if a.fonts.ui != "Test Sans" || c.tv.family != "Test Mono, monospace" || c.tv.fontSize != 16 {
		t.Errorf("in effect: ui %q, terminal %q %v", a.fonts.ui, c.tv.family, c.tv.fontSize)
	}
	if c.tv.fonts[0].Family != "Test Mono, monospace" {
		t.Errorf("terminal measured with %q", c.tv.fonts[0].Family)
	}
	// They persist.
	b, _ := os.ReadFile(filepath.Join(a.p.DataDir, "settings.json"))
	if !strings.Contains(string(b), `"terminalFont": "Test Mono"`) || !strings.Contains(string(b), `"uiFont": "Test Sans"`) {
		t.Errorf("settings file %s", b)
	}
	// A family not installed falls back to the system's, and says so.
	s.TerminalFont = "Gone Mono"
	a.ApplySettings(s)
	tt.Frame()
	if c.tv.family != "monospace" || a.fonts.ui != "Test Sans" {
		t.Errorf("fallback: terminal %q ui %q", c.tv.family, a.fonts.ui)
	}
	if !tt.HasText("The font “Gone Mono” isn’t installed, so the system font is used instead") {
		t.Errorf("no toast: %q", tt.Texts())
	}
	// The terminal choices: monospaced only by default, then all.
	if got := fonts.ForTerminal(a.fonts.list, a.Settings().MonospacedOnly); len(got) != 1 {
		t.Errorf("monospaced only: %v", got)
	}
}

// TestDragPaneToEdge drags the rightmost pane by its header onto the
// bottom of the first, as a user does, through the window's pointer.
func TestDragPaneToEdge(t *testing.T) {
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "box", "secret")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	first, _ := a.Connect(h.ID, openAt{})
	a.SplitFocused(layout.Right)
	a.SplitFocused(layout.Right)
	last := a.focused().id
	tt.Frame()
	tt.Frame()
	src, dst := a.paneBounds[last], a.paneBounds[first]
	if src.W == 0 || dst.W == 0 {
		t.Fatalf("pane bounds %v %v", src, dst)
	}
	// press the header, away from its buttons
	x, y := src.X+60, src.Y+20
	tt.Press(x, y)
	for i := 1; i <= 10; i++ {
		f := float32(i) / 10
		tt.Move(x+(dst.X+dst.W/2-x)*f, y+(dst.Y+dst.H*0.9-y)*f)
		tt.Frame()
	}
	tt.Frame()
	if a.dropPreview == nil || a.dropPreview.pane != a.paneNode(first) || a.dropPreview.edge != layout.Bottom {
		t.Fatalf("drop preview %+v", a.dropPreview)
	}
	tt.Release(dst.X+dst.W/2, dst.Y+dst.H*0.9)
	tt.Frame()
	tab := a.ws.ActiveTab()
	if tab.Root.Axis != layout.Row || tab.Root.A.Leaf() || tab.Root.A.Axis != layout.Column ||
		tab.Root.A.A.Item.ID != first || tab.Root.A.B.Item.ID != last {
		t.Fatalf("layout after the drop: %+v", tab.Root)
	}
	if a.dropPreview != nil {
		t.Error("the preview outlived the drop")
	}

	// Escape gives a drag up: nothing moves and the preview goes.
	before := len(a.ws.ActiveTab().Panes())
	src, dst = a.paneBounds[first], a.paneBounds[last]
	tt.Press(src.X+60, src.Y+20)
	for i := 1; i <= 8; i++ {
		f := float32(i) / 8
		tt.Move(src.X+60+(dst.X+dst.W/2-src.X-60)*f, src.Y+20+(dst.Y+dst.H*0.5-src.Y-20)*f)
		tt.Frame()
	}
	if a.dropPreview == nil || a.dropPreview.edge != layout.Center {
		t.Fatalf("center preview %+v", a.dropPreview)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Release(dst.X+dst.W/2, dst.Y+dst.H*0.5)
	tt.Frame()
	if a.dropPreview != nil || tab.Root.A.A.Item.ID != first {
		t.Errorf("Escape did not cancel: preview %+v", a.dropPreview)
	}

	// A host from the sidebar onto the right of a pane, with the same
	// preview: a new terminal there, on the shared connection.
	hb, ok := tt.Find("box")
	if !ok || hb.X > 240 {
		t.Fatalf("sidebar host at %v", hb)
	}
	dst = a.paneBounds[last]
	tt.Press(hb.X+hb.W/2, hb.Y+hb.H/2)
	for i := 1; i <= 8; i++ {
		f := float32(i) / 8
		tt.Move(hb.X+(dst.X+dst.W*0.95-hb.X)*f, hb.Y+(dst.Y+dst.H*0.5-hb.Y)*f)
		tt.Frame()
	}
	if a.dropPreview == nil || a.dropPreview.edge != layout.Right || a.dropPreview.pane != a.paneNode(last) {
		t.Fatalf("host preview %+v", a.dropPreview)
	}
	tt.Release(dst.X+dst.W*0.95, dst.Y+dst.H*0.5)
	tt.Frame()
	if n := len(a.ws.ActiveTab().Panes()); n != before+1 {
		t.Errorf("%d panes after the host drop, want %d", n, before+1)
	}
}
