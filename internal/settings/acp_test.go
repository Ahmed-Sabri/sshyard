package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestACPConfigRoundTripAndUnknownAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s := Default()
	s.AIDefaultAgent = "acp:claude"
	s.AIACPAgents = []ACPConfig{
		{ID: "claude", Agent: "claude", Command: "/opt/custom-acp", Args: []string{"--stdio", "argument with spaces", ""}, Options: map[string]string{"model": "opus"}},
		{ID: "custom", Command: "/opt/other"},
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.AIDefaultAgent != "acp:claude" || !reflect.DeepEqual(got.AIACPAgents, s.AIACPAgents) {
		t.Fatalf("ACP configuration not preserved: %+v", got.AIACPAgents)
	}
	got.AIDefaultAgent = "unknown-backend"
	got.Normalize()
	if got.AIDefaultAgent != "unknown-backend" {
		t.Fatal("unknown backend silently replaced with built-in")
	}
}

func TestACPAgentsNormalizeAndLookup(t *testing.T) {
	s := Default()
	s.AIACPAgents = []ACPConfig{
		{Agent: " codex ", Command: " /bin/codex-acp "},
		{Command: "/bin/a"},
		{Command: "  "},
		{Command: "/bin/b"},
		{ID: "codex", Command: "/bin/c"},
	}
	s.Normalize()
	var ids []string
	for _, c := range s.AIACPAgents {
		ids = append(ids, c.ID)
	}
	if !reflect.DeepEqual(ids, []string{"codex", "custom", "custom-2", "codex-2"}) || s.AIACPAgents[0].Command != "/bin/codex-acp" || s.AIACPAgents[0].Agent != "codex" {
		t.Fatalf("normalized %+v", s.AIACPAgents)
	}
	if c, ok := s.ACP("acp:custom-2"); !ok || c.Command != "/bin/b" {
		t.Fatalf("lookup %+v %v", c, ok)
	}
	if c, ok := s.ACP("acp"); !ok || c.ID != "codex" {
		t.Fatalf("first %+v %v", c, ok)
	}
	if _, ok := s.ACP("acp:gone"); ok {
		t.Fatal("found a removed agent")
	}
	if _, ok := s.ACP("builtin"); ok || IsACP("builtin") || !IsACP("acp") || !IsACP("acp:x") {
		t.Fatal("builtin taken for ACP")
	}
	a, b := s.AIACPAgents[0], s.AIACPAgents[0]
	b.Options = map[string]string{"model": "x"}
	if !a.SameLaunch(b) {
		t.Fatal("chosen options change how the agent starts")
	}
}

func TestLegacySingleACPMoves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"aiDefaultAgent":"acp","aiACP":{"agent":"grok","command":"/bin/grok","args":["agent","stdio"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []ACPConfig{{ID: "grok", Agent: "grok", Command: "/bin/grok", Args: []string{"agent", "stdio"}}}
	if got.AIACP != nil || !reflect.DeepEqual(got.AIACPAgents, want) {
		t.Fatalf("legacy %+v %+v", got.AIACP, got.AIACPAgents)
	}
	if c, ok := got.ACP(got.AIDefaultAgent); !ok || c.ID != "grok" {
		t.Fatalf("default %+v %v", c, ok)
	}
}
