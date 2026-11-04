package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/ai/history"
	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func aiRequestCount(r *recModel) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.reqs)
}

func TestAIDeferredSendCancelPreservesDraftAndResumesOnce(t *testing.T) {
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	b.fail.Store(true)
	a, tt := lockedTestApp(t, b)
	h := savedHost(t, a, "AI host", "host.example")
	consentHost(t, a, h.ID)
	model := &recModel{}
	a.newModel = func(*conn) agent.Streamer { return model }
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := a.conns[id]
	c.ai.input = "question to preserve"
	c.ai.attach = []*aiAttach{{name: "context.txt", data: []byte("context")}}
	files := cloneAIAttach(c.ai.attach)
	for range 3 {
		tt.Frame()
	}
	if b.calls.Load() != 0 {
		t.Fatal("opening AI accessed system keychain")
	}
	a.sendAI(c)
	waitFor(t, tt, "send authorization denied", func() bool { return a.unlock.modal })
	if c.ai.input != "question to preserve" || !reflect.DeepEqual(c.ai.attach, files) || aiRequestCount(model) != 0 || c.ai.running {
		t.Fatal("denied authorization changed draft or dispatched model")
	}
	mustClick(t, tt, locales.In("en").Cancel())
	b.fail.Store(false)
	for range 3 {
		tt.Frame()
	}
	if b.calls.Load() != 1 || aiRequestCount(model) != 0 || c.ai.input == "" || len(c.ai.attach) != 1 {
		t.Fatal("cancel retried or discarded draft")
	}
	a.sendAI(c)
	waitFor(t, tt, "authorized response", func() bool { return !c.ai.running && aiRequestCount(model) == 1 })
	if c.ai.input != "" || len(c.ai.attach) != 0 || b.calls.Load() != 3 {
		t.Fatal("successful authorization did not consume exactly one draft")
	}
	if list := chatSummaries(t, a, h.ID); len(list) != 1 {
		t.Fatalf("authorized turn was not encrypted: %+v", list)
	}
}

func TestAIDeferredSendDoesNotDispatchChangedOrReplacedPane(t *testing.T) {
	for _, change := range []string{"draft", "attachments", "pane", "conversation"} {
		t.Run(change, func(t *testing.T) {
			b := &authorizationBackend{Memory: keychain.NewMemory(), entered: make(chan struct{}), release: make(chan struct{})}
			a, tt := lockedTestApp(t, b)
			h := savedHost(t, a, "AI host", "host.example")
			consentHost(t, a, h.ID)
			model := &recModel{}
			a.newModel = func(*conn) agent.Streamer { return model }
			id, err := a.OpenAI(h.ID, openAt{}, 0)
			if err != nil {
				t.Fatal(err)
			}
			c := a.conns[id]
			c.ai.input = "original"
			c.ai.attach = []*aiAttach{{name: "context.txt", data: []byte("context")}}
			a.sendAI(c)
			<-b.entered
			switch change {
			case "draft":
				c.ai.input = "edited"
			case "attachments":
				c.ai.attach[0].data[0] = 'X'
			case "pane":
				a.CloseSession(id)
			case "conversation":
				a.NewChat(id)
			}
			close(b.release)
			waitFor(t, tt, "authorization completed", func() bool { return a.unlocked() && !a.unlock.busy })
			if aiRequestCount(model) != 0 || c.ai.running {
				t.Fatal("late authorization dispatched stale draft")
			}
		})
	}
}

func TestAIHistoryOpeningDefersAuthorizationUntilClick(t *testing.T) {
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	a, tt := lockedTestApp(t, b)
	h := savedHost(t, a, "AI host", "host.example")
	a.withSecrets(nil)
	waitFor(t, tt, "seed history unlock", a.unlocked)
	conversation := history.Conversation{ID: history.NewID(), Host: h.ID, Title: "previous encrypted conversation", Agent: "builtin", Created: time.Now(), Updated: time.Now(), Items: []history.Item{{Kind: "text", Text: "saved answer"}}}
	if err := a.chatHistory().Save(conversation); err != nil {
		t.Fatal(err)
	}
	p := a.p
	a.Close()
	before := b.calls.Load()
	a, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	a.Start()
	tt = ui.NewTester(a.View, 1200, 760)
	tt.SetLocale("en")
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		tt.Frame()
	}
	m := locales.In("en")
	if b.calls.Load() != before || a.unlocked() || !tt.HasText(m.SecretsLockedTitle()) || tt.HasText(m.AiHistoryEmpty()) {
		t.Fatal("AI render authorized or claimed locked history empty")
	}
	mustClick(t, tt, m.AiChatHistory())
	waitFor(t, tt, "history authorization and list", func() bool { return tt.HasText(conversation.Title) })
	if b.calls.Load() != before+1 {
		t.Fatal("history click did not share one master read")
	}
	mustClick(t, tt, conversation.Title)
	tt.Frame()
	if a.conns[id].ai.conv != conversation.ID || len(a.conns[id].ai.items) != 1 {
		t.Fatal("history click did not open encrypted conversation")
	}
}

func TestAIHistorySaveFailureKeepsCurrentConversation(t *testing.T) {
	a, tt := newTestApp(t)
	h := savedHost(t, a, "AI host", "host.example")
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := a.conns[id]
	a.noteQuestion(c, "unsaved conversation", nil)
	c.ai.items = []aiItem{{kind: "user", text: "unsaved conversation"}}
	conv := c.ai.conv
	if err := os.WriteFile(filepath.Join(a.p.DataDir, "ai-history"), []byte("block directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.NewChat(id)
	tt.Frame()
	if c.ai.conv != conv || len(c.ai.items) != 1 || !tt.HasText(locales.In("en").AiHistorySaveFailed()) {
		t.Fatal("save failure dropped current conversation or hid error")
	}
}

func TestAIProviderClientPreservesLockedFailure(t *testing.T) {
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	a, tt := lockedTestApp(t, b)
	p := &settings.AIProvider{ID: "test", BaseURL: "https://example.com", Model: "test"}
	// A brand-new locked store has no entry, so it correctly reports missing.
	if _, err := a.providerClient(p); !errors.Is(err, errAIKey) {
		t.Fatalf("missing key: %v", err)
	}
	a.withSecrets(nil)
	waitFor(t, tt, "seed provider unlock", a.unlocked)
	if err := a.kc.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.providerClient(p); !errors.Is(err, keychain.ErrLocked) || errors.Is(err, errAIKey) {
		t.Fatalf("locked key reported missing: %v", err)
	}
}

func TestAIHistoryReadOrDeleteFailurePreservesConversation(t *testing.T) {
	a, tt := newTestApp(t)
	h := savedHost(t, a, "AI host", "host.example")
	prior := history.Conversation{ID: history.NewID(), Host: h.ID, Title: "previous", Created: time.Now(), Updated: time.Now(), Items: []history.Item{{Kind: "text", Text: "previous answer"}}}
	if err := a.chatHistory().Save(prior); err != nil {
		t.Fatal(err)
	}
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := a.conns[id]
	a.noteQuestion(c, "unsaved current", nil)
	c.ai.items = []aiItem{{kind: "user", text: "unsaved current"}}
	current := c.ai.conv
	indices, err := filepath.Glob(filepath.Join(a.p.DataDir, "ai-history", "*", "index"))
	if err != nil || len(indices) != 1 {
		t.Fatalf("history index: %v %v", indices, err)
	}
	if err := os.WriteFile(indices[0], []byte("damaged ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Loading the prior file succeeds, but saving the current index fails.
	a.OpenChat(id, prior.ID)
	if c.ai.conv != current || len(c.ai.items) != 1 {
		t.Fatal("open discarded unsaved conversation after save failure")
	}
	a.ClearChat(id)
	if c.ai.conv != current || len(c.ai.items) != 1 {
		t.Fatal("clear discarded conversation after delete failure")
	}
	a.DeleteChat(h.ID, current)
	if c.ai.conv != current || len(c.ai.items) != 1 {
		t.Fatal("delete discarded conversation after delete failure")
	}
	tt.Frame()
	mustClick(t, tt, locales.In("en").AiChatHistory())
	tt.Frame()
	if !tt.HasText(locales.In("en").AiHistoryLoadFailed()) || tt.HasText(locales.In("en").AiHistoryEmpty()) {
		t.Fatal("damaged history was presented as empty")
	}
}

func TestAITemporaryACPChecksSavedConnectionSecrets(t *testing.T) {
	a, _ := newTestApp(t)
	opts, _ := installBackupStore(t, a)
	credential, err := a.store.AddCredential(assets.Credential{ID: assets.NewID(), Name: "login", Kind: assets.CredPassword})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.kc.Set(keychain.Cred(credential.ID, keychain.Password), []byte("test-password")); err != nil {
		t.Fatal(err)
	}
	if err := a.kc.Close(); err != nil {
		t.Fatal(err)
	}
	a.kc, err = keychain.OpenLocked(opts)
	if err != nil {
		t.Fatal(err)
	}
	a.unlock.stored = true
	a.unlock.secretsReady.Store(false)
	c := &conn{host: assets.Host{ID: tempPrefix + "acp", Auth: assets.AuthCredential, CredentialID: credential.ID}, ai: &aiState{agent: "acp"}}
	if !a.aiNeedsSecrets(c) {
		t.Fatal("temporary ACP ignored saved credential")
	}
	a.typed[keychain.Cred(credential.ID, keychain.Password)] = "typed-password"
	if a.aiNeedsSecrets(c) {
		t.Fatal("temporary ACP required vault for typed password")
	}
	c.host.Auth = assets.AuthAgent
	if a.aiNeedsSecrets(c) {
		t.Fatal("temporary ACP required vault for SSH agent")
	}
	proxy, err := a.store.AddProxy(assets.Proxy{ID: assets.NewID(), Name: "proxy", Kind: assets.ProxySOCKS5, Address: "localhost", User: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	c.host.ProxyID = proxy.ID
	if !a.aiNeedsSecrets(c) {
		t.Fatal("temporary ACP ignored authenticated proxy")
	}
}
