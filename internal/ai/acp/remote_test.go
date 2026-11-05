package acp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/ai/policy"

	sdk "github.com/coder/acp-go-sdk"
)

func TestRemoteToolsRunThroughGate(t *testing.T) {
	files := &memFiles{}
	files.seed("/home/me/note", "a\nb\n")
	ex := &fakeExec{out: "listing\n"}
	gate := &fixedGate{run: true}
	script := "mcp-http\n---\n" +
		`mcp-call remote_run {"command":"ls -l"}` + "\n" +
		`mcp-call remote_read_file {"path":"note"}` + "\n" +
		`mcp-call remote_write_file {"path":"/home/me/out","content":"hi"}` + "\n"
	s := mustStart(t, script, Config{Gate: gate, Files: files, Exec: ex})
	if _, err := s.Prompt(t.Context(), "go", nil); err != nil {
		t.Fatal(err)
	}
	d := waitDiag(t, s, "wrote /home/me/out")
	if !strings.Contains(d, `"name":"sshyard"`) || !strings.Contains(d, `exit code: 0\nlisting`) || !strings.Contains(d, `a\nb`) || !strings.Contains(d, "wrote /home/me/out") {
		t.Fatalf("diag %s", d)
	}
	if got, _ := files.get("/home/me/out"); got != "hi" {
		t.Fatalf("written %q", got)
	}
	actions, _, dones := gate.snapshot()
	var tools []string
	for _, a := range actions {
		if a.Where != policy.Remote {
			t.Fatalf("action not remote: %+v", a)
		}
		tools = append(tools, a.Tool)
	}
	if !slices.Equal(tools, []string{"run_command", "read_file", "read_file", "write_file"}) || actions[0].Command != "ls -l" || actions[0].Dir != "/home/me" || actions[1].Path != "/home/me/note" {
		t.Fatalf("actions %+v", actions)
	}
	if len(dones) != 4 {
		t.Fatalf("dones %+v", dones)
	}

	refused := &fixedGate{run: false, refusal: "not here"}
	ex2 := &fakeExec{}
	s2 := mustStart(t, "mcp-http\n---\n"+`mcp-call remote_run {"command":"rm -rf /"}`+"\n", Config{Gate: refused, Exec: ex2})
	if _, err := s2.Prompt(t.Context(), "go", nil); err != nil {
		t.Fatal(err)
	}
	d = waitDiag(t, s2, "mcp-result")
	if !strings.Contains(d, `"isError":true`) || !strings.Contains(d, "not here") || len(ex2.cmds) != 0 {
		t.Fatalf("refused diag %s cmds %v", d, ex2.cmds)
	}

	// Outside a turn a call has nothing to belong to.
	if _, err := s2.toolRun(t.Context(), json.RawMessage(`{"command":"ls"}`)); err == nil {
		t.Fatal("tool ran outside a turn")
	}
}

func TestRemoteServerOnlyWhenAgentTakesHTTP(t *testing.T) {
	s := mustStart(t, "", Config{Gate: &fixedGate{run: true}})
	if d := waitDiag(t, s, "mcp "); !strings.Contains(d, "mcp []") {
		t.Fatalf("diag %s", d)
	}
}

func TestNewSessionRequestByKind(t *testing.T) {
	srv := &sdk.McpServer{Http: &sdk.McpServerHttpInline{Name: RemoteServer, Type: "http", Url: "http://127.0.0.1:1/mcp"}}
	cl := newSessionRequest("/w", "claude", srv)
	if len(cl.McpServers) != 1 {
		t.Fatalf("claude servers %+v", cl.McpServers)
	}
	b, _ := json.Marshal(cl.Meta)
	var meta struct {
		SystemPrompt struct{ Append string } `json:"systemPrompt"`
		ClaudeCode   struct {
			Options struct {
				Tools        []string `json:"tools"`
				Strict       bool     `json:"strictMcpConfig"`
				AllowedTools []string `json:"allowedTools"`
				Skip         bool     `json:"allowDangerouslySkipPermissions"`
			} `json:"options"`
		} `json:"claudeCode"`
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		t.Fatal(err)
	}
	o := meta.ClaudeCode.Options
	if meta.SystemPrompt.Append != Instructions || !o.Strict || o.Skip || slices.Contains(o.Tools, "Bash") || slices.Contains(o.Tools, "Read") ||
		!slices.Contains(o.AllowedTools, "mcp__sshyard__remote_run") || len(o.AllowedTools) != len(remoteToolNames) {
		t.Fatalf("claude meta %s", b)
	}

	cx := newSessionRequest("/w", "codex", srv)
	if len(cx.McpServers) != 0 || cx.Meta != nil {
		t.Fatalf("codex request %+v", cx)
	}
	other := newSessionRequest("/w", "grok", srv)
	if len(other.McpServers) != 1 || other.Meta != nil {
		t.Fatalf("other request %+v", other)
	}
}

func TestCodexConfigMerges(t *testing.T) {
	got := codexConfig(`{"model":"m","features":{"web_search":true,"shell_tool":true}}`, "http://127.0.0.1:9/mcp", "tok", []string{"cua", RemoteServer})
	var cfg map[string]any
	if err := json.Unmarshal([]byte(got), &cfg); err != nil {
		t.Fatal(err)
	}
	features := cfg["features"].(map[string]any)
	servers := cfg["mcp_servers"].(map[string]any)
	own := servers[RemoteServer].(map[string]any)
	if cfg["model"] != "m" || features["web_search"] != true || features["shell_tool"] != false || features["unified_exec"] != false ||
		servers["cua"].(map[string]any)["enabled"] != false || own["url"] != "http://127.0.0.1:9/mcp" ||
		own["http_headers"].(map[string]any)["Authorization"] != "Bearer tok" || cfg["developer_instructions"] != Instructions {
		t.Fatalf("config %s", got)
	}
	if bad := codexConfig("not json", "u", "t", nil); !strings.Contains(bad, `"shell_tool":false`) {
		t.Fatalf("bad base %s", bad)
	}
	// Probing serves no remote tools but still keeps Codex's own off.
	if probe := codexConfig("", "", "", []string{"cua"}); strings.Contains(probe, `"sshyard"`) || !strings.Contains(probe, `"cua":{"enabled":false}`) {
		t.Fatalf("probe config %s", probe)
	}
}

func TestCodexServersFromConfigFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	body := "model = \"x\"\n[mcp_servers.cua]\ncommand = \"node\"\n[mcp_servers.cua.env]\nA = \"1\"\n[mcp_servers.\"a.b\"]\n[ mcp_servers.spaced ]\n[profiles.x]\n[mcp_servers.]\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := codexServers(p); !slices.Equal(got, []string{"cua", "a.b"}) {
		t.Fatalf("servers %q", got)
	}
	if got := codexServers(filepath.Join(t.TempDir(), "missing.toml")); got != nil {
		t.Fatalf("missing %q", got)
	}
}

func TestCodexGetsConfigAndNoPromptInstructions(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[mcp_servers.cua]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := mustStart(t, "---\nenv CODEX_CONFIG\n", Config{Type: "codex", Gate: &fixedGate{run: true}, Env: map[string]string{"CODEX_HOME": home}})
	if _, err := s.Prompt(t.Context(), "hi", nil); err != nil {
		t.Fatal(err)
	}
	d := waitDiag(t, s, "env CODEX_CONFIG=")
	if !strings.Contains(d, `"shell_tool":false`) || !strings.Contains(d, `"cua":{"enabled":false}`) || !strings.Contains(d, `"sshyard":{`) {
		t.Fatalf("diag %s", d)
	}
	if strings.Contains(d, "sshyard://instructions") || !strings.Contains(d, "prompt text\n") {
		t.Fatalf("codex prompt carried instructions: %s", d)
	}
}

func TestInstructionsOnlyBeforeFirstRequest(t *testing.T) {
	s := mustStart(t, "", Config{Gate: &fixedGate{run: true}})
	for range 2 {
		if _, err := s.Prompt(t.Context(), "hi", nil); err != nil {
			t.Fatal(err)
		}
	}
	d := waitDiag(t, s, "prompt text\n")
	if strings.Count(d, "sshyard://instructions") != 1 || !strings.Contains(d, "prompt resource text\n") {
		t.Fatalf("diag %s", d)
	}
}

func TestOptionsSetPreferAndProbe(t *testing.T) {
	script := "model mdl v1 v1 v2\neffort eff low low high\n"
	s := mustStart(t, script, Config{Gate: &fixedGate{run: true}})
	opts := s.Options()
	if len(opts) != 2 || opts[0].Category != CategoryModel || opts[1].Category != CategoryThought || opts[0].Current != "v1" || len(opts[1].Values) != 2 || opts[1].Values[1].Name != "HIGH" {
		t.Fatalf("options %+v", opts)
	}
	if _, err := s.SetOption(t.Context(), "eff", "max"); !errors.Is(err, errInvalid) {
		t.Fatalf("unknown value: %v", err)
	}
	if _, err := s.SetOption(t.Context(), "nope", "x"); err == nil {
		t.Fatal("unknown option set")
	}
	got, err := s.SetOption(t.Context(), "eff", "high")
	if o, _ := Find(got, "eff"); err != nil || o.Current != "high" {
		t.Fatalf("set %+v %v", got, err)
	}
	if err := s.Prefer(t.Context(), map[string]string{"mdl": "v2", "eff": "gone", "x": "y"}); err != nil {
		t.Fatal(err)
	}
	d := waitDiag(t, s, "set_config mdl=v2")
	if !strings.Contains(d, "set_config mdl=v2") || strings.Contains(d, "eff=gone") {
		t.Fatalf("diag %s", d)
	}
	if o, _ := Find(s.Options(), "mdl"); o.Current != "v2" {
		t.Fatalf("after prefer %+v", s.Options())
	}

	probed, err := Probe(t.Context(), Config{Type: "custom", Command: fakeBin, Args: []string{scriptFile(t, script)}, RemoteHome: "/home/me"})
	if err != nil || len(probed) != 2 || probed[0].ID != "mdl" {
		t.Fatalf("probe %+v %v", probed, err)
	}
}

func TestToolViewLocal(t *testing.T) {
	for _, c := range []struct {
		v     ToolView
		local bool
	}{
		{ToolView{Kind: "execute", Title: "ls -l"}, true},
		{ToolView{Kind: "search", Title: "grep foo"}, true},
		{ToolView{Kind: "execute", Title: "ls", TerminalID: "t1"}, false},
		{ToolView{Kind: "execute", Title: "mcp__sshyard__remote_run"}, false},
		{ToolView{Kind: "read", Title: "Read /etc/hosts"}, false},
		{ToolView{Kind: "edit", Title: "Edit a.go"}, false},
	} {
		if got := c.v.Local(); got != c.local {
			t.Fatalf("%+v local %v", c.v, got)
		}
	}
}
