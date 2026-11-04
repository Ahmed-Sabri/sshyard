package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/ai/llm"
	"github.com/Licoy/sshyard/internal/ai/policy"
)

type resolvingTools struct {
	fakeTools
	resolved   string
	resolveErr error
}

func (t *resolvingTools) ResolvePath(context.Context, string) (string, error) {
	return t.resolved, t.resolveErr
}

func securityModel(name, args string) *scriptModel {
	return &scriptModel{replies: []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call", Name: name, Arguments: args}}},
		{Role: "assistant", Content: "done"},
	}}
}

func TestWriteDiffRequiresReadPermission(t *testing.T) {
	tools := &fakeTools{read: "private content", readOK: true}
	gate := &recordGate{refusal: "read denied"}
	model := securityModel("write_file", `{"path":"/home/user/.env","content":"new"}`)
	s := New(Config{Model: model, Gate: gate, Tools: tools})
	if err := s.Turn(t.Context(), "edit", HostMeta{}, "", nil); err != nil {
		t.Fatal(err)
	}
	if tools.readPath != "" || len(tools.calls) != 0 || len(gate.actions) != 1 || gate.actions[0].Tool != "read_file" {
		t.Fatalf("unauthorized read/write: read=%q calls=%v actions=%v", tools.readPath, tools.calls, gate.actions)
	}
}

func TestResolvedPathIsAuthorizedAndExecuted(t *testing.T) {
	tools := &resolvingTools{resolved: "/canonical/report.txt"}
	gate := &recordGate{allow: true}
	s := New(Config{Model: securityModel("read_file", `{"path":"report.txt"}`), Gate: gate, Tools: tools})
	if err := s.Turn(t.Context(), "read", HostMeta{}, "", nil); err != nil {
		t.Fatal(err)
	}
	if len(gate.actions) != 1 || gate.actions[0].Path != tools.resolved || len(tools.calls) != 1 || !strings.Contains(tools.calls[0], tools.resolved) {
		t.Fatalf("unresolved path: gate=%v calls=%v", gate.actions, tools.calls)
	}
}

func TestPathResolutionFailureStopsBeforeApprovalOrRead(t *testing.T) {
	tools := &resolvingTools{resolveErr: errors.New("symbolic link denied")}
	gate := &recordGate{allow: true}
	s := New(Config{Model: securityModel("write_file", `{"path":"report.txt","content":"new"}`), Gate: gate, Tools: tools})
	if err := s.Turn(t.Context(), "edit", HostMeta{}, "", nil); err != nil {
		t.Fatal(err)
	}
	if len(gate.actions) != 0 || tools.readPath != "" || len(tools.calls) != 0 {
		t.Fatalf("resolution failure still accessed file: gate=%v tools=%+v", gate.actions, tools)
	}
}

func TestCancelledDiffReadStopsTurn(t *testing.T) {
	tools := &fakeTools{}
	gate := &recordGate{answer: policy.Cancelled}
	model := securityModel("write_file", `{"path":"/tmp/report.txt","content":"new"}`)
	s := New(Config{Model: model, Gate: gate, Tools: tools})
	err := s.Turn(t.Context(), "edit", HostMeta{}, "", nil)
	if !errors.Is(err, context.Canceled) || len(model.reqs) != 1 || tools.readPath != "" || len(tools.calls) != 0 {
		t.Fatalf("cancelled read continued: err=%v requests=%d read=%q calls=%v", err, len(model.reqs), tools.readPath, tools.calls)
	}
}
