package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/mcp"
)

func TestSkillText(t *testing.T) {
	a, _ := newTestApp(t)
	text := skillText("http://127.0.0.1:4000", "tok-123", a.mcpHandler("tok-123").Tools)
	for _, want := range []string{
		"---\nname: sshyard\ndescription: ",
		"Base URL: http://127.0.0.1:4000/api/v1",
		"Authorization: Bearer tok-123",
		"### run_command",
		"- `host_id` (string, required): The id from list_hosts",
		"- `timeout_seconds` (integer): Default 60",
		"### list_groups\n\nList the host groups",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
}

func TestSkillInject(t *testing.T) {
	a, tt := newTestApp(t)
	tt.SetSize(1100, 2600)
	home := a.p.HomeDir
	for _, d := range []string{".claude", ".agents"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	claude := filepath.Join(home, ".claude", "skills", "sshyard", "SKILL.md")
	agents := filepath.Join(home, ".agents", "skills", "sshyard", "SKILL.md")

	// Off: what is found shows, but nothing can be injected.
	a.openSettings(prefAgents)
	tt.Frame()
	if !tt.HasText(filepath.FromSlash("~/.codex/skills/sshyard · Not found")) || !tt.HasText(filepath.FromSlash("~/.claude/skills/sshyard · Not installed")) ||
		!tt.HasText(filepath.FromSlash("~/.agents/skills/sshyard · Not installed")) {
		t.Fatalf("off: %q", tt.Texts())
	}
	mustClick(t, tt, "Inject into All Found Agents")
	tt.Frame()
	if _, err := os.Stat(claude); err == nil {
		t.Fatal("injected while off")
	}

	s := a.Settings()
	s.MCPPort = freePort(t)
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if err := a.setMCP(true); err != nil || a.mcp.srv == nil {
		t.Fatalf("start: %v %v", err, a.mcp.err)
	}
	tt.Frame()
	mustClick(t, tt, "Inject into All Found Agents")
	tt.Frame()
	for _, f := range []string{claude, agents} {
		b, err := os.ReadFile(f)
		if err != nil || string(b) != a.mcp.skill || !strings.Contains(string(b), a.mcp.token) {
			t.Fatalf("%s: %v %q", f, err, b)
		}
		if fi, _ := os.Stat(f); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", f, fi.Mode())
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); err == nil {
		t.Fatal("made a folder for an agent that is not there")
	}
	if !tt.HasText(filepath.FromSlash("~/.claude/skills/sshyard · Installed")) || !tt.HasText("Skill injected into 2 agents.") {
		t.Fatalf("installed: %q", tt.Texts())
	}

	// A skill of the same name that is not SSHYard's is left alone.
	other := filepath.Join(home, ".codex", "skills", "sshyard", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("my own"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A new token rewrites SSHYard's skills.
	if err := a.newMCPToken(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(claude); string(b) != a.mcp.skill || !strings.Contains(string(b), a.mcp.token) {
		t.Fatalf("not refreshed: %q", b)
	}
	if b, _ := os.ReadFile(other); string(b) != "my own" {
		t.Fatalf("overwrote another skill: %q", b)
	}
	tt.Frame()
	if !tt.HasText(filepath.FromSlash("~/.codex/skills/sshyard · A file with this name is managed by the user and was not changed")) {
		t.Fatalf("stale: %q", tt.Texts())
	}

	mustClick(t, tt, "Remove from Claude Code")
	tt.Frame()
	if _, err := os.Stat(filepath.Dir(claude)); err == nil {
		t.Fatal("not removed")
	}
	mustClick(t, tt, "Inject into Claude Code")
	tt.Frame()
	if b, _ := os.ReadFile(claude); string(b) != a.mcp.skill {
		t.Fatalf("single inject: %q", b)
	}

	// Off, a skill can still be removed.
	if err := a.setMCP(false); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	mustClick(t, tt, "Remove from .agents")
	tt.Frame()
	if _, err := os.Stat(agents); err == nil {
		t.Fatal("not removed while off")
	}
	if a.skillPrompt() != "" || a.apiExample() != "" {
		t.Fatal("prompt while off")
	}
}

func TestSkillPrompt(t *testing.T) {
	a, _ := newTestApp(t)
	s := a.Settings()
	s.MCPPort = freePort(t)
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if err := a.setMCP(true); err != nil {
		t.Fatal(err)
	}
	if p := a.skillPrompt(); !strings.Contains(p, a.mcp.srv.Base()+mcp.SkillPath) || !strings.Contains(p, "Bearer "+a.mcp.token) {
		t.Fatalf("prompt %q", p)
	}
	if e := a.apiExample(); !strings.Contains(e, a.mcp.srv.Base()+"/api/v1/list_hosts") {
		t.Fatalf("example %q", e)
	}
}
