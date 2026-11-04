package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Licoy/sshyard/internal/ai/llm"
	"github.com/Licoy/sshyard/internal/ai/policy"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
)

// decisionServer answers every Decisions request with body, keeping the
// last request's input.
func decisionServer(t *testing.T, body string) (*httptest.Server, *atomic.Value) {
	t.Helper()
	var input atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input string `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &req)
		input.Store(r.URL.Path + " " + req.Input)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &input
}

func decisionAnswer(p float64, risk string) string {
	b, _ := json.Marshal(map[string]any{"answers": []map[string]any{
		{"type": "predicate", "name": "safe", "probability": p},
		{"type": "choice", "name": "risk", "choice": risk, "confidence": 0.9},
	}})
	return string(b)
}

// TestDecisionReviewer checks that a decision model allows only an action
// it finds safe at the threshold and names no risk for, and that a refusal
// is an error, so the gate asks the user.
func TestDecisionReviewer(t *testing.T) {
	in := policy.ReviewInput{Category: "run_command", Risk: policy.RiskMedium, Command: "systemctl restart nginx", Task: "restart nginx"}
	for _, tc := range []struct {
		body      string
		threshold float64
		allow     bool
		note      string
	}{
		{decisionAnswer(0.95, "none"), 0.9, true, "decision:decisions:95:none"},
		{decisionAnswer(0.95, "none"), 0.97, false, "decision:decisions:95:none"},
		{decisionAnswer(0.99, "destructive"), 0.8, false, "decision:decisions:99:destructive"},
	} {
		srv, got := decisionServer(t, tc.body)
		r := decisionReviewer{client: &llm.Client{BaseURL: srv.URL, APIKey: "test-key-not-real", Model: settings.AIDecisionsModel}, kind: settings.AIReviewDecisions, threshold: tc.threshold}
		allow, note, err := r.Review(t.Context(), in)
		if err != nil || allow != tc.allow || note != tc.note {
			t.Fatalf("%s at %v: allow=%v note=%q err=%v", tc.body, tc.threshold, allow, note, err)
		}
		if s, _ := got.Load().(string); !strings.HasPrefix(s, "/decisions ") || !strings.Contains(s, "restart nginx") || !strings.Contains(s, "systemctl restart nginx") {
			t.Fatalf("request %q", s)
		}
	}
	srv, _ := decisionServer(t, `{"answers":[{"type":"refusal","name":"safe"},{"type":"choice","name":"risk","choice":"none","confidence":0.5}]}`)
	r := decisionReviewer{client: &llm.Client{BaseURL: srv.URL, APIKey: "test-key-not-real", Model: settings.AIDecisionsModel}, kind: settings.AIReviewDecisions, threshold: 0.9}
	if allow, note, err := r.Review(t.Context(), in); err == nil || allow || note != "review-declined" {
		t.Fatalf("refusal allow=%v note=%q err=%v", allow, note, err)
	}
}

// TestReviewNoteText checks that review notes read as sentences in both
// languages, and that a model's own reason shows as it is.
func TestReviewNoteText(t *testing.T) {
	en, zh := locales.In("en"), locales.In("zh-Hans")
	for _, tc := range []struct {
		note, en, zh string
	}{
		{"decision:jev:42:destructive", "Jev: 42% likely safe · may destroy data", "Jev：安全概率 42% · 可能破坏数据"},
		{"decision:decisions:97:none", "OpenAI Decisions: 97% likely safe · no risk found", "OpenAI Decisions：安全概率 97% · 未发现风险"},
		{"review timed out", "The review timed out", "审核超时"},
		{"review-declined", "The review model declined to judge this action", "审核模型拒绝判断此操作"},
		{"Restarts a web server the user named.", "Restarts a web server the user named.", "Restarts a web server the user named."},
		{"decision:jev:x:none", "decision:jev:x:none", "decision:jev:x:none"},
	} {
		if got := reviewNoteText(en, tc.note); got != tc.en {
			t.Errorf("en %q: %q", tc.note, got)
		}
		if got := reviewNoteText(zh, tc.note); got != tc.zh {
			t.Errorf("zh %q: %q", tc.note, got)
		}
	}
}

// TestAIReviewSettingsPresets checks that the review model is picked from
// what the configured providers offer, with Custom last, that a decision
// model shows the strictness, and that the custom fields set the model.
func TestAIReviewSettingsPresets(t *testing.T) {
	a, tt := newTestApp(t)
	tt.SetSize(1200, 3200)
	s := a.Settings()
	s.AIProviders = []settings.AIProvider{
		{ID: "oai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1", Model: "gpt-x"},
		{ID: "ts", Name: "TS", Format: settings.AIFormatTypeSafe, BaseURL: "https://api.typesafe.ai/v1", Model: settings.AIJevModel},
		{ID: "claude", Name: "Claude", Format: settings.AIFormatAnthropic, BaseURL: "https://api.anthropic.com/v1", Model: "claude-x"},
	}
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	m := locales.In("en")
	var labels []string
	for _, p := range reviewPresets(m, a.Settings().AIProviders) {
		labels = append(labels, p.label)
	}
	want := "OpenAI · OpenAI Decisions gpt-6-luna|OpenAI · gpt-x|TS · Jev jev-latest|TS · Jev jev-preview|Claude · claude-x"
	if strings.Join(labels, "|") != want {
		t.Fatalf("presets %q", labels)
	}
	a.openSettings(prefAI)
	tt.Frame()
	if tt.HasText("Strictness") {
		t.Fatal("strictness shown with no review model")
	}
	// The select opens on its value, as its label also names the row.
	mustClick(t, tt, "None")
	tt.Frame()
	for _, l := range append(labels, "None", "Custom…") {
		if !tt.HasText(l) {
			t.Fatalf("menu lacks %q: %q", l, tt.Texts())
		}
	}
	mustClick(t, tt, "TS · Jev jev-preview")
	tt.Frame()
	got := a.Settings()
	if got.AIReviewProvider != "ts" || got.AIReviewKind != settings.AIReviewJev || got.AIReviewModel != "jev-preview" {
		t.Fatalf("preset saved %q %q %q", got.AIReviewProvider, got.AIReviewKind, got.AIReviewModel)
	}
	if !tt.HasText("Strictness") || !tt.HasText("Standard") || tt.HasText("Review model ID") {
		t.Fatalf("texts %q", tt.Texts())
	}
	mustClick(t, tt, "Strict")
	tt.Frame()
	if a.Settings().AIReviewStrictness != settings.AIReviewStrict {
		t.Fatalf("strictness %q", a.Settings().AIReviewStrictness)
	}

	// Custom keeps the provider and shows its fields; a chat review hides
	// the strictness.
	mustClick(t, tt, "TS · Jev jev-preview")
	tt.Frame()
	mustClick(t, tt, "Custom…")
	tt.Frame()
	if !tt.HasText("Review provider") || !tt.HasText("Review model ID") || tt.HasText("Review method") {
		t.Fatalf("custom texts %q", tt.Texts())
	}
	s = a.Settings()
	s.AIReviewProvider, s.AIReviewKind, s.AIReviewModel = "oai", settings.AIReviewChat, "gpt-mini"
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Review method") || !tt.HasText("Chat model") || !tt.HasText("OpenAI Decisions") || tt.HasText("Strictness") {
		t.Fatalf("custom chat texts %q", tt.Texts())
	}
	mustClick(t, tt, "OpenAI Decisions")
	tt.Frame()
	got = a.Settings()
	if got.AIReviewKind != settings.AIReviewDecisions || got.AIReviewModel != "" || !tt.HasText("Strictness") {
		t.Fatalf("kind %q model %q texts %q", got.AIReviewKind, got.AIReviewModel, tt.Texts())
	}
	a.review.modelFocus = true
	a.review.model = "gpt-6-luna-mini"
	tt.Frame()
	if a.Settings().AIReviewModel != "gpt-6-luna-mini" {
		t.Fatalf("model not committed: %q", a.Settings().AIReviewModel)
	}
}

// TestTypeSafeProviderOnlyReviews checks that a TypeSafe provider is not
// offered to panes, and that a decision review asks it with the review
// model rather than the provider's.
func TestTypeSafeProviderOnlyReviews(t *testing.T) {
	srv, got := decisionServer(t, `{"answers":{"safe":{"type":"noul","noul":0.99},"risk":{"type":"choice","choice":"none","confidence":0.9}}}`)
	a, tt := newTestApp(t)
	h := savedHost(t, a, "web", "10.0.0.8")
	s := a.Settings()
	s.AIProviders = []settings.AIProvider{
		{ID: "ts", Name: "TS", Format: settings.AIFormatTypeSafe, BaseURL: srv.URL + "/v1", Model: settings.AIJevModel},
		{ID: "chat", Name: "Chat", BaseURL: "https://example.invalid/v1", Model: "chat-1"},
	}
	s.AIReviewProvider, s.AIReviewKind, s.AIReviewModel = "ts", settings.AIReviewJev, "jev-preview"
	if err := a.kc.Set(keychain.AIKey("ts"), []byte("test-key-not-real")); err != nil {
		t.Fatal(err)
	}
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if a.defaultProvider() != "chat" {
		t.Fatalf("default %q", a.defaultProvider())
	}
	for _, g := range a.modelChoices("") {
		for _, ch := range g {
			if ch.p.ID == "ts" {
				t.Fatalf("TypeSafe in the model menu: %+v", ch)
			}
		}
	}
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	cn := a.conns[id]
	tt.Frame()
	if p := a.paneProvider(cn); p == nil || p.ID != "chat" {
		t.Fatalf("pane provider %+v", p)
	}
	cn.ai.provider = "ts"
	if p := a.paneProvider(cn); p == nil || p.ID != "chat" {
		t.Fatalf("pinned non-chat provider %+v", p)
	}
	rev, ok := a.newReviewer(cn).(decisionReviewer)
	if !ok || rev.err != nil || rev.client.Model != "jev-preview" || rev.threshold != 0.9 {
		t.Fatalf("reviewer %+v", rev)
	}
	allow, note, err := rev.Review(t.Context(), policy.ReviewInput{Category: "run_command", Risk: policy.RiskMedium, Command: "ls -la"})
	if err != nil || !allow || note != "decision:jev:99:none" {
		t.Fatalf("allow=%v note=%q err=%v", allow, note, err)
	}
	if s, _ := got.Load().(string); !strings.HasPrefix(s, "/v1/systemone ") {
		t.Fatalf("request %q", s)
	}
}

// Both reviewer protocols must call their endpoint for the same action;
// transport failures keep a safe diagnostic without provider response text.
func TestReviewProtocolsKeepSafeFailureReason(t *testing.T) {
	for _, kind := range []string{settings.AIReviewChat, settings.AIReviewDecisions} {
		for _, status := range []int{200, 400, 401, 404, 429, 500} {
			t.Run(kind+"/"+http.StatusText(status), func(t *testing.T) {
				var calls atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					want := "/v1/decisions"
					if kind == settings.AIReviewChat {
						want = "/v1/chat/completions"
					}
					if r.URL.Path != want {
						t.Errorf("endpoint %q, want %q", r.URL.Path, want)
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					if status != 200 {
						io.WriteString(w, `{"error":{"message":"provider-private-text test-key-not-real https://private.invalid"}}`)
					} else if kind == settings.AIReviewChat {
						io.WriteString(w, `{"choices":[{"message":{"content":"{\"decision\":\"allow\",\"reason\":\"safe\"}"},"finish_reason":"stop"}]}`)
					} else {
						io.WriteString(w, decisionAnswer(0.99, "none"))
					}
				}))
				defer srv.Close()
				a, _ := newTestApp(t)
				h := savedHost(t, a, "review", "127.0.0.1")
				s := a.Settings()
				s.AIProviders = []settings.AIProvider{{ID: "p", BaseURL: srv.URL + "/v1", Model: "gpt-6-luna"}}
				s.AIReviewProvider, s.AIReviewKind = "p", kind
				if err := a.kc.Set(keychain.AIKey("p"), []byte("test-key-not-real")); err != nil {
					t.Fatal(err)
				}
				if err := a.ApplySettings(s); err != nil {
					t.Fatal(err)
				}
				id, err := a.OpenAI(h.ID, openAt{}, 0)
				if err != nil {
					t.Fatal(err)
				}
				gate := policy.NewGate(policy.GateConfig{Mode: func() policy.Mode { return policy.ModeReview }, Reviewer: a.newReviewer(a.conns[id])})
				v, err := gate.Authorize(t.Context(), policy.Action{Where: policy.Remote, Tool: "run_command", Command: "systemctl restart nginx"}, "")
				if err != nil || calls.Load() != 1 || v.Run != (status == 200) {
					t.Fatalf("review %+v error=%v calls=%d", v, err, calls.Load())
				}
				if status != 200 {
					want := "review-error:http:" + strconv.Itoa(status)
					if v.ReviewReason != want {
						t.Fatalf("reason %q, want safe code %q", v.ReviewReason, want)
					}
					for _, lang := range []string{"en", "zh-Hans"} {
						text := reviewNoteText(locales.In(lang), v.ReviewReason)
						if !strings.Contains(text, strconv.Itoa(status)) || strings.Contains(text, "private") || strings.Contains(text, "test-key") || strings.Contains(text, "review-error:") {
							t.Fatalf("unsafe or unlocalized diagnostic %q", text)
						}
					}
				}
			})
		}
	}
}

func TestReviewFailureCategoriesHideRawErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{errAIKey, "key"},
		{errAIProvider, "provider"},
		{context.DeadlineExceeded, "timeout"},
		{llm.ErrProtocol, "protocol"},
		{llm.ErrTooBig, "size"},
		{&net.DNSError{Err: "provider-private-text", Name: "private.invalid"}, "network"},
		{errors.New("test-key-not-real https://private.invalid"), "request"},
	} {
		if got := reviewErrorNote(tc.err); got != "review-error:"+tc.code {
			t.Errorf("safe failure code %q, want %q", got, tc.code)
		}
	}
}
