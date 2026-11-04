package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

type authorizationBackend struct {
	*keychain.Memory
	fail    atomic.Bool
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *authorizationBackend) Get(k string) ([]byte, error) {
	b.calls.Add(1)
	if b.entered != nil {
		b.once.Do(func() { close(b.entered) })
		<-b.release
	}
	if b.fail.Load() {
		return nil, &keychain.SystemError{Code: -25293, Err: errors.New("private raw system diagnostic")}
	}
	return b.Memory.Get(k)
}
func lockedTestApp(t *testing.T, b keychain.Backend) (*App, *ui.Tester) {
	t.Helper()
	dir := t.TempDir()
	a, err := New(Platform{DataDir: dir, Secrets: b, HomeDir: dir, CacheDir: filepath.Join(dir, "cache")})
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

func TestUnlockFailureCancelRetryAndShortcuts(t *testing.T) {
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	b.fail.Store(true)
	a, tt := lockedTestApp(t, b)
	waitFor(t, tt, "authorization failure", func() bool { return a.unlock.modal })
	m := locales.In("en")
	if !tt.HasText(m.SecretsUnlockFailed()) || !tt.HasText(m.SecretsRetry()) || strings.Contains(strings.Join(tt.Texts(), " "), "private raw") {
		t.Fatalf("failure UI: %q", tt.Texts())
	}
	mustClick(t, tt, m.Cancel())
	tt.Frame()
	if a.unlock.modal || a.unlocked() || !tt.HasText(m.SecretsLockedTitle()) {
		t.Fatal("cancel unlocked or reopened the modal")
	}
	calls := b.calls.Load()
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
	if b.calls.Load() != calls {
		t.Fatal("cancel retried without a user action")
	}
	for _, key := range []ui.Key{ui.KeyK, ui.KeyT, ui.KeyO, ui.KeyD, ui.KeyE} {
		tt.Key(appMods, key)
		tt.Frame()
	}
	if a.quicking || len(a.conns) > 0 || a.page != pageWorkspace {
		t.Fatal("business shortcut escaped the lock")
	}
	if _, err := a.kc.Get("x"); !errors.Is(err, keychain.ErrLocked) {
		t.Fatalf("read: %v", err)
	}
	if err := a.kc.Set("x", []byte("x")); !errors.Is(err, keychain.ErrLocked) {
		t.Fatalf("write: %v", err)
	}
	if _, err := a.Connect("x", openAt{}); !errors.Is(err, keychain.ErrLocked) {
		t.Fatalf("connect: %v", err)
	}
	if err := a.StartForward("x"); !errors.Is(err, keychain.ErrLocked) {
		t.Fatalf("forward: %v", err)
	}
	b.fail.Store(false)
	mustClick(t, tt, m.SecretsRetry())
	waitFor(t, tt, "retry unlock", a.unlocked)
	if a.unlock.modal || !tt.HasText("Welcome to SSHYard") {
		t.Fatalf("ready: %q", tt.Texts())
	}
	if keys := b.Memory.Keys(); len(keys) != 1 || keys[0] != keychain.MasterKey {
		t.Fatalf("system entries: %q", keys)
	}
}

func TestUnlockModalDismissalAndTranslations(t *testing.T) {
	for _, locale := range []string{"en", "zh-Hans"} {
		for _, mode := range []theme.Mode{theme.Light, theme.Dark} {
			t.Run(locale+string(mode), func(t *testing.T) {
				b := &authorizationBackend{Memory: keychain.NewMemory()}
				b.fail.Store(true)
				a, tt := lockedTestApp(t, b)
				a.settings.Theme = mode
				tt.SetLocale(locale)
				waitFor(t, tt, "modal", func() bool { return a.unlock.modal })
				m := locales.In(locale)
				for _, want := range []string{m.SecretsUnlockFailed(), m.Cancel(), m.SecretsRetry(), unlockErrorText(m, a.unlock.err)} {
					if !tt.HasText(want) {
						t.Fatalf("missing %q in %q", want, tt.Texts())
					}
				}
				tt.Key(0, ui.KeyEscape)
				tt.Frame()
				if a.unlock.modal || !tt.HasText(m.SecretsRetry()) {
					t.Fatal("Escape did not leave retryable locked page")
				}
				mustClick(t, tt, m.SecretsRetry())
				waitFor(t, tt, "second modal", func() bool { return a.unlock.modal })
				tt.ClickAt(10, 200)
				tt.Frame()
				if a.unlock.modal || a.unlocked() {
					t.Fatal("outside dismissal escaped lock")
				}
			})
		}
	}
}

func TestUnlockBusyAndCloseLateResult(t *testing.T) {
	b := &authorizationBackend{Memory: keychain.NewMemory(), entered: make(chan struct{}), release: make(chan struct{})}
	a, tt := lockedTestApp(t, b)
	select {
	case <-b.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("authorization not requested")
	}
	for i := 0; i < 5; i++ {
		a.retryUnlock()
		tt.Frame()
	}
	if b.calls.Load() != 1 {
		t.Fatalf("concurrent authorization calls: %d", b.calls.Load())
	}
	original := []byte("original layout")
	file := filepath.Join(a.p.DataDir, "layout.json")
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	a.Close()
	close(b.release)
	// Reopening the same encrypted store proves the late result released its lock.
	waitFor(t, tt, "late result released", func() bool {
		kc, err := keychain.Open(keychain.OpenOptions{Path: filepath.Join(a.p.DataDir, "secrets.enc"), LockPath: filepath.Join(a.p.DataDir, "secrets.lock"), Scope: "sshyard", System: b.Memory})
		if err != nil {
			return false
		}
		if err := kc.Close(); err != nil {
			t.Fatal(err)
		}
		return true
	})
	tt.Frame()
	if a.unlocked() || len(a.fwds) > 0 || a.mcp.srv != nil {
		t.Fatal("late result started business after close")
	}
	if got, err := os.ReadFile(file); err != nil || string(got) != string(original) {
		t.Fatalf("locked exit overwrote layout: %q %v", got, err)
	}
}

func TestUnlockBlocksAutomaticServicesAndStartsOnce(t *testing.T) {
	dir := t.TempDir()
	s := settings.Default()
	s.MCPEnabled = true
	s.MCPPort = freePort(t)
	if err := settings.Save(filepath.Join(dir, "settings.json"), s); err != nil {
		t.Fatal(err)
	}
	st, err := assets.Open(filepath.Join(dir, "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := st.Add(assets.Host{Name: "box", Address: "localhost", User: "test", Auth: assets.AuthAsk})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveForward(assets.Forward{Name: "automatic", Kind: assets.ForwardLocal, HostID: h.ID, ListenPort: freePort(t), TargetAddr: "127.0.0.1", TargetPort: 22, AutoStart: true}); err != nil {
		t.Fatal(err)
	}
	layout := `{"tabs":[{"root":{"kind":"ai","host":"` + h.ID + `"}}]}`
	if err := os.WriteFile(filepath.Join(dir, "layout.json"), []byte(layout), 0600); err != nil {
		t.Fatal(err)
	}
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	b.fail.Store(true)
	a, err := New(Platform{DataDir: dir, Secrets: b, CacheDir: filepath.Join(dir, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.Start()
	tt := ui.NewTester(a.View, 1200, 760)
	waitFor(t, tt, "failed", func() bool { return a.unlock.modal })
	if len(a.conns) > 0 || a.mcp.srv != nil || len(a.fwds) > 0 {
		t.Fatal("locked startup ran business")
	}
	a.syncMCP()
	if a.mcp.srv != nil {
		t.Fatal("MCP bypassed gate")
	}
	b.fail.Store(false)
	a.retryUnlock()
	waitFor(t, tt, "unlocked", a.unlocked)
	if a.mcp.srv == nil || len(a.conns) != 1 || len(a.fwds) != 1 || len(a.ws.Tabs) != 0 {
		t.Fatalf("services not restored: %v %d", a.mcp.srv, len(a.conns))
	}
	srv := a.mcp.srv
	a.retryUnlock()
	a.Start()
	tt.Frame()
	if a.mcp.srv != srv || len(a.conns) != 1 || len(a.ws.Tabs) != 0 {
		t.Fatal("duplicate startup")
	}
}

func TestUnlockStorageResolutionRecovers(t *testing.T) {
	dir := t.TempDir()
	var fail atomic.Bool
	fail.Store(true)
	resolve := func() (StoragePaths, error) {
		if fail.Load() {
			return StoragePaths{}, errors.New("private path")
		}
		return StoragePaths{DataDir: dir, LockPath: filepath.Join(dir, "secrets.lock"), Scope: "test"}, nil
	}
	a, err := New(Platform{Secrets: keychain.NewMemory(), ResolveStorage: resolve})
	if err == nil {
		t.Fatal("missing initial resolution error")
	}
	defer a.Close()
	a.Start()
	tt := ui.NewTester(a.View, 1200, 760)
	waitFor(t, tt, "path failure", func() bool { return a.unlock.modal })
	fail.Store(false)
	a.retryUnlock()
	waitFor(t, tt, "recovered", a.unlocked)
	if a.Store() == nil || a.known == nil || a.p.DataDir != dir {
		t.Fatal("metadata was not reloaded")
	}
}

func TestUnlockErrorWhitelist(t *testing.T) {
	for _, locale := range []string{"en", "zh-Hans"} {
		m := locales.In(locale)
		for _, code := range []int{-128, -25293, -25308, -25291, -99999} {
			err := &keychain.OpError{Op: "master-read", Err: &keychain.SystemError{Code: code, Err: errors.New("secret /private/path raw diagnostic")}}
			text := unlockErrorText(m, err)
			if strings.Contains(text, "secret /") || !strings.Contains(text, "OSStatus") || !strings.Contains(text, m.SecretsStageReadMaster()) {
				t.Fatalf("unsafe/missing diagnostic: %q", text)
			}
		}
	}
}
