package app

import (
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/ai/policy"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/egoist/mygo/ui"
)

// TestAIPaneLayout checks the pane's controls: the provider in its
// header alongside the selected backend, with permission first on the composer row.
func TestAIPaneLayout(t *testing.T) {
	a, tt := newTestApp(t)
	h := savedHost(t, a, "web", "10.0.0.8")
	s := a.Settings()
	s.AIProviders = []settings.AIProvider{
		{ID: "p1", Name: "Alpha", Model: "alpha-1", BaseURL: "https://alpha.test/v1"},
		{ID: "p2", Name: "Beta", Model: "beta-2", BaseURL: "https://beta.test/v1"},
	}
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, gone := range []string{"Linked terminal", "No terminal", "Agent"} {
		if tt.HasText(gone) {
			t.Fatalf("%q still shows", gone)
		}
	}
	head := mustFind(t, tt, "Close Pane")
	backend := mustFind(t, tt, "Built-in")
	if backend.Y > head.Y+head.H || backend.Y+backend.H < head.Y {
		t.Fatal("backend not in header")
	}
	prov := mustFind(t, tt, "Alpha")
	if prov.Y > head.Y+head.H || prov.Y+prov.H < head.Y {
		t.Fatalf("provider %v not in the header row %v", prov, head)
	}
	perm := mustFind(t, tt, "Permission")
	input := mustFind(t, tt, "Ask about this host")
	send := mustFind(t, tt, "Send")
	mid := perm.Y + perm.H/2
	if mid < input.Y || mid > input.Y+input.H || perm.X >= input.X || send.X <= input.X {
		t.Fatalf("permission %v input %v send %v not one row in order", perm, input, send)
	}

	mustClick(t, tt, "Provider")
	tt.Frame()
	for _, s := range []string{"Alpha", "alpha-1", "Beta", "beta-2", "Manage Providers…"} {
		if !tt.HasText(s) {
			t.Fatalf("provider menu lacks %q: %q", s, tt.Texts())
		}
	}
	if a.conns[id].ai.provider != "p1" {
		t.Fatalf("with several providers the pane starts on %q, not the first", a.conns[id].ai.provider)
	}
	mustClick(t, tt, "Beta")
	tt.Frame()
	if a.conns[id].ai.provider != "p2" || a.menu.key != "" || !tt.HasText("Beta") {
		t.Fatalf("provider %q menu %q", a.conns[id].ai.provider, a.menu.key)
	}

	mustClick(t, tt, "Permission")
	tt.Frame()
	for _, s := range []string{"Asks you before every action", "A review model allows safe actions", "Every action runs without asking"} {
		if !tt.HasText(s) {
			t.Fatalf("permission menu lacks %q: %q", s, tt.Texts())
		}
	}
	mustClick(t, tt, "Review")
	tt.Frame()
	if a.conns[id].ai.mode != "review" || a.modeOf(a.conns[id]) != policy.ModeReview || a.full != nil {
		t.Fatalf("mode %q", a.conns[id].ai.mode)
	}

	mustClick(t, tt, "Provider")
	tt.Frame()
	mustClick(t, tt, "Manage Providers…")
	tt.Frame()
	if a.page != pageSettings || a.pref != prefAI {
		t.Fatalf("page %v pref %v", a.page, a.pref)
	}
}

// TestAIPaneLinksTabTerminal checks that a pane works with the terminal
// of its host in its own tab, and with none in a tab without one.
func TestAIPaneLinksTabTerminal(t *testing.T) {
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	a, tt := newTestApp(t)
	term := connected(t, a, tt, srv)
	tab := a.ws.ActiveTab()
	beside, err := a.OpenAI(term.host.ID, openAt{pane: tab.Focus, edge: layout.Right}, 0)
	if err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.conns[beside].ai.link; got != term.id {
		t.Fatalf("link %d, terminal %d", got, term.id)
	}
	alone, err := a.OpenAI(term.host.ID, openAt{}, term.id)
	if err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.ws.ActiveTab() == tab {
		t.Fatal("the pane opened in the terminal's tab")
	}
	if got := a.conns[alone].ai.link; got != 0 {
		t.Fatalf("a pane in another tab linked %d", got)
	}
}

// TestAIPaneMarkdownAndSend checks that replies show as Markdown with
// their code's buttons, that Enter sends, and that the send button stops
// a turn while it runs.
func TestAIPaneMarkdownAndSend(t *testing.T) {
	a, tt := newTestApp(t)
	h := savedHost(t, a, "web", "10.0.0.8")
	consentHost(t, a, h.ID)
	model := &blockModel{done: make(chan struct{})}
	a.newModel = func(*conn) agent.Streamer { return model }
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	cn := a.conns[id]
	cn.ai.items = []aiItem{{kind: "text", text: "Disk is **almost full**.\n\n```sh\ndf -h\n```\n"}}
	tt.Frame()
	if !tt.HasText("almost full") || tt.HasText("**almost full**") {
		t.Fatalf("not Markdown: %q", tt.Texts())
	}
	if !tt.HasText("df -h") || countExact(tt, "Copy") == 0 {
		t.Fatalf("code block: %q", tt.Texts())
	}
	mustClick(t, tt, "Copy")
	tt.Frame()
	if tt.Clipboard() != "df -h" {
		t.Fatalf("clipboard %q", tt.Clipboard())
	}
	if tt.HasText("Insert into terminal") {
		t.Fatal("insert shows without a terminal")
	}

	cn.ai.items = nil
	tt.Frame()
	mustClick(t, tt, "Ask about this host")
	tt.Type("how full is the disk")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !cn.ai.running || !paneHas(a, id, "how full is the disk") {
		t.Fatalf("Enter did not send: running %v items %+v", cn.ai.running, cn.ai.items)
	}
	if tt.HasText("Send") || !tt.HasText("Stop") || !tt.HasText("Working…") {
		t.Fatalf("running composer: %q", tt.Texts())
	}
	mustClick(t, tt, "Stop")
	waitFor(t, tt, "stopped", func() bool { return !cn.ai.running })
	tt.Frame()
	if !tt.HasText("Send") || strings.Contains(strings.Join(tt.Texts(), "\n"), "Working…") {
		t.Fatalf("after stop: %q", tt.Texts())
	}
}

// TestAIDefaultProvider checks that a pane asks the provider set as the
// default, else the first, and that a star in the settings sets it.
func TestAIDefaultProvider(t *testing.T) {
	a, tt := newTestApp(t)
	tt.SetSize(1200, 2400)
	h := savedHost(t, a, "web", "10.0.0.8")
	s := a.Settings()
	s.AIProviders = []settings.AIProvider{
		{ID: "p1", Name: "Alpha", Model: "alpha-1", BaseURL: "https://alpha.test/v1"},
		{ID: "p2", Name: "Beta", Model: "beta-2", BaseURL: "https://beta.test/v1"},
	}
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	a.openSettings(prefAI)
	tt.Frame()
	if countExact(tt, "Default provider") == 0 || countExact(tt, "Set as Default Provider") == 0 {
		t.Fatalf("stars: %q", tt.Texts())
	}
	mustClick(t, tt, "Set as Default Provider")
	tt.Frame()
	if got := a.Settings().AIDefaultProvider; got != "p2" {
		t.Fatalf("default %q", got)
	}
	a.page = pageWorkspace
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p := a.paneProvider(a.conns[id]); p == nil || p.ID != "p2" || !tt.HasText("Beta") {
		t.Fatalf("pane provider %+v", p)
	}
	if err := a.deleteProvider("p2"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.Settings().AIDefaultProvider != "" {
		t.Fatalf("deleted default kept: %q", a.Settings().AIDefaultProvider)
	}
	if p := a.paneProvider(a.conns[id]); p != nil {
		t.Fatalf("after delete %+v", p)
	}
}
