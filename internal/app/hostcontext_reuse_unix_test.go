//go:build !windows

package app

import (
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/ai/llm"
	"github.com/Licoy/sshyard/internal/session"
)

func TestAIReusesOpenTerminalSSHAcrossTurns(t *testing.T) {
	a, tt, srv, host := bashHost(t)
	terminalID, err := a.Connect(host.ID, openAt{})
	if err != nil {
		t.Fatal(err)
	}
	terminal := a.conns[terminalID]
	waitFor(t, tt, "terminal connected", func() bool { return terminal.s.Status().State == session.Connected })
	logins := srv.Logins()
	if logins != 1 {
		t.Fatalf("terminal logins %d", logins)
	}
	a.newModel = scripted(toolCall("first", "run_command", `{"command":"pwd"}`), llm.Message{Role: "assistant", Content: "first result"}, toolCall("second", "run_command", `{"command":"pwd"}`), llm.Message{Role: "assistant", Content: "second result"})
	id := openAIPane(t, a, host.ID)
	for _, question := range []string{"Show current directory", "Show it again"} {
		a.SendAI(id, question)
		waitFor(t, tt, "AI turn complete", func() bool { return !a.conns[id].ai.running })
		if srv.Logins() != logins || a.pool.Shared(a.poolKey(host)) != 1 {
			t.Fatalf("AI opened another login or retained a lease: logins %d shared %d", srv.Logins(), a.pool.Shared(a.poolKey(host)))
		}
	}
	cards := 0
	for _, item := range a.conns[id].ai.items {
		if item.kind != "tool" {
			continue
		}
		cards++
		if item.tool.Name != "run_command" || item.tool.Phase != "done" || item.tool.Exit != 0 || item.tool.Output == "" {
			t.Fatalf("unexpected card: %+v", item.tool)
		}
	}
	if cards != 2 {
		t.Fatalf("want only the two requested operations, got %d cards", cards)
	}
	if strings.Contains(terminal.s.Term.ScreenText(), "pwd") {
		t.Fatal("AI command was injected into the interactive terminal")
	}
}

func TestHostToolsNoRedundantHostInfo(t *testing.T) {
	tools := &hostTools{}
	text, exit, err := tools.Call(t.Context(), "host_info", nil)
	if err != nil || exit != 1 || text != "unknown tool host_info" {
		t.Fatalf("host_info remains callable: %q %d %v", text, exit, err)
	}
}
