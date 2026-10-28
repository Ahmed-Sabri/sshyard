package openai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConnectionProbeAcceptsOnlyExplicitTextTokenLimit(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		ok   bool
	}{
		"SSE length":                  {body: "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n", ok: true},
		"JSON length":                 {body: `{"choices":[{"message":{"content":"ok"},"finish_reason":"length"}]}`, ok: true},
		"truncated SSE":               {body: "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"},
		"DONE missing finish":         {body: "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\ndata: [DONE]\n\n"},
		"truncated JSON":              {body: `{"choices":[{"message":{"content":"partial"}}]}`},
		"SSE tools length":            {body: "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"x\",\"function\":{\"name\":\"run_command\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n"},
		"JSON tools length":           {body: `{"choices":[{"message":{"tool_calls":[{"id":"x","function":{"name":"run_command","arguments":"{}"}}]},"finish_reason":"length"}]}`},
		"SSE length without delta":    {body: "data: {\"choices\":[{\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n"},
		"JSON length without message": {body: `{"choices":[{"finish_reason":"length"}]}`},
		"no choices":                  {body: `{"choices":[],"finish_reason":"length"}`},
		"invalid SSE tail":            {body: "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\ndata: {broken\n\n"},
		"malformed JSON":              {body: `{"choices":[{"message":{},"finish_reason":"length"}]`},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer r.Body.Close()
				var req struct {
					MaxTokens int               `json:"max_tokens"`
					Tools     []json.RawMessage `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.MaxTokens != 1 || len(req.Tools) != 0 {
					t.Errorf("probe request %+v", req)
				}
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			c := &Client{BaseURL: srv.URL, Model: "probe-model", APIKey: "test-key-not-real"}
			err := c.Test(t.Context())
			if (err == nil) != tc.ok {
				t.Fatalf("probe error=%v want success=%v", err, tc.ok)
			}
		})
	}
}

func TestNormalStreamRejectsExplicitTokenLimitWithoutTools(t *testing.T) {
	for _, body := range []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n",
		`{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}]}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		c := &Client{BaseURL: srv.URL, Model: "probe-model", APIKey: "test-key-not-real"}
		msg, err := c.Stream(t.Context(), Request{Messages: []Message{{Role: "user", Content: "run"}}}, nil)
		srv.Close()
		if !errors.Is(err, ErrTokenLimit) || !errors.Is(err, ErrIncompleteResponse) {
			t.Fatalf("normal stream accepted token limit: %v", err)
		}
		if msg.Content != "" || len(msg.ToolCalls) != 0 {
			t.Fatalf("incomplete stream returned executable message: %+v", msg)
		}
	}
}
