package app

import (
	"testing"

	"github.com/Licoy/sshyard/internal/ai/policy"
)

// TestAIApprovalInline checks that an approval asks above the composer of
// its pane, stays when clicked away from, and answers with its buttons.
func TestAIApprovalInline(t *testing.T) {
	a, tt := newTestApp(t)
	h := savedHost(t, a, "web", "10.0.0.8")
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	cn := a.conns[id]
	ch := make(chan policy.Answer, 1)
	cn.ai.pending = &aiPend{ch: ch, prompt: policy.Prompt{
		Action:   policy.Action{Where: policy.Remote, Tool: "run_command", Command: "rm -rf /tmp/build"},
		Decision: policy.Decision{Grade: policy.Grade{Risk: policy.RiskHigh}, Similar: true},
	}}
	tt.Frame()
	for _, s := range []string{"Approval needed", "rm -rf /tmp/build", "Allow once", "Allow similar this session", "Reject"} {
		if !tt.HasText(s) {
			t.Fatalf("approval lacks %q: %q", s, tt.Texts())
		}
	}
	allow := mustFind(t, tt, "Allow once")
	input := mustFind(t, tt, "Ask about this host")
	if allow.Y+allow.H > input.Y {
		t.Fatalf("approval %v not above the composer %v", allow, input)
	}
	// Clicking the timeline, away from the approval, leaves it waiting.
	tt.ClickAt(input.X+input.W/2, input.Y/2)
	tt.Frame()
	if cn.ai.pending == nil || len(ch) != 0 || !tt.HasText("Allow once") {
		t.Fatal("clicking away answered the approval")
	}
	mustClick(t, tt, "Allow once")
	tt.Frame()
	if ans := <-ch; ans != policy.AllowOnce {
		t.Fatalf("answered %v, not allow once", ans)
	}
	if cn.ai.pending != nil || tt.HasText("Allow once") {
		t.Fatal("the approval stays once answered")
	}
}

// TestAIApprovalElsewhere checks that an approval in a pane out of sight
// is told of by a toast whose action shows the pane, and that the toast
// goes once the pane shows.
func TestAIApprovalElsewhere(t *testing.T) {
	a, tt := newTestApp(t)
	h := savedHost(t, a, "web", "10.0.0.8")
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	a.page = pageSettings
	ch := make(chan policy.Answer, 1)
	a.conns[id].ai.pending = &aiPend{ch: ch, prompt: policy.Prompt{
		Action: policy.Action{Where: policy.Remote, Tool: "run_command", Command: "uptime"},
	}}
	tt.Frame()
	tt.Frame()
	if !tt.HasText("The AI on web is waiting for your approval") || tt.HasText("Allow once") {
		t.Fatalf("no toast for the approval out of sight: %q", tt.Texts())
	}
	mustClick(t, tt, "Show")
	tt.Frame()
	tt.Frame()
	if a.page != pageWorkspace || !tt.HasText("Allow once") {
		t.Fatalf("Show does not show the pane: %q", tt.Texts())
	}
	if tt.HasText("The AI on web is waiting for your approval") || len(ch) != 0 {
		t.Fatal("the toast stays, or the approval was answered")
	}
}
