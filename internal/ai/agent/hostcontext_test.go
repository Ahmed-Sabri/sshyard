package agent

import (
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/ai/llm"
)

func TestHostContextReusedAcrossTurnsWithoutHostInfo(t *testing.T) {
	model := &scriptModel{replies: []llm.Message{{Role: "assistant", Content: "first"}, {Role: "assistant", Content: "second"}}}
	tools := &fakeTools{}
	s := New(Config{Model: model, Gate: &recordGate{allow: true}, Tools: tools})
	meta := HostMeta{Name: "web-context", Address: "10.0.0.9:22", User: "ops", Cwd: "/srv/app"}
	for _, question := range []string{"Who is connected?", "What is the current directory?"} {
		if err := s.Turn(t.Context(), question, meta, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(model.reqs) != 2 || len(tools.calls) != 0 {
		t.Fatalf("requests %d calls %v", len(model.reqs), tools.calls)
	}
	for i, req := range model.reqs {
		for _, text := range []string{"Host name: web-context", "Address: 10.0.0.9:22", "User: ops", "Cwd: /srv/app", "Uname: (unknown)", "Distro: (unknown)"} {
			if countSub(req.Messages, text) != 1 {
				t.Fatalf("request %d must contain context once: %q", i, text)
			}
		}
		for _, tool := range req.Tools {
			if tool.Name == "host_info" {
				t.Fatalf("request %d offers redundant host_info", i)
			}
		}
		for _, msg := range req.Messages {
			if strings.Contains(msg.Content, "Call host_info") {
				t.Fatalf("request %d requires redundant metadata lookup", i)
			}
		}
	}
}

func TestHostPromptQueriesUnknownOSOnlyWhenNeeded(t *testing.T) {
	text := hostMessage(HostMeta{Name: "web"})
	if strings.Contains(text, "host_info") {
		t.Fatalf("still directs host_info lookup: %s", text)
	}
	for _, want := range []string{"Uname: (unknown)", "Distro: (unknown)", "only when needed for the user's task", "Do not guess"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	known := hostMessage(HostMeta{Name: "web", Uname: "Linux", Distro: "Debian"})
	if !strings.Contains(known, "Uname: Linux\nDistro: Debian") {
		t.Fatal(known)
	}
}
