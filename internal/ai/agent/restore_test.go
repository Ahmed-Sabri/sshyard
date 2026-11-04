package agent

import (
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/ai/llm"
)

func TestHistoryAndRestore(t *testing.T) {
	model := &scriptModel{replies: []llm.Message{{Content: "hello"}}}
	s := New(Config{Model: model, Gate: &recordGate{allow: true}, Tools: &fakeTools{}})
	parts := []llm.Part{{Name: "shot.png", MIME: "image/png", Data: []byte("png")}}
	if err := s.TurnWith(t.Context(), "look", parts, HostMeta{Name: "web"}, "", nil); err != nil {
		t.Fatal(err)
	}
	h := s.History()
	if len(h) != 2 || h[0].Role != "user" || h[1].Content != "hello" {
		t.Fatalf("history = %+v", h)
	}
	if h[0].Parts != nil || !strings.Contains(h[0].Content, "[attachments omitted: shot.png]") {
		t.Fatalf("attachment kept: %+v", h[0])
	}

	// A call answered once, a call never answered and a stray result.
	h = append(h,
		llm.Message{Role: "user", Content: "run it"},
		llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "a", Name: "run_command"}, {ID: "b", Name: "run_command"}}},
		llm.Message{Role: "tool", ToolCallID: "a", Content: "ok"},
		llm.Message{Role: "tool", ToolCallID: "z", Content: "stray"},
		llm.Message{Role: "system", Content: "old host"},
	)
	model2 := &scriptModel{replies: []llm.Message{{Content: "again"}}}
	r := Restore(Config{Model: model2, Gate: &recordGate{allow: true}, Tools: &fakeTools{}}, h)
	if r.Paused() {
		t.Fatal("restored paused")
	}
	if err := r.Turn(t.Context(), "next", HostMeta{Name: "web"}, "", nil); err != nil {
		t.Fatal(err)
	}
	got := model2.reqs[0].Messages
	var roles []string
	for _, m := range got {
		roles = append(roles, m.Role+":"+m.ToolCallID)
	}
	want := "system: user: assistant: user: assistant: tool:a tool:b system: user:"
	if strings.Join(roles, " ") != want {
		t.Fatalf("roles = %s", strings.Join(roles, " "))
	}
	if got[0].Content != SystemPrompt || strings.Contains(got[7].Content, "old host") || !strings.Contains(got[7].Content, "web") || got[6].Content != "cancelled" {
		t.Fatalf("messages = %+v", got)
	}
}
