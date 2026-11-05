package app

import (
	"context"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/Licoy/sshyard/internal/ai/acp"
	"github.com/Licoy/sshyard/internal/ai/acp/detect"
	"github.com/Licoy/sshyard/internal/ai/policy"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
)

// fakeACPScan replaces detection with agents for one test and counts the
// scans and the ones asked to read the login shell again.
func fakeACPScan(t *testing.T, agents ...detect.Agent) (scans, refreshes *atomic.Int32) {
	t.Helper()
	scans, refreshes = new(atomic.Int32), new(atomic.Int32)
	old := scanACPAgents
	scanACPAgents = func(_ context.Context, refresh bool) []detect.Agent {
		scans.Add(1)
		if refresh {
			refreshes.Add(1)
		}
		return agents
	}
	t.Cleanup(func() { scanACPAgents = old })
	return scans, refreshes
}

// fakeACPProbe replaces probing with opts for one test and counts the
// probes.
func fakeACPProbe(t *testing.T, opts []acp.Option) *atomic.Int32 {
	t.Helper()
	n := new(atomic.Int32)
	old := probeACP
	probeACP = func(_ context.Context, cfg acp.Config) ([]acp.Option, error) {
		n.Add(1)
		return opts, nil
	}
	t.Cleanup(func() { probeACP = old })
	return n
}

var (
	testCodex  = detect.Agent{ID: "codex", Name: "Codex", Type: "codex", Command: "/usr/local/bin/npx", Args: []string{"-y", "@agentclientprotocol/codex-acp"}, Version: "0.166.0", Fetch: true, Package: "@agentclientprotocol/codex-acp"}
	testGrok   = detect.Agent{ID: "grok", Name: "Grok", Type: "grok", Command: "/home/u/.grok/bin/grok", Args: []string{"agent", "stdio"}, Version: "1.0.46"}
	testClaude = detect.Agent{ID: "claude", Name: "Claude Code", Type: "claude", Version: "2.1.304", Package: "@agentclientprotocol/claude-agent-acp", Missing: true}
)

func TestACPSheetAddsSeveralDetectedAgents(t *testing.T) {
	_, refreshes := fakeACPScan(t, testClaude, testCodex, testGrok)
	a, tt := newTestApp(t)
	m := locales.In("en")
	a.openACP()
	tt.Frame()
	waitFor(t, tt, "detected agents", func() bool { return tt.HasText("Grok") })
	for _, s := range []string{m.AiACPDetected(), "Claude Code", m.AiACPNeedsAdapter(testClaude.Package), "0.166.0", m.AiACPViaNpx(), "grok agent stdio", m.AiACPCustom(), m.AiACPAddCustom()} {
		mustFind(t, tt, s)
	}
	// With nothing configured, the first agent that can run is offered.
	if !reflect.DeepEqual(a.acpForm.added, map[string]bool{"codex": true}) {
		t.Fatalf("initially added %v", a.acpForm.added)
	}
	mustClick(t, tt, "Claude Code")
	tt.Frame()
	if a.acpForm.added["claude"] {
		t.Fatal("an agent missing its adapter was added")
	}
	mustClick(t, tt, "Grok")
	tt.Frame()
	if tt.HasText(m.AiACPCommand()) {
		t.Fatal("custom command fields shown without asking")
	}
	mustClick(t, tt, m.AiACPDetectAgain())
	waitFor(t, tt, "second scan", func() bool { return refreshes.Load() == 1 && !a.acpForm.scanning })
	mustClick(t, tt, m.Save())
	tt.Frame()
	want := []settings.ACPConfig{
		{ID: "codex", Agent: "codex", Command: testCodex.Command, Args: testCodex.Args},
		{ID: "grok", Agent: "grok", Command: testGrok.Command, Args: testGrok.Args},
	}
	if got := a.Settings().AIACPAgents; !reflect.DeepEqual(got, want) {
		t.Fatalf("saved %+v, want %+v", got, want)
	}
	if a.acpOpen {
		t.Fatal("sheet still open after saving")
	}

	// Taking one off keeps the other with the choices made for it.
	s := a.Settings()
	s.AIACPAgents[1].Options = map[string]string{"model": "grok-4"}
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	a.openACP()
	waitFor(t, tt, "rescan", func() bool { return !a.acpForm.scanning && tt.HasText("Codex") })
	mustClick(t, tt, "Codex")
	tt.Frame()
	if !a.saveACP(m) {
		t.Fatal(a.acpForm.err)
	}
	want = []settings.ACPConfig{{ID: "grok", Agent: "grok", Command: testGrok.Command, Args: testGrok.Args, Options: map[string]string{"model": "grok-4"}}}
	if got := a.Settings().AIACPAgents; !reflect.DeepEqual(got, want) {
		t.Fatalf("after removal %+v", got)
	}
}

func TestACPSheetCustomCommands(t *testing.T) {
	fakeACPScan(t)
	a, tt := newTestApp(t)
	m := locales.In("en")
	s := a.Settings()
	s.AIDefaultAgent = "acp:custom"
	s.AIACPAgents = []settings.ACPConfig{{ID: "custom", Command: "/opt/old-acp"}}
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	a.openACP()
	waitFor(t, tt, "empty scan", func() bool { return tt.HasText(m.AiACPNoneDetected()) })
	mustFind(t, tt, "old-acp")
	mustClick(t, tt, m.AiACPAddCustom())
	tt.Frame()
	mustFind(t, tt, m.AiACPCommand())
	f := a.acpForm
	if f.addCustom(m) || f.err != m.AiACPCommandRequired() {
		t.Fatalf("empty command added: %q", f.err)
	}
	f.command, f.args = "  my-agent ", "--stdio\n--verbose"
	if !f.addCustom(m) || f.adding {
		t.Fatalf("custom command rejected: %q", f.err)
	}
	tt.Frame()
	mustFind(t, tt, "my-agent --stdio --verbose")
	mustClick(t, tt, m.AiACPRemove("old-acp"))
	tt.Frame()
	// The one being typed is kept as Save is pressed.
	f.adding, f.command = true, "/usr/bin/other"
	if !a.saveACP(m) {
		t.Fatal(f.err)
	}
	want := []settings.ACPConfig{{ID: "custom", Command: "my-agent", Args: []string{"--stdio", "--verbose"}}, {ID: "custom-2", Command: "/usr/bin/other"}}
	if got := a.Settings().AIACPAgents; !reflect.DeepEqual(got, want) {
		t.Fatalf("saved %+v, want %+v", got, want)
	}
	if a.Settings().AIDefaultAgent != "acp:custom" {
		t.Fatalf("default %q", a.Settings().AIDefaultAgent)
	}
	// A default naming an agent taken off goes back to the built-in one.
	s = a.Settings()
	s.AIDefaultAgent = "acp:custom-2"
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	a.openACP()
	tt.Frame()
	mustClick(t, tt, m.AiACPRemove("other"))
	if !a.saveACP(m) {
		t.Fatal(a.acpForm.err)
	}
	if got := a.Settings(); len(got.AIACPAgents) != 1 || got.AIDefaultAgent != "builtin" {
		t.Fatalf("after removal %+v default %q", got.AIACPAgents, got.AIDefaultAgent)
	}
}

func TestACPDetectedAgentNamesPane(t *testing.T) {
	scans, _ := fakeACPScan(t, testGrok)
	a, tt := newTestApp(t)
	h := savedHost(t, a, "agent", "127.0.0.1")
	s := a.Settings()
	s.AIDefaultAgent = "acp:grok"
	s.AIACPAgents = []settings.ACPConfig{{Command: "/opt/x-acp"}, {ID: "grok", Agent: "grok", Command: testGrok.Command, Args: testGrok.Args}}
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	m := locales.In("en")
	if a.conns[id].ai.agent != "acp:grok" {
		t.Fatalf("saved ACP default opened %q", a.conns[id].ai.agent)
	}
	mustFind(t, tt, m.AiBackend())
	if !tt.HasText("Grok") || tt.HasText(m.AiACP()) {
		t.Fatalf("pane does not name the detected agent: %q", tt.Texts())
	}
	a.menu = dropMenu{key: "aiagent:" + strconv.Itoa(id), sub: -1}
	tt.Frame()
	mustFind(t, tt, "x-acp")
	mustFind(t, tt, m.AiBuiltin())
	if scans.Load() != 0 {
		t.Fatal("opening a pane ran detection")
	}
}

func TestACPModelAndThinkingChips(t *testing.T) {
	opts := []acp.Option{
		{ID: "model", Name: "Model", Category: acp.CategoryModel, Current: "opus", Values: []acp.Choice{{Value: "opus", Name: "Opus"}, {Value: "sonnet", Name: "Sonnet", Description: "Fast"}}},
		{ID: "effort", Name: "Effort", Category: acp.CategoryThought, Current: "high", Values: []acp.Choice{{Value: "low", Name: "Low"}, {Value: "high", Name: "High"}}},
	}
	probes := fakeACPProbe(t, opts)
	a, tt := newTestApp(t)
	m := locales.In("en")
	s := a.Settings()
	s.AIDefaultAgent = "acp:claude"
	s.AIACPAgents = []settings.ACPConfig{{ID: "claude", Agent: "claude", Command: "/opt/claude-acp"}}
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	h := savedHost(t, a, "opts", "127.0.0.1")
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	mustFind(t, tt, m.AiACPAgentDefault())
	if tt.HasText(m.AiThinkingLevel()) || probes.Load() != 0 {
		t.Fatal("selectors shown or asked before the menu opened")
	}
	a.menu = dropMenu{key: "acpmodel:" + strconv.Itoa(id), sub: -1}
	tt.Frame()
	waitFor(t, tt, "probe", func() bool { return tt.HasText("Sonnet") })
	for _, s := range []string{"Opus", "Fast", m.AiThinkingLevel(), "High"} {
		mustFind(t, tt, s)
	}
	mustClick(t, tt, "Sonnet")
	tt.Frame()
	if got := a.Settings().AIACPAgents[0].Options; !reflect.DeepEqual(got, map[string]string{"model": "sonnet"}) {
		t.Fatalf("kept %v", got)
	}
	if got, _ := a.acpOptions(a.conns[id], a.Settings().AIACPAgents[0]); got[0].Current != "sonnet" {
		t.Fatalf("shown %+v", got)
	}
	// Another pane of the agent knows its selectors without asking again.
	other, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	a.menu = dropMenu{key: "acpeffort:" + strconv.Itoa(other), sub: -1}
	tt.Frame()
	mustClick(t, tt, "Low")
	tt.Frame()
	if probes.Load() != 1 || a.Settings().AIACPAgents[0].Options["effort"] != "low" {
		t.Fatalf("probes %d options %v", probes.Load(), a.Settings().AIACPAgents[0].Options)
	}

	// A session's report replaces what the probe found.
	c := a.conns[id]
	c.ai.acp = &acpRuntime{ready: make(chan struct{})}
	a.onACPUpdate(c, acp.Update{Kind: "config", Options: opts[:1]})
	if got, probe := a.acpOptions(c, a.Settings().AIACPAgents[0]); probe != nil || len(got) != 1 || got[0].Current != "opus" {
		t.Fatalf("live %+v", got)
	}
	c.ai.acp = nil
}

func TestACPToolCardWhere(t *testing.T) {
	a, _ := newTestApp(t)
	h := savedHost(t, a, "where", "127.0.0.1")
	c := a.conns[openAIPane(t, a, h.ID)]
	where := func(id string) string {
		for _, it := range c.ai.items {
			if it.kind == "tool" && it.tool.ID == id {
				return it.tool.Where
			}
		}
		return ""
	}
	a.onACPUpdate(c, acp.Update{Kind: "tool", Tool: &acp.ToolView{ID: "1", Kind: "execute", Title: "ls -la", Status: "pending"}})
	a.onACPUpdate(c, acp.Update{Kind: "tool", Tool: &acp.ToolView{ID: "1", Status: "completed"}})
	a.onACPUpdate(c, acp.Update{Kind: "tool", Tool: &acp.ToolView{ID: "2", Kind: "execute", Title: "mcp__sshyard__remote_run"}})
	a.onACPUpdate(c, acp.Update{Kind: "tool", Tool: &acp.ToolView{ID: "3", Kind: "edit", Title: "Edit a.go"}})
	if where("1") != string(policy.Local) || where("2") != string(policy.Remote) || where("3") != string(policy.Remote) {
		t.Fatalf("where %q %q %q", where("1"), where("2"), where("3"))
	}
}
