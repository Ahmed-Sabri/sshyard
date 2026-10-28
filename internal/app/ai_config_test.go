package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/ai/openai"
	"github.com/Licoy/sshyard/internal/ai/policy"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
)

func TestAIReviewUsesSelectedProviderAndNoneAsks(t *testing.T) {
	var chat, review atomic.Int32
	makeServer := func(count *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count.Add(1)
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"choices":[{"message":{"content":"{\"decision\":\"allow\",\"reason\":\"safe\"}"},"finish_reason":"stop"}]}`)
		}))
	}
	chatServer, reviewServer := makeServer(&chat), makeServer(&review)
	defer chatServer.Close()
	defer reviewServer.Close()
	a, tt := newTestApp(t)
	h := savedHost(t, a, "review", "127.0.0.1")
	s := a.Settings()
	s.AIProviders = []settings.AIProvider{{ID: "chat", BaseURL: chatServer.URL, Model: "chat"}, {ID: "review", BaseURL: reviewServer.URL, Model: "review"}}
	s.AIDefaultProvider = "chat"
	s.AIReviewProvider = "review"
	s.AIDefaultMode = "review"
	for _, id := range []string{"chat", "review"} {
		if err := a.kc.Set(keychain.AIKey(id), []byte("test-key-not-real")); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := a.conns[id]
	if err := a.ensureAI(c); err != nil {
		t.Fatal(err)
	}
	ask := a.reviewAsk(c)
	if _, err := ask(t.Context(), "review"); err != nil {
		t.Fatal(err)
	}
	if chat.Load() != 0 || review.Load() != 1 {
		t.Fatalf("chat=%d review=%d", chat.Load(), review.Load())
	}
	s = a.Settings()
	s.AIReviewProvider = ""
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if err := a.ensureAI(c); err != nil {
		t.Fatal(err)
	}
	gate := a.aiGate(c)
	done := make(chan policy.Verdict, 1)
	go func() {
		v, _ := gate.Authorize(context.Background(), policy.Action{Where: policy.Remote, Tool: "run_command", Command: "systemctl restart nginx"}, "")
		done <- v
	}()
	waitFor(t, tt, "user confirmation", func() bool { return c.ai.pending != nil })
	a.answerAI(c, policy.Reject)
	if v := <-done; v.Run {
		t.Fatal("None reviewer auto-authorized")
	}
	if review.Load() != 1 || chat.Load() != 0 {
		t.Fatal("None reviewer sent a model request")
	}
}

func TestAIProviderDeletionInvalidatesCachedClient(t *testing.T) {
	a, _ := newTestApp(t)
	h := savedHost(t, a, "provider", "127.0.0.1")
	s := a.Settings()
	s.AIProviders = []settings.AIProvider{{ID: "p", BaseURL: "http://127.0.0.1:1", Model: "m"}, {ID: "other", BaseURL: "http://127.0.0.1:2", Model: "m"}}
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if err := a.kc.Set(keychain.AIKey("p"), []byte("test-key-not-real")); err != nil {
		t.Fatal(err)
	}
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := a.conns[id]
	if err := a.ensureAI(c); err != nil {
		t.Fatal(err)
	}
	session := c.ai.sess
	if err := a.deleteProvider("p"); err != nil {
		t.Fatal(err)
	}
	if err := a.ensureAI(c); !errors.Is(err, errAIProvider) {
		t.Fatalf("error=%v", err)
	}
	if c.ai.sess != session {
		t.Fatal("history session replaced")
	}
	if _, err := c.ai.box.Stream(t.Context(), openai.Request{}, nil); !errors.Is(err, errAIProvider) {
		t.Fatalf("cached client survived: %v", err)
	}
}

func TestAIScopeRefreshCancelsTurnAndErrorsStayRedacted(t *testing.T) {
	a, _ := newTestApp(t)
	h := savedHost(t, a, "scope", "127.0.0.1")
	a.newModel = func(*conn) agent.Streamer { return echoModel{} }
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := a.conns[id]
	if err := a.ensureAI(c); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.ai.cancel = cancel
	h.AIPolicy.Sensitive = []string{"/private"}
	if _, err := a.store.Update(h); err != nil {
		t.Fatal(err)
	}
	a.refreshMode(c)
	if ctx.Err() == nil || !c.ai.dirty {
		t.Fatal("scope update did not cancel the current turn")
	}
	if err := a.ensureAI(c); err != nil {
		t.Fatal(err)
	}
	if len(c.ai.scope.Sensitive) != 1 || c.ai.scope.Sensitive[0] != "/private" {
		t.Fatal("stale gate scope")
	}
	raw := errors.New("Authorization: Bearer secret /private/path https://user:pass@example.test")
	if text := a.scrubAI(c, raw); strings.Contains(text, "secret") || strings.Contains(text, "private") {
		t.Fatalf("unsafe error %q", text)
	}
}

func TestAIProviderKeyOnlyChangeCancelsAndSaveFailureRollsBack(t *testing.T) {
	a, _ := appIn(t, t.TempDir(), nil)
	h := savedHost(t, a, "keychange", "127.0.0.1")
	s := a.Settings()
	s.AIProviders = []settings.AIProvider{{ID: "p", Name: "Provider", BaseURL: "https://example.test/v1", Model: "m"}}
	s.AIReviewProvider = "p"
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if err := a.kc.Set(keychain.AIKey("p"), []byte("test-old-key")); err != nil {
		t.Fatal(err)
	}
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := a.conns[id]
	if err := a.ensureAI(c); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.ai.cancel = cancel
	a.openProvider(&s.AIProviders[0])
	a.prov.key = "test-new-key"
	if !a.saveProvider(locales.In("en")) {
		t.Fatal(a.prov.err)
	}
	if ctx.Err() == nil || !c.ai.dirty {
		t.Fatal("key-only change did not cancel and refresh review client")
	}
	if err := a.ensureAI(c); err != nil {
		t.Fatal(err)
	}
	a.openProvider(&s.AIProviders[0])
	a.prov.key = "test-unsaved-key"
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	originalDir := a.p.DataDir
	a.p.DataDir = filepath.Join(blocker, "child")
	saved := a.saveProvider(locales.In("en"))
	a.p.DataDir = originalDir
	if saved {
		t.Fatal("settings save unexpectedly succeeded")
	}
	key, err := a.kc.Get(keychain.AIKey("p"))
	if err != nil || string(key) != "test-new-key" {
		t.Fatal("failed settings save did not restore prior key")
	}
	if a.prov.err != locales.In("en").AiSettingsSaveFailed() {
		t.Fatal("save failure did not remain visible")
	}
}
