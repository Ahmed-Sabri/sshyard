//go:build !windows

package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/ai/audit"
	"github.com/Licoy/sshyard/internal/sshtest"
)

// mcpClient calls the endpoint as an agent would.
type mcpClient struct {
	t     *testing.T
	url   string
	token string
	id    int
}

func (c *mcpClient) post(method string, params any) (int, map[string]any) {
	c.t.Helper()
	c.id++
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params})
	req, _ := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

// call runs a tool and returns its text and whether it failed.
func (c *mcpClient) call(name string, args map[string]any) (string, bool) {
	c.t.Helper()
	code, out := c.post("tools/call", map[string]any{"name": name, "arguments": args})
	res, ok := out["result"].(map[string]any)
	if code != http.StatusOK || !ok {
		c.t.Fatalf("%s: %d %v", name, code, out)
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	return text, res["isError"] == true
}

func TestMCPConnectAndRun(t *testing.T) {
	a, tt, srv, h := bashHost(t)
	h.Group = "prod"
	if _, err := a.Store().Update(h); err != nil {
		t.Fatal(err)
	}
	asked := addHost(t, a, srv, "asked", "")
	untrusted, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(untrusted.Close)
	stranger := addHost(t, a, untrusted, "stranger", "secret")

	s := a.Settings()
	s.MCPPort = freePort(t)
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if err := a.setMCP(true); err != nil || a.mcp.srv == nil {
		t.Fatalf("start: %v %v", err, a.mcp.err)
	}
	c := &mcpClient{t: t, url: a.mcp.srv.URL(), token: a.mcp.token}
	var seen strings.Builder

	if code, _ := (&mcpClient{t: t, url: c.url, token: "wrong"}).post("tools/list", nil); code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", code)
	}
	code, out := c.post("tools/list", nil)
	if code != http.StatusOK {
		t.Fatalf("tools/list %d", code)
	}
	var names []string
	for _, tl := range out["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "list_groups,list_hosts,connect,run_command,list_sessions,disconnect" {
		t.Fatalf("tools %v", names)
	}

	text, _ := c.call("list_groups", nil)
	seen.WriteString(text)
	if !strings.Contains(text, `"name": "prod"`) || !strings.Contains(text, `"ungrouped_hosts": 2`) {
		t.Fatalf("groups %s", text)
	}
	text, _ = c.call("list_hosts", map[string]any{"group": "prod"})
	seen.WriteString(text)
	var hosts struct{ Hosts []mcpHostInfo }
	if err := json.Unmarshal([]byte(text), &hosts); err != nil || len(hosts.Hosts) != 1 || hosts.Hosts[0].ID != h.ID || hosts.Hosts[0].Address != "127.0.0.1" {
		t.Fatalf("hosts %s", text)
	}

	if text, failed := c.call("connect", map[string]any{"host_id": asked.ID}); !failed || !strings.Contains(text, "refused") {
		t.Fatalf("a host asking for a password: %v %s", failed, text)
	}
	if text, failed := c.call("connect", map[string]any{"host_id": stranger.ID}); !failed || !strings.Contains(text, "not trusted") {
		t.Fatalf("unknown host key: %v %s", failed, text)
	}
	text, failed := c.call("connect", map[string]any{"host_id": h.ID})
	seen.WriteString(text)
	var sess struct {
		SessionID string `json:"session_id"`
	}
	if failed || json.Unmarshal([]byte(text), &sess) != nil || sess.SessionID == "" {
		t.Fatalf("connect %v %s", failed, text)
	}
	tt.SetSize(1100, 2600)
	a.openSettings(prefAgents)
	tt.Frame()
	if !tt.HasText("Agent connections") || tt.HasText("No agent has an SSH connection open.") || !tt.HasText("Disconnect Agent Connection") {
		t.Fatalf("session row: %q", tt.Texts())
	}

	text, failed = c.call("run_command", map[string]any{"session_id": sess.SessionID, "command": "echo hello; echo oops >&2; exit 3"})
	seen.WriteString(text)
	var ran struct {
		ExitCode int    `json:"exit_code"`
		Output   string `json:"output"`
	}
	if failed || json.Unmarshal([]byte(text), &ran) != nil || ran.ExitCode != 3 || !strings.Contains(ran.Output, "hello") || !strings.Contains(ran.Output, "oops") {
		t.Fatalf("run %v %q", failed, text)
	}
	if text, failed := c.call("run_command", map[string]any{"session_id": "nope", "command": "true"}); !failed || !strings.Contains(text, "connect first") {
		t.Fatalf("unknown session %v %s", failed, text)
	}
	text, _ = c.call("list_sessions", nil)
	seen.WriteString(text)
	if !strings.Contains(text, sess.SessionID) || len(a.mcpSessionList()) != 1 {
		t.Fatalf("sessions %s", text)
	}
	if strings.Contains(seen.String(), "secret") || strings.Contains(seen.String(), "demo") {
		t.Fatalf("a secret or user reached the agent: %s", seen.String())
	}

	recs, err := audit.New(a.p.DataDir).Read(audit.Filter{Host: h.ID})
	if err != nil || len(recs) != 2 || recs[0].Verdict != "started" || recs[1].Agent != "mcp" || recs[1].Command != "echo hello; echo oops >&2; exit 3" || recs[1].ExitCode == nil || *recs[1].ExitCode != 3 {
		t.Fatalf("audit %+v %v", recs, err)
	}

	// The open API calls the same tools, on the same sessions.
	api := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, a.mcp.srv.Base()+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	if code, out := api(http.MethodPost, "/api/v1/run_command", `{"session_id":"`+sess.SessionID+`","command":"echo via-api"}`); code != http.StatusOK ||
		out["result"].(map[string]any)["exit_code"] != float64(0) || !strings.Contains(out["result"].(map[string]any)["output"].(string), "via-api") {
		t.Fatalf("api run %d %v", code, out)
	}
	if code, out := api(http.MethodPost, "/api/v1/connect", `{"host_id":"`+asked.ID+`"}`); code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "refused") {
		t.Fatalf("api refusal %d %v", code, out)
	}
	if code, out := api(http.MethodGet, "/api/v1", ""); code != http.StatusOK || len(out["tools"].([]any)) != 6 {
		t.Fatalf("api tools %d %v", code, out)
	}
	req, _ := http.NewRequest(http.MethodGet, a.mcp.srv.Base()+"/skill", nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	skill, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(skill) != a.mcp.skill || !strings.Contains(string(skill), a.mcp.srv.Base()+"/api/v1") || !strings.Contains(string(skill), c.token) {
		t.Fatalf("skill %q", skill)
	}

	if text, failed := c.call("disconnect", map[string]any{"session_id": sess.SessionID}); failed || len(a.mcpSessionList()) != 0 {
		t.Fatalf("disconnect %v %s", failed, text)
	}

	// A new token cuts off the old one and drops the connections.
	if _, failed := c.call("connect", map[string]any{"host_id": h.ID}); failed {
		t.Fatal("reconnect")
	}
	old := c.token
	if err := a.newMCPToken(); err != nil {
		t.Fatal(err)
	}
	if a.mcp.token == old || len(a.mcpSessionList()) != 0 {
		t.Fatal("new token kept the old one or its sessions")
	}
	c.url = a.mcp.srv.URL()
	if code, _ := c.post("tools/list", nil); code != http.StatusUnauthorized {
		t.Fatalf("old token: %d", code)
	}

	if err := a.setMCP(false); err != nil || a.mcp.srv != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := http.Get(c.url); err == nil {
		t.Fatal("still listening")
	}
}
