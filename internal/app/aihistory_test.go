package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/ai/history"
	"github.com/Licoy/sshyard/internal/ai/llm"
)

func chatSummaries(t *testing.T, a *App, host string) []history.Summary {
	t.Helper()
	list, err := a.loadChats(host)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// recModel echoes the last question and keeps what each request sent.
type recModel struct {
	mu   sync.Mutex
	reqs [][]llm.Message
}

func (r *recModel) Stream(ctx context.Context, req llm.Request, emit func(llm.Event)) (llm.Message, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, append([]llm.Message(nil), req.Messages...))
	r.mu.Unlock()
	return echoModel{}.Stream(ctx, req, emit)
}

func (r *recModel) last() []llm.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reqs[len(r.reqs)-1]
}

func TestChatHistory(t *testing.T) {
	a, tt := newTestApp(t)
	h := savedHost(t, a, "web", "10.0.0.8")
	consentHost(t, a, h.ID)
	model := &recModel{}
	a.newModel = func(*conn) agent.Streamer { return model }
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := a.conns[id]
	ask := func(q string) {
		t.Helper()
		a.SendAI(id, q)
		waitFor(t, tt, "reply to "+q, func() bool { return !c.ai.running && trailingText(c.ai.items) == "echo:"+q })
	}
	tt.Frame()
	if !tt.HasText("New Chat") || !tt.HasText("Chat History") {
		t.Fatalf("header misses its chat buttons: %q", tt.Texts())
	}
	ask("why is the disk full")
	ask("and now?")
	first := c.ai.conv
	if list := chatSummaries(t, a, h.ID); len(list) != 1 || list[0].ID != first || list[0].Title != "why is the disk full" {
		t.Fatalf("history = %+v", list)
	}

	a.NewChat(id)
	if len(c.ai.items) != 0 || c.ai.conv != "" || c.ai.sess != nil {
		t.Fatalf("new chat kept %d items, conv %q", len(c.ai.items), c.ai.conv)
	}
	ask("other question")
	second := c.ai.conv
	if list := chatSummaries(t, a, h.ID); len(list) != 2 || list[0].ID != second || list[1].ID != first {
		t.Fatalf("history = %+v", list)
	}
	for _, m := range model.last() {
		if m.Role == "user" && strings.Contains(m.Content, "disk") {
			t.Fatal("a new chat sent the old one")
		}
	}

	// Opening the first carries it on where it was.
	a.OpenChat(id, first)
	if c.ai.conv != first || len(c.ai.items) != 4 || c.ai.forgot != 0 {
		t.Fatalf("opened %q with %d items, forgot %d", c.ai.conv, len(c.ai.items), c.ai.forgot)
	}
	ask("third")
	sent := model.last()
	found := false
	for _, m := range sent {
		found = found || m.Role == "user" && m.Content == "why is the disk full"
	}
	if !found {
		t.Fatalf("the old chat was not sent again: %+v", sent)
	}

	// The history lists them and opens one.
	tt.Frame()
	mustClick(t, tt, "Chat History")
	tt.Frame()
	if !tt.HasText("other question") || !tt.HasText("why is the disk full") {
		t.Fatalf("history menu: %q", tt.Texts())
	}
	mustClick(t, tt, "other question")
	tt.Frame()
	if c.ai.conv != second {
		t.Fatalf("menu opened %q", c.ai.conv)
	}

	// Clearing asks first, then forgets it.
	c.ai.drop = c.ai.conv
	tt.Frame()
	if !tt.HasText("Clear this chat?") {
		t.Fatalf("no confirmation: %q", tt.Texts())
	}
	a.ClearChat(id)
	c.ai.drop = ""
	if list := chatSummaries(t, a, h.ID); len(list) != 1 || list[0].ID != first || len(c.ai.items) != 0 {
		t.Fatalf("after clear: %+v, %d items", list, len(c.ai.items))
	}

	// Closing the pane saves what it showed.
	ask("before close")
	last := c.ai.conv
	a.CloseSession(id)
	if list := chatSummaries(t, a, h.ID); len(list) != 2 || list[0].ID != last {
		t.Fatalf("close did not save: %+v", list)
	}
	dir := filepath.Join(a.p.DataDir, "ai-history")
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, _ error) error {
		if b, _ := os.ReadFile(p); !info.IsDir() && bytes.Contains(b, []byte("disk")) {
			t.Errorf("%s holds plain text", p)
		}
		return nil
	})

	// The empty pane offers the recent ones; deleting the host forgets them.
	id, _ = a.OpenAI(h.ID, openAt{}, 0)
	tt.Frame()
	if !tt.HasText("Recent chats") || !tt.HasText("before close") {
		t.Fatalf("no recent chats: %q", tt.Texts())
	}
	a.CloseSession(id)
	if err := a.DeleteHost(h.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := a.chatHistory().List(h.ID); len(list) != 0 {
		t.Fatalf("host's chats kept: %+v", list)
	}
}

func TestChatOfExternalAgent(t *testing.T) {
	enableACP(t)
	a, tt := newTestApp(t)
	h := savedHost(t, a, "web", "10.0.0.8")
	conv := history.Conversation{ID: history.NewID(), Host: h.ID, Title: "deploy", Agent: "acp",
		Items: []history.Item{{Kind: "user", Text: "deploy"}, {Kind: "text", Text: "done"}}}
	if err := a.chatHistory().Save(conv); err != nil {
		t.Fatal(err)
	}
	id, _ := a.OpenAI(h.ID, openAt{}, 0)
	a.OpenChat(id, conv.ID)
	c := a.conns[id]
	if c.ai.agent != "acp" || c.ai.forgot != 2 || len(c.ai.items) != 2 {
		t.Fatalf("agent %q, forgot %d, %d items", c.ai.agent, c.ai.forgot, len(c.ai.items))
	}
	tt.Frame()
	if !tt.HasText("does not remember the chat above") {
		t.Fatalf("no notice: %q", tt.Texts())
	}
}

func TestTempHostKeepsNoChats(t *testing.T) {
	a, _ := newTestApp(t)
	if a.keepsChats(&conn{host: a.localHost(), ai: &aiState{}}) {
		t.Fatal("a host that is not saved keeps chats")
	}
}

func TestChatOfHiddenExternalAgentUsesBuiltin(t *testing.T) {
	a, _ := newTestApp(t)
	h := savedHost(t, a, "web", "10.0.0.8")
	conv := history.Conversation{ID: history.NewID(), Host: h.ID, Title: "deploy", Agent: "acp",
		Items: []history.Item{{Kind: "user", Text: "deploy"}, {Kind: "text", Text: "done"}}}
	if err := a.chatHistory().Save(conv); err != nil {
		t.Fatal(err)
	}
	id, _ := a.OpenAI(h.ID, openAt{}, 0)
	a.OpenChat(id, conv.ID)
	if c := a.conns[id]; c.ai.agent != "builtin" || c.ai.forgot != 2 {
		t.Fatalf("agent %q, forgot %d", c.ai.agent, c.ai.forgot)
	}
}

// enableACP turns on the hidden ACP backend for one test
func enableACP(t *testing.T) {
	old := acpEnabled
	acpEnabled = true
	t.Cleanup(func() { acpEnabled = old })
}
