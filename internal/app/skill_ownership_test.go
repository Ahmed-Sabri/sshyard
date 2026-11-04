package app

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSkillOwnershipAllOperations(t *testing.T) {
	for _, content := range []string{"my own", skillHead + "description: My own SSHYard helper\n---\n"} {
		t.Run(strings.Split(content, "\n")[0], func(t *testing.T) {
			a, tt := newTestApp(t)
			a.mcp.skill = skillText("http://127.0.0.1:4000", "test-token", nil)
			target := a.skillTargets()[0]
			if err := os.MkdirAll(filepath.Dir(target.file()), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target.file(), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if state := a.skillStateOf(target); state != skillConflict {
				t.Fatalf("state %v", state)
			}
			if err := a.installSkill(target); !errors.Is(err, errSkillConflict) {
				t.Fatalf("single install: %v", err)
			}
			if err := a.removeSkill(target); !errors.Is(err, errSkillConflict) {
				t.Fatalf("remove: %v", err)
			}
			if n, err := a.installSkills(); n != 0 || !errors.Is(err, errSkillConflict) {
				t.Fatalf("batch: %d %v", n, err)
			}
			if err := a.refreshSkills(); err != nil {
				t.Fatalf("refresh user file: %v", err)
			}
			if b, err := os.ReadFile(target.file()); err != nil || string(b) != content {
				t.Fatalf("user file changed: %q %v", b, err)
			}
			tt.SetSize(1100, 2600)
			a.openSettings(prefAgents)
			tt.Frame()
			if !tt.HasText(filepath.FromSlash("~/.claude/skills/sshyard · A file with this name is managed by the user and was not changed")) {
				t.Fatalf("conflict hidden: %q", tt.Texts())
			}
		})
	}
}

func TestSkillRefusesNonRegularTargets(t *testing.T) {
	a, _ := newTestApp(t)
	a.mcp.skill = skillText("http://127.0.0.1:4000", "test-token", nil)
	target := a.skillTargets()[0]
	if err := os.MkdirAll(target.file(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.installSkill(target); !errors.Is(err, errSkillConflict) {
		t.Fatalf("directory: %v", err)
	}
	if err := os.Remove(target.file()); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	external := filepath.Join(t.TempDir(), "user-skill")
	if err := os.WriteFile(external, []byte("user"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, target.file()); err != nil {
		t.Fatal(err)
	}
	if err := a.installSkill(target); !errors.Is(err, errSkillConflict) {
		t.Fatalf("symlink install: %v", err)
	}
	if err := a.removeSkill(target); !errors.Is(err, errSkillConflict) {
		t.Fatalf("symlink remove: %v", err)
	}
	if b, _ := os.ReadFile(external); string(b) != "user" {
		t.Fatalf("symlink target changed: %q", b)
	}
}

func TestSkillTokenRotationReportsPartialRefresh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions")
	}
	a, tt := newTestApp(t)
	s := a.Settings()
	s.MCPPort = freePort(t)
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if err := a.setMCP(true); err != nil {
		t.Fatal(err)
	}
	targets := a.skillTargets()
	for _, target := range targets[:2] {
		if err := a.installSkill(target); err != nil {
			t.Fatal(err)
		}
	}
	old := a.mcp.token
	blocked := filepath.Dir(targets[0].file())
	if err := os.Chmod(blocked, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(blocked, 0700)
	err := a.newMCPToken()
	if err == nil || !strings.Contains(err.Error(), "Claude Code") {
		t.Fatalf("refresh error: %v", err)
	}
	if a.mcp.token == old || a.mcp.srv == nil || a.mcp.err != nil || a.mcp.skillErr == nil {
		t.Fatal("rotation/listener state incorrect")
	}
	if b, _ := os.ReadFile(targets[0].file()); !strings.Contains(string(b), old) {
		t.Fatal("failed skill unexpectedly replaced")
	}
	if b, _ := os.ReadFile(targets[1].file()); string(b) != a.mcp.skill {
		t.Fatal("healthy target not refreshed")
	}
	tt.SetSize(1100, 2600)
	a.openSettings(prefAgents)
	tt.Frame()
	if !tt.HasText("Skill update failed: Claude Code: No permission to access the directory") {
		t.Fatalf("refresh failure hidden: %q", tt.Texts())
	}
	mustClick(t, tt, "Generate New Token")
	tt.Frame()
	if !tt.HasText("Token rotated, but some Skill updates failed: Claude Code: No permission to access the directory") || tt.HasText("New token generated. Agents using the old one were disconnected and injected skills were updated.") {
		t.Fatalf("rotation toast misreported: %q", tt.Texts())
	}
	if err := os.Chmod(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.installSkill(targets[0]); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.mcp.skillErr != nil || tt.HasText("Skill update failed: Claude Code: No permission to access the directory") {
		t.Fatal("resolved refresh error remained visible")
	}
}
