package app

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/localkeys"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/egoist/mygo/ui"
)

func TestLayoutRememberedLocalCredentialsWaitForUnlock(t *testing.T) {
	for _, where := range []string{"target", "jump", "none"} {
		t.Run(where, func(t *testing.T) {
			dir, backend := t.TempDir(), keychain.NewMemory()
			rememberLayoutIn(t, dir)
			a, _ := appIn(t, dir, backend)
			h := savedHost(t, a, "target", "127.0.0.1")
			rememberedHost := h
			if where == "jump" {
				jump := savedHost(t, a, "jump", "127.0.0.2")
				h.JumpChain = []string{jump.ID}
				if _, err := a.store.Update(h); err != nil {
					t.Fatal(err)
				}
				rememberedHost = jump
			}
			// A real encrypted key makes default AuthAsk authentication reach the
			// network after credential selection; the dial then fails deliberately.
			pub := homeKey(t, a, "id_ed25519", "test-passphrase")
			keyID := session.KeyID(pub)
			if err := a.kc.Set(keychain.LocalKeyPassphrase(keyID), []byte("test-passphrase")); err != nil {
				t.Fatal(err)
			}
			if where != "none" {
				if err := a.lk.Remember(localkeys.Remembered{ID: keyID}); err != nil {
					t.Fatal(err)
				}
				if err := a.lk.SetLast(rememberedHost.ID, keyID); err != nil {
					t.Fatal(err)
				}
			}
			pane, err := a.makeConn(h.ID, layout.Terminal, 0)
			if err != nil {
				t.Fatal(err)
			}
			a.ws.Open(pane.item())
			a.Close()
			b := &authorizationBackend{Memory: backend}
			var dials atomic.Int32
			again, err := New(Platform{DataDir: dir, Secrets: b, HomeDir: dir, CacheDir: filepath.Join(dir, "cache"), Dial: func(context.Context, string, string) (net.Conn, error) {
				dials.Add(1)
				return nil, errors.New("test dial stopped")
			}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(again.Close)
			again.Start()
			tt := ui.NewTester(again.View, 1200, 760)
			tt.SetLocale("en")
			tt.Frame()
			restored := again.focused()
			if restored == nil || !restored.restoredQuiet {
				t.Fatal("missing quiet restored pane")
			}
			if where != "none" {
				if dials.Load() != 0 || !restored.waitingSecrets || !restored.restorePending || restored.s.Status().State != session.Idle {
					t.Fatal("remembered passphrase connection started before unlock")
				}
				if !again.restoredConnectionNeedsSecrets(h) {
					t.Fatal("remembered metadata not recognized")
				}
				again.withSecrets(nil)
			}
			waitFor(t, tt, "fresh noninteractive dial", func() bool { return dials.Load() == 1 && restored.s.Status().State == session.Failed })
			if again.prompting || again.hostKey != nil || again.unlock.modal {
				t.Fatal("restoration opened an interactive prompt")
			}
			if where == "none" && b.calls.Load() != 0 {
				t.Fatal("unremembered default authentication requested authorization")
			}
			if where != "none" && (!again.unlocked() || b.calls.Load() != 1 || restored.restorePending || restored.waitingSecrets) {
				t.Fatal("unlock did not start the pending connection once")
			}
		})
	}
}
