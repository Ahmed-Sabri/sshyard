package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestUnlockReloadsMetadataAfterAnotherInstance(t *testing.T) {
	dir := t.TempDir()
	system := &authorizationBackend{Memory: keychain.NewMemory()}
	first, _ := appIn(t, dir, system)
	calls := system.calls.Load()
	second, err := New(Platform{DataDir: dir, Secrets: system, HomeDir: dir, CacheDir: filepath.Join(dir, "cache")})
	if !errors.Is(err, keychain.ErrBusy) {
		t.Fatalf("second open: %v", err)
	}
	defer second.Close()
	if second.Store() != nil {
		t.Fatal("metadata opened before acquiring lifetime lock")
	}
	second.Start()
	tt := ui.NewTester(second.View, 1200, 760)
	waitFor(t, tt, "second instance blocked", func() bool { return second.unlock.modal })
	if !errors.Is(second.unlock.err, keychain.ErrBusy) {
		t.Fatalf("second launch: %v", second.unlock.err)
	}
	saved, err := first.Store().Add(assets.Host{Name: "added by first", Address: "host.example", User: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	first.Close()
	second.retryUnlock()
	waitFor(t, tt, "second metadata ready", second.ready)
	if system.calls.Load() != calls || second.unlocked() {
		t.Fatal("storage retry requested authorization")
	}
	if _, ok := second.Store().Get(saved.ID); !ok {
		t.Fatal("successful retry reused stale metadata")
	}
	if _, err := second.Store().Add(assets.Host{Name: "added by second", Address: "another.example", User: "demo"}); err != nil {
		t.Fatal(err)
	}
	st, err := assets.Open(filepath.Join(dir, "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Len() != 2 {
		t.Fatalf("metadata overwritten: %d hosts", st.Len())
	}
}

func TestUnlockFileErrorsAreSafeAndSpecific(t *testing.T) {
	for _, locale := range []string{"en", "zh-Hans"} {
		m := locales.In(locale)
		for _, tc := range []struct {
			err  error
			want string
		}{
			{os.ErrPermission, m.SecretsReasonPermission()},
			{os.ErrNotExist, m.SecretsReasonNotFound()},
			{syscall.ENOSPC, m.SecretsReasonDiskFull()},
		} {
			err := &keychain.OpError{Op: "file-read", Err: &os.PathError{Op: "open", Path: "/private/sensitive/user-file", Err: tc.err}}
			got := unlockErrorText(m, err)
			if !strings.Contains(got, tc.want) || strings.Contains(got, "/private/") {
				t.Fatalf("unsafe or vague file error: %q", got)
			}
		}
	}
}

func TestUnlockKeepsWindowControls(t *testing.T) {
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	b.fail.Store(true)
	a, tt := lockedTestApp(t, b)
	closed := false
	a.withSecrets(nil)
	a.SetWindow(WindowControls{Minimize: func() {}, Maximize: func() {}, Unmaximize: func() {}, Close: func() { closed = true }})
	waitFor(t, tt, "modal", func() bool { return a.unlock.modal })
	mustClick(t, tt, locales.In("en").Cancel())
	tt.Frame()
	mustClick(t, tt, locales.In("en").WindowClose())
	if !closed {
		t.Fatal("locked page did not retain window close action")
	}
}
