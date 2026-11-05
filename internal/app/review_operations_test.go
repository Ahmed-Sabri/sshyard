package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

type reviewOperationTools struct{ calls atomic.Int32 }

func (r *reviewOperationTools) Call(context.Context, string, json.RawMessage) (string, int, error) {
	r.calls.Add(1)
	return "operation output", 0, nil
}
func (*reviewOperationTools) ReadFile(context.Context, string) (string, bool, error) {
	return "", false, nil
}

// This exercises the HTTP assistant, selected review provider, real permission
// gate and rendered cards together, without contacting an external service.
func TestAIReviewEveryOperationUsesConfiguredModel(t *testing.T) {
	for _, kind := range []string{settings.AIReviewChat, settings.AIReviewDecisions} {
		t.Run(kind, func(t *testing.T) {
			a, tt, cn, tools, reviews := reviewOperationApp(t, kind, "allow", reviewedCommands...)
			a.SendAI(cn.id, "Restart nginx and sample system activity")
			waitFor(t, tt, "reviewed operations", func() bool { return !cn.ai.running || cn.ai.pending != nil })
			if cn.ai.pending != nil {
				t.Fatalf("operation bypassed model for user confirmation: %+v", cn.ai.pending.prompt)
			}
			if reviews.Load() != 2 || tools.calls.Load() != 2 {
				t.Fatalf("reviews %d executions %d", reviews.Load(), tools.calls.Load())
			}
			for _, it := range cn.ai.items {
				if it.kind == "tool" && (it.tool.Verdict != "reviewer" || it.tool.Review == "" || !it.tool.Run) {
					t.Fatalf("tool lacks model approval: %+v", it.tool)
				}
			}
			if countExact(tt, "Approved · Reviewer") != 2 || tt.HasText("Approved · Auto") {
				t.Fatalf("wrong approval source: %q", tt.Texts())
			}
		})
	}
}

// reviewedCommands are not read-only, so review mode sends each to the model.
var reviewedCommands = []string{"systemctl restart nginx", "iotop -b -n 1"}

func TestAIReviewReadOnlyRunsWithoutModel(t *testing.T) {
	for _, kind := range []string{settings.AIReviewChat, settings.AIReviewDecisions} {
		t.Run(kind, func(t *testing.T) {
			a, tt, cn, tools, reviews := reviewOperationApp(t, kind, "ask", "df -h", "vmstat 1 2")
			a.SendAI(cn.id, "Check disk and virtual memory usage")
			waitFor(t, tt, "read-only operations", func() bool { return !cn.ai.running || cn.ai.pending != nil })
			if cn.ai.pending != nil || reviews.Load() != 0 || tools.calls.Load() != 2 {
				t.Fatalf("pending=%v reviews %d executions %d", cn.ai.pending != nil, reviews.Load(), tools.calls.Load())
			}
			if countExact(tt, "Approved · Auto") != 2 || tt.HasText("Approved · Reviewer") {
				t.Fatalf("wrong approval source: %q", tt.Texts())
			}
		})
	}
}

// The review model judges a short follow-up with the user's earlier
// messages in the pane, never the assistant's text.
func TestAIReviewSeesEarlierUserMessages(t *testing.T) {
	for _, kind := range []string{settings.AIReviewChat, settings.AIReviewDecisions} {
		t.Run(kind, func(t *testing.T) {
			a, tt, cn, _, reviews := reviewOperationApp(t, kind, "allow", "systemctl restart nginx")
			cn.ai.items = []aiItem{
				{kind: "user", text: "nginx keeps returning 502"},
				{kind: "text", text: "assistant-only analysis"},
				{kind: "user", text: "check the upstream config"},
			}
			a.SendAI(cn.id, "go ahead and fix it")
			waitFor(t, tt, "reviewed operation", func() bool { return !cn.ai.running || cn.ai.pending != nil })
			body, _ := reviews.body.Load().(string)
			if reviews.Load() != 1 || !strings.Contains(body, "earlier user message: nginx keeps returning 502") ||
				!strings.Contains(body, "earlier user message: check the upstream config") ||
				!strings.Contains(body, "user request: go ahead and fix it") ||
				strings.Contains(body, "earlier user message: go ahead") || strings.Contains(body, "assistant-only analysis") {
				t.Fatalf("review request %q", body)
			}
		})
	}
}

func TestAIReviewAskAndFailureKeepConfirmation(t *testing.T) {
	for _, tc := range []struct{ kind, response string }{
		{settings.AIReviewChat, "ask"}, {settings.AIReviewChat, "error"},
		{settings.AIReviewDecisions, "ask"}, {settings.AIReviewDecisions, "error"},
	} {
		t.Run(tc.kind+"/"+tc.response, func(t *testing.T) {
			a, tt, cn, tools, reviews := reviewOperationApp(t, tc.kind, tc.response, reviewedCommands...)
			a.SendAI(cn.id, "Restart nginx and sample system activity")
			waitFor(t, tt, "review confirmation", func() bool { return cn.ai.pending != nil || !cn.ai.running })
			if cn.ai.pending == nil || cn.ai.pending.prompt.ReviewNote == "" || reviews.Load() != 1 || tools.calls.Load() != 0 {
				t.Fatalf("missing confirmation after review: pending=%+v reviews=%d executions=%d", cn.ai.pending, reviews.Load(), tools.calls.Load())
			}
			note := reviewNoteText(locales.In("en"), cn.ai.pending.prompt.ReviewNote)
			if !tt.HasText("Review note: "+note) || !tt.HasText("Allow once") {
				t.Fatalf("review reason not visible: %q", tt.Texts())
			}
			a.StopAI(cn.id)
			waitFor(t, tt, "stopped", func() bool { return !cn.ai.running })
		})
	}
}

func reviewOperationApp(t *testing.T, kind, response string, commands ...string) (*App, *ui.Tester, *conn, *reviewOperationTools, *reviewCount) {
	t.Helper()
	srv, reviews := reviewOperationServer(kind, response, commands)
	t.Cleanup(srv.Close)
	a, tt := newTestApp(t)
	host := savedHost(t, a, "review", "127.0.0.1")
	consentHost(t, a, host.ID)
	s := a.Settings()
	s.AIProviders = []settings.AIProvider{{ID: "chat", BaseURL: srv.URL, Model: "assistant-model"}, {ID: "review", BaseURL: srv.URL, Model: "review-model"}}
	s.AIDefaultProvider, s.AIReviewProvider, s.AIReviewKind, s.AIReviewModel, s.AIDefaultMode = "chat", "review", kind, "review-model", "review"
	for _, id := range []string{"chat", "review"} {
		if err := a.kc.Set(keychain.AIKey(id), []byte("test-key-not-real")); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	tools := &reviewOperationTools{}
	a.newTools = func(*conn) agent.Tools { return tools }
	id, err := a.OpenAI(host.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return a, tt, a.conns[id], tools, reviews
}

// reviewCount counts review requests and keeps the last one's body.
type reviewCount struct {
	atomic.Int32
	body atomic.Value
}

func reviewOperationServer(kind, response string, commands []string) (*httptest.Server, *reviewCount) {
	var assistant atomic.Int32
	var reviews reviewCount
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		body, err := io.ReadAll(r.Body)
		if err == nil {
			err = json.Unmarshal(body, &req)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Model {
		case "review-model":
			reviews.body.Store(string(body))
			reviews.Add(1)
			if response == "error" {
				http.Error(w, "review temporarily unavailable", http.StatusServiceUnavailable)
				return
			}
			if kind == settings.AIReviewDecisions {
				probability := 0.99
				if response == "ask" {
					probability = 0.1
				}
				io.WriteString(w, decisionAnswer(probability, "none"))
				return
			}
			reason := "safe for the requested inspection"
			if response == "ask" {
				reason = "Please confirm this operation"
			}
			content, _ := json.Marshal(map[string]string{"decision": response, "reason": reason})
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(content)}, "finish_reason": "stop"}}})
		case "assistant-model":
			if assistant.Add(1) == 1 {
				var calls []any
				for i, c := range commands {
					args, _ := json.Marshal(map[string]string{"command": c})
					calls = append(calls, map[string]any{"id": string(rune('a' + i)), "type": "function", "function": map[string]string{"name": "run_command", "arguments": string(args)}})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"tool_calls": calls}, "finish_reason": "tool_calls"}}})
				return
			}
			io.WriteString(w, `{"choices":[{"message":{"content":"Inspection complete"},"finish_reason":"stop"}]}`)
		default:
			http.Error(w, "unexpected model", http.StatusBadRequest)
		}
	}))
	return srv, &reviews
}

func TestAIReviewModeExplainsEveryOperation(t *testing.T) {
	for _, lang := range []string{"en", "zh-Hans"} {
		note := locales.In(lang).AiReviewProviderNote()
		old := "medium-risk"
		want := "read-only operations run"
		if lang == "zh-Hans" {
			old, want = "中风险", "只读操作直接执行"
		}
		if strings.Contains(note, old) || !strings.Contains(note, want) {
			t.Fatalf("%s has stale review scope: %s", lang, note)
		}
	}
}
