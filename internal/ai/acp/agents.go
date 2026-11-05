package acp

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/coder/acp-go-sdk"
)

// What each kind of agent is told so that its work happens on the remote
// host through SSHYard and not with its own tools on this computer.
//
//   - claude: its local tools are off except web access and its to-do list,
//     only the MCP servers given here are loaded, the remote tools need no
//     second approval (the gate already decides), and Instructions are
//     appended to its system prompt.
//   - codex: it uses neither client files nor terminals, so CODEX_CONFIG
//     turns its local shell, plugins, apps and memories off, serves it the
//     remote tools without a second approval, disables the MCP servers of
//     the user's own configuration, and makes Instructions its developer
//     instructions. The remote tools reach it through that configuration,
//     not session/new, which would replace the whole server table.
//   - any other agent gets the remote tools in session/new when it takes
//     HTTP servers, and Instructions before its first request.

// claudeTools are the tools Claude keeps: none touches this computer.
var claudeTools = []string{"WebFetch", "WebSearch", "TodoWrite"}

// codexOff are the Codex features that act on this computer.
var codexOff = []string{"shell_tool", "unified_exec", "plugins", "apps", "memories"}

// ownPrompt says whether the agent kind takes Instructions in its system
// prompt, so the first request need not carry them.
func ownPrompt(kind string) bool { return kind == "claude" || kind == "codex" }

func newSessionRequest(cwd, kind string, server *sdk.McpServer) sdk.NewSessionRequest {
	req := sdk.NewSessionRequest{Cwd: cwd, McpServers: []sdk.McpServer{}}
	if server != nil && kind != "codex" {
		req.McpServers = append(req.McpServers, *server)
	}
	if kind == "claude" {
		opts := map[string]any{
			"allowDangerouslySkipPermissions": false,
			"tools":                           claudeTools,
			"strictMcpConfig":                 true,
		}
		if server != nil {
			allowed := make([]string, 0, len(remoteToolNames))
			for _, name := range remoteToolNames {
				allowed = append(allowed, "mcp__"+RemoteServer+"__"+name)
			}
			opts["allowedTools"] = allowed
		}
		req.Meta = map[string]any{
			"systemPrompt": map[string]any{"append": Instructions},
			"claudeCode":   map[string]any{"options": opts},
		}
	}
	return req
}

// bridgeServer is the remote tools' entry for session/new, or nil when
// they are not served or the agent cannot reach an HTTP server.
func (s *Session) bridgeServer() *sdk.McpServer {
	if s.bridge == nil || !s.mcpHTTP {
		return nil
	}
	return &sdk.McpServer{Http: &sdk.McpServerHttpInline{
		Name:    RemoteServer,
		Type:    "http",
		Url:     s.bridge.URL(),
		Headers: []sdk.HttpHeader{{Name: "Authorization", Value: "Bearer " + s.bridgeToken}},
	}}
}

// agentEnv is the agent's own variables with what its kind needs added.
func (s *Session) agentEnv() map[string]string {
	env := make(map[string]string, len(s.cfg.Env)+1)
	for k, v := range s.cfg.Env {
		env[k] = v
	}
	if s.cfg.Type == "codex" {
		home := env["CODEX_HOME"]
		if home == "" {
			if dir, err := os.UserHomeDir(); err == nil {
				home = filepath.Join(dir, ".codex")
			}
		}
		var url string
		if s.bridge != nil {
			url = s.bridge.URL()
		}
		env["CODEX_CONFIG"] = codexConfig(env["CODEX_CONFIG"], url, s.bridgeToken, codexServers(filepath.Join(home, "config.toml")))
	}
	return env
}

// codexConfig adds SSHYard's settings to the user's CODEX_CONFIG, if any:
// theirs stay unless SSHYard sets the same key. Without a url, as when
// only probed, the remote tools are not served.
func codexConfig(base, url, token string, userServers []string) string {
	cfg := map[string]any{}
	if base != "" {
		_ = json.Unmarshal([]byte(base), &cfg)
		if cfg == nil {
			cfg = map[string]any{}
		}
	}
	section := func(key string) map[string]any {
		m, _ := cfg[key].(map[string]any)
		if m == nil {
			m = map[string]any{}
			cfg[key] = m
		}
		return m
	}
	features := section("features")
	for _, f := range codexOff {
		features[f] = false
	}
	servers := section("mcp_servers")
	for _, name := range userServers {
		if name != RemoteServer {
			servers[name] = map[string]any{"enabled": false}
		}
	}
	if url != "" {
		servers[RemoteServer] = map[string]any{
			"url":                         url,
			"http_headers":                map[string]string{"Authorization": "Bearer " + token},
			"default_tools_approval_mode": "approve",
		}
	}
	cfg["developer_instructions"] = Instructions
	b, _ := json.Marshal(cfg)
	return string(b)
}

// codexServers are the MCP server names a Codex config file declares, from
// its [mcp_servers.<name>] tables. A missing file declares none.
func codexServers(file string) []string {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()
	var names []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		rest, ok := strings.CutPrefix(line, "[mcp_servers.")
		if !ok {
			continue
		}
		var name string
		if q, ok := strings.CutPrefix(rest, `"`); ok {
			name, _, _ = strings.Cut(q, `"`)
		} else if q, ok := strings.CutPrefix(rest, "'"); ok {
			name, _, _ = strings.Cut(q, "'")
		} else if f := strings.FieldsFunc(rest, func(r rune) bool { return r == '.' || r == ']' }); len(f) > 0 {
			name = strings.TrimSpace(f[0])
		}
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}
