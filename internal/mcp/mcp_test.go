package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testHandler() *Handler {
	return &Handler{
		Name: "sshyard", Version: "1.0", Token: "secret-token",
		Tools: []Tool{
			{Name: "echo", Description: "echoes", Schema: json.RawMessage(`{"type":"object"}`),
				Call: func(_ context.Context, args json.RawMessage) (string, error) { return string(args), nil }},
			{Name: "fail", Schema: json.RawMessage(`{"type":"object"}`),
				Call: func(context.Context, json.RawMessage) (string, error) { return "", errors.New("it failed") }},
		},
	}
}

func post(t *testing.T, h http.Handler, body string, edit func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4000"+Path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer secret-token")
	if edit != nil {
		edit(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q: %v", w.Body.String(), err)
	}
	return out
}

func TestRefusals(t *testing.T) {
	h := testHandler()
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	for _, tc := range []struct {
		name string
		edit func(*http.Request)
		want int
	}{
		{"no token", func(r *http.Request) { r.Header.Del("Authorization") }, http.StatusUnauthorized},
		{"wrong token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") }, http.StatusUnauthorized},
		{"basic", func(r *http.Request) { r.Header.Set("Authorization", "Basic secret-token") }, http.StatusUnauthorized},
		{"rebound host", func(r *http.Request) { r.Host = "evil.example:4000" }, http.StatusForbidden},
		{"foreign origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, http.StatusForbidden},
		{"form post", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, http.StatusUnsupportedMediaType},
		{"other path", func(r *http.Request) { r.URL.Path = "/" }, http.StatusNotFound},
		{"get", func(r *http.Request) { r.Method = http.MethodGet }, http.StatusMethodNotAllowed},
	} {
		if w := post(t, h, init, tc.edit); w.Code != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, w.Code, tc.want)
		}
	}
	if w := post(t, &Handler{}, init, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("empty token accepted: %d", w.Code)
	}
	if w := post(t, h, init, func(r *http.Request) {
		r.Host = "localhost:4000"
		r.Header.Set("Origin", "http://localhost:6274")
	}); w.Code != http.StatusOK {
		t.Errorf("local origin: %d %s", w.Code, w.Body)
	}
	if w := post(t, h, strings.Repeat(" ", MaxBody+1), nil); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("large body: %d", w.Code)
	}
}

func TestProtocol(t *testing.T) {
	h := testHandler()
	out := decode(t, post(t, h, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`, nil))
	res := out["result"].(map[string]any)
	if res["protocolVersion"] != "2025-06-18" || res["serverInfo"].(map[string]any)["name"] != "sshyard" {
		t.Fatalf("initialize %v", out)
	}
	out = decode(t, post(t, h, `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`, nil))
	if out["result"].(map[string]any)["protocolVersion"] != Versions[0] {
		t.Fatalf("unknown version %v", out)
	}
	if w := post(t, h, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil); w.Code != http.StatusAccepted || w.Body.Len() != 0 {
		t.Fatalf("notification %d %q", w.Code, w.Body)
	}
	out = decode(t, post(t, h, `{"jsonrpc":"2.0","id":"p","method":"ping"}`, nil))
	if out["id"] != "p" || out["result"] == nil {
		t.Fatalf("ping %v", out)
	}
	out = decode(t, post(t, h, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`, nil))
	tools := out["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 || tools[0].(map[string]any)["name"] != "echo" || tools[0].(map[string]any)["inputSchema"] == nil {
		t.Fatalf("tools %v", out)
	}
	out = decode(t, post(t, h, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"a":1}}}`, nil))
	res = out["result"].(map[string]any)
	if res["isError"] != false || res["content"].([]any)[0].(map[string]any)["text"] != `{"a":1}` {
		t.Fatalf("call %v", out)
	}
	out = decode(t, post(t, h, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"fail"}}`, nil))
	res = out["result"].(map[string]any)
	if res["isError"] != true || res["content"].([]any)[0].(map[string]any)["text"] != "it failed" {
		t.Fatalf("failed call %v", out)
	}
	out = decode(t, post(t, h, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"nope"}}`, nil))
	if out["error"].(map[string]any)["code"] != float64(codeParams) {
		t.Fatalf("unknown tool %v", out)
	}
	out = decode(t, post(t, h, `{"jsonrpc":"2.0","id":7,"method":"resources/list"}`, nil))
	if out["error"].(map[string]any)["code"] != float64(codeNoMethod) {
		t.Fatalf("unknown method %v", out)
	}
	if w := post(t, h, `{`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("parse error %d", w.Code)
	}
}

func TestAPIAndSkill(t *testing.T) {
	h := testHandler()
	h.Skill = func() string { return "# skill" }
	do := func(method, path, body string, edit func(*http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://127.0.0.1:4000"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer secret-token")
		if edit != nil {
			edit(r)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	out := decode(t, do(http.MethodGet, APIPath, "", nil))
	if tools := out["tools"].([]any); len(tools) != 2 || tools[1].(map[string]any)["name"] != "fail" {
		t.Fatalf("list %v", out)
	}
	out = decode(t, do(http.MethodPost, APIPath+"/echo", `{"a":1}`, nil))
	if out["result"].(map[string]any)["a"] != float64(1) {
		t.Fatalf("echo %v", out)
	}
	if w := do(http.MethodPost, APIPath+"/echo", "", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"result":{}`) {
		t.Fatalf("no body %d %s", w.Code, w.Body)
	}
	if w := do(http.MethodPost, APIPath+"/fail", `{}`, nil); w.Code != http.StatusBadRequest || decode(t, w)["error"] != "it failed" {
		t.Fatalf("fail %d %s", w.Code, w.Body)
	}
	for _, tc := range []struct {
		method, path, body string
		edit               func(*http.Request)
		want               int
	}{
		{http.MethodPost, APIPath + "/nope", `{}`, nil, http.StatusNotFound},
		{http.MethodGet, APIPath + "/echo", "", nil, http.StatusMethodNotAllowed},
		{http.MethodPost, APIPath + "/echo", `{`, nil, http.StatusBadRequest},
		{http.MethodPost, APIPath + "/echo", `{}`, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, http.StatusUnsupportedMediaType},
		{http.MethodPost, APIPath + "/echo", `{}`, func(r *http.Request) { r.Header.Del("Authorization") }, http.StatusUnauthorized},
		{http.MethodGet, SkillPath, "", func(r *http.Request) { r.Header.Del("Authorization") }, http.StatusUnauthorized},
		{http.MethodGet, SkillPath, "", func(r *http.Request) { r.Host = "evil.example" }, http.StatusForbidden},
		{http.MethodPost, SkillPath, "", nil, http.StatusMethodNotAllowed},
	} {
		if w := do(tc.method, tc.path, tc.body, tc.edit); w.Code != tc.want {
			t.Errorf("%s %s: %d, want %d", tc.method, tc.path, w.Code, tc.want)
		}
	}
	w := do(http.MethodGet, SkillPath, "", nil)
	if w.Code != http.StatusOK || w.Body.String() != "# skill" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/markdown") {
		t.Fatalf("skill %d %q", w.Code, w.Body)
	}
	h.Skill = nil
	if w := do(http.MethodGet, SkillPath, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("no skill %d", w.Code)
	}
}

func TestListenLoopbackOnly(t *testing.T) {
	s, err := Listen(0, testHandler())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !strings.HasPrefix(s.URL(), "http://127.0.0.1:") || s.Port() == 0 {
		t.Fatalf("url %q", s.URL())
	}
	req, _ := http.NewRequest(http.MethodPost, s.URL(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"result"`) {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	if _, err := Listen(70000, testHandler()); !errors.Is(err, ErrBadPort) {
		t.Fatalf("bad port %v", err)
	}
}
