package app

import (
	"bytes"
	"os"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/egoist/mygo/ui"
)

func TestDeferredCredentialSaveKeepsDraftThroughCancelAndRetry(t *testing.T) {
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	b.fail.Store(true)
	a, tt := lockedTestApp(t, b)
	a.openCredForm(nil, nil)
	f := a.credForm
	f.name, f.user, f.password = "saved login", "demo", "temporary-draft-secret"
	if a.SaveCredForm(en) {
		t.Fatal("saved before authorization")
	}
	waitFor(t, tt, "authorization failure", func() bool { return a.unlock.modal })
	if a.credForm != f || f.password != "temporary-draft-secret" || len(a.store.Credentials()) != 0 {
		t.Fatal("failed authorization changed the draft or metadata")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.unlock.modal || a.credForm != f || a.unlocked() {
		t.Fatal("cancel did not leave the credential draft locked")
	}
	calls := b.calls.Load()
	for range 3 {
		tt.Frame()
	}
	if b.calls.Load() != calls {
		t.Fatal("render retried authorization")
	}
	b.fail.Store(false)
	a.SaveCredForm(en)
	waitFor(t, tt, "resumed credential save", func() bool { return a.credForm == nil })
	crs := a.store.Credentials()
	if len(crs) != 1 {
		t.Fatalf("credentials: %v", crs)
	}
	pw, err := a.kc.Get(keychain.Cred(crs[0].ID, keychain.Password))
	if err != nil || string(pw) != "temporary-draft-secret" {
		t.Fatalf("saved secret: %q %v", pw, err)
	}
	noSecrets(t, a.p.DataDir, "temporary-draft-secret")
}

func TestDeferredMetadataFormsDoNotRequestSecrets(t *testing.T) {
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	a, _ := lockedTestApp(t, b)
	a.openEditor(nil)
	a.form.name, a.form.address, a.form.user = "plain", "example.test", "demo"
	a.form.auth = a.askLabel(en)
	if !a.SaveForm(en) {
		t.Fatal(a.form.err)
	}
	a.openProxyForm(nil, nil)
	a.proxyForm.name, a.proxyForm.address = "plain proxy", "localhost"
	if !a.SaveProxyForm(en) {
		t.Fatal(a.proxyForm.err)
	}
	h, err := a.store.AddHost(assets.Host{ID: assets.NewID(), Name: "saved password metadata", Address: "example.test", Port: 22, User: "demo", Auth: assets.AuthPassword})
	if err != nil {
		t.Fatal(err)
	}
	a.openEditor(&h)
	a.form.name = "renamed without reading password"
	if !a.SaveForm(en) {
		t.Fatal(a.form.err)
	}
	if b.calls.Load() != 0 || a.unlocked() || a.unlock.busy {
		t.Fatal("metadata requested system authorization")
	}
}

func TestDeferredSnippetSaveResumesAfterAuthorization(t *testing.T) {
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	a, tt := lockedTestApp(t, b)
	a.openSnippet(en, assets.Snippet{}, false)
	f := a.snippets.form
	f.meta.Name, f.content = "command", "echo encrypted-command-marker"
	if a.saveSnippetForm(en) {
		t.Fatal("snippet saved before authorization")
	}
	waitFor(t, tt, "snippet saved", func() bool { return a.snippets.form == nil })
	items := a.store.Snippets()
	if len(items) != 1 {
		t.Fatalf("snippets: %v", items)
	}
	body, err := a.snippetContent(items[0].ID)
	if err != nil || body != "echo encrypted-command-marker" {
		t.Fatalf("snippet body: %q %v", body, err)
	}
	noSecrets(t, a.p.DataDir, "encrypted-command-marker")
}

func TestDeferredExistingVaultAllowsMetadataEditAndPlainOwnerDeletion(t *testing.T) {
	a, _ := newTestApp(t)
	opts, _ := installBackupStore(t, a)
	h := savePasswordHost(t, a, "example.test")
	before, err := os.ReadFile(opts.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.kc.Close(); err != nil {
		t.Fatal(err)
	}
	a.kc, err = keychain.OpenLocked(opts)
	if err != nil {
		t.Fatal(err)
	}
	a.unlock.secretsReady.Store(false)
	a.openEditor(&h)
	a.form.name, a.form.address = "renamed while locked", "new.example.test"
	if !a.SaveForm(en) {
		t.Fatal(a.form.err)
	}
	a.openEditor(nil)
	a.form.name, a.form.address, a.form.user, a.form.auth = "plain", "plain.test", "demo", a.askLabel(en)
	if !a.SaveForm(en) {
		t.Fatal(a.form.err)
	}
	plainID := a.selected
	if err := a.DeleteHost(plainID); err != nil {
		t.Fatal(err)
	}
	a.openProxyForm(nil, nil)
	a.proxyForm.name, a.proxyForm.address = "plain proxy", "localhost"
	if !a.SaveProxyForm(en) {
		t.Fatal(a.proxyForm.err)
	}
	if err := a.DeleteProxy(a.store.Proxies()[0].ID); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(opts.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || a.kc.Unlocked() || a.unlock.busy {
		t.Fatal("metadata accessed or modified the encrypted vault")
	}
}

func TestDeferredAuthorizationDoesNotSaveReplacedDraft(t *testing.T) {
	b := &authorizationBackend{Memory: keychain.NewMemory(), entered: make(chan struct{}), release: make(chan struct{})}
	a, tt := lockedTestApp(t, b)
	a.openCredForm(nil, nil)
	a.credForm.name, a.credForm.password = "original draft", "secret"
	a.SaveCredForm(en)
	<-b.entered
	a.openCredForm(nil, nil)
	replacement := a.credForm
	replacement.name = "replacement draft"
	close(b.release)
	waitFor(t, tt, "authorization complete", a.unlocked)
	if a.credForm != replacement || len(a.store.Credentials()) != 0 {
		t.Fatal("authorization saved a replaced draft")
	}
}

func TestDeferredManualForwardUsesTemporaryPasswordWithoutUnlock(t *testing.T) {
	srv, _ := keyServer(t)
	srv.DirectTCPIP = true
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	a, tt := lockedTestApp(t, b)
	h := addSrvHost(t, a, srv, "temporary login", assets.AuthAsk, "")
	a.typed["ask/"+h.ID] = "pw"
	f, err := a.store.SaveForward(assets.Forward{Kind: assets.ForwardLocal, HostID: h.ID, ListenPort: freeTCPPort(t), TargetAddr: "127.0.0.1", TargetPort: 80})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ToggleForward(f.ID); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, a, tt, f.ID)
	if b.calls.Load() != 0 || a.unlocked() {
		t.Fatal("manual forward requested system authorization")
	}
	if err := a.ToggleForward(f.ID); err != nil {
		t.Fatal(err)
	}
	if b.calls.Load() != 0 {
		t.Fatal("stopping forward requested system authorization")
	}
}
