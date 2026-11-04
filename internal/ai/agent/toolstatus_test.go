package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/Licoy/sshyard/internal/ai/llm"
	"github.com/Licoy/sshyard/internal/ai/policy"
)

func TestInterruptedToolEmitsFinalCard(t *testing.T) {
	for _, stopErr := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(stopErr.Error(), func(t *testing.T) {
			s := New(Config{Gate: &recordGate{allow: true, by: policy.ByUser}, Tools: &fakeTools{err: stopErr, output: "partial output"}})
			var events []Event
			err := s.execOne(context.Background(), llm.ToolCall{ID: "c", Name: "run_command", Arguments: `{"command":"sleep 600"}`}, collect(&events))
			if !errors.Is(err, stopErr) {
				t.Fatalf("error %v", err)
			}
			card := lastCard(t, events)
			wantStatus := "cancel"
			if errors.Is(stopErr, context.DeadlineExceeded) {
				wantStatus = "timeout"
			}
			if card.Output != "partial output" {
				t.Fatalf("lost partial output: %+v", card)
			}
			if card.Status != wantStatus {
				t.Fatalf("status %q, want %q", card.Status, wantStatus)
			}
			if card.Phase != "done" || card.Exit != -1 || !card.Run || card.Verdict != "user" {
				t.Fatalf("interrupted tool lacks final state with approval: %+v", card)
			}
		})
	}
}
