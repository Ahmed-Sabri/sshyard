package llm

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var safetyQuestions = []Question{
	{Name: "safe", Instructions: "Is it safe?", Yes: "harmless", No: "harmful"},
	{Name: "risk", Instructions: "Which risk?", Choices: []Choice{{Value: "none", Description: "no risk"}, {Value: "destructive"}}},
}

func TestDecideOpenAI(t *testing.T) {
	srv, got, payload := serve(t, `{"model":"gpt-6-luna","answers":[{"type":"predicate","name":"safe","probability":0.93},{"type":"choice","name":"risk","choice":"none","confidence":0.8,"probabilities":[{"value":"none","probability":0.9}]}],"usage":{"input_tokens":10}}`)
	c := &Client{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "gpt-6-luna"}
	ans, err := c.Decide(t.Context(), "rm -rf build", safetyQuestions)
	if err != nil {
		t.Fatal(err)
	}
	if ans["safe"].Probability != 0.93 || ans["risk"].Choice != "none" || ans["risk"].Confidence != 0.8 {
		t.Fatalf("answers %+v", ans)
	}
	if got.URL.Path != "/v1/decisions" || got.Header.Get("Authorization") != "Bearer k" || got.Header.Get("Accept") != "application/json" {
		t.Fatalf("request %s %v", got.URL.Path, got.Header)
	}
	if s := string(*payload); !strings.Contains(s, `"choices":[{"description":"no risk","value":"none"},{"value":"destructive"}]`) {
		t.Fatalf("choices %s", s)
	}
	m := decode(t, *payload)
	qs := m["questions"].([]any)
	if m["model"] != "gpt-6-luna" || m["input"] != "rm -rf build" || len(qs) != 2 {
		t.Fatalf("payload %s", *payload)
	}
	if q := qs[0].(map[string]any); q["type"] != "predicate" || q["name"] != "safe" || q["instructions"] != "Is it safe?" {
		t.Fatalf("predicate %v", q)
	}
	if q := qs[1].(map[string]any); q["type"] != "choice" || q["name"] != "risk" {
		t.Fatalf("choice %v", q)
	}
}

func TestDecideTypeSafe(t *testing.T) {
	srv, got, payload := serve(t, `{"model":"jev-1.14.0","answers":{"safe":{"type":"noul","noul":0.41},"risk":{"type":"choice","choice":"destructive","confidence":0.7,"probabilities":{"none":0.2,"destructive":0.8}}}}`)
	c := &Client{Format: FormatTypeSafe, BaseURL: srv.URL + "/v1", APIKey: "k", Model: "jev-latest"}
	ans, err := c.Decide(t.Context(), "rm -rf build", safetyQuestions)
	if err != nil {
		t.Fatal(err)
	}
	if ans["safe"].Probability != 0.41 || ans["risk"].Choice != "destructive" {
		t.Fatalf("answers %+v", ans)
	}
	if got.URL.Path != "/v1/systemone" || got.Header.Get("Authorization") != "Bearer k" {
		t.Fatalf("request %s %v", got.URL.Path, got.Header)
	}
	m := decode(t, *payload)
	qs := m["questions"].(map[string]any)
	safe, risk := qs["safe"].(map[string]any), qs["risk"].(map[string]any)
	if m["state"] != "rm -rf build" || m["model"] != "jev-latest" || safe["type"] != "noul" || safe["criteria"].(map[string]any)["true"] != "harmless" {
		t.Fatalf("payload %s", *payload)
	}
	if risk["type"] != "choice" || risk["criteria"].(map[string]any)["none"] != "no risk" {
		t.Fatalf("choice %v", risk)
	}
}

func TestDecideRefusalAndBadAnswers(t *testing.T) {
	srv, _, _ := serve(t, `{"answers":[{"type":"refusal","name":"safe"},{"type":"choice","name":"risk","choice":"none","confidence":0.5}]}`)
	c := &Client{BaseURL: srv.URL, APIKey: "k", Model: "m"}
	ans, err := c.Decide(t.Context(), "x", safetyQuestions)
	if err != nil || !ans["safe"].Refused {
		t.Fatalf("refusal %+v %v", ans, err)
	}
	for name, body := range map[string]string{
		"missing":     `{"answers":[{"type":"predicate","name":"safe","probability":0.5}]}`,
		"range":       `{"answers":[{"type":"predicate","name":"safe","probability":1.5},{"type":"choice","name":"risk","choice":"none","confidence":0.5}]}`,
		"choice":      `{"answers":[{"type":"predicate","name":"safe","probability":0.5},{"type":"choice","name":"risk","choice":"other","confidence":0.5}]}`,
		"no estimate": `{"answers":[{"type":"predicate","name":"safe"},{"type":"choice","name":"risk","choice":"none","confidence":0.5}]}`,
	} {
		srv, _, _ := serve(t, body)
		c := &Client{BaseURL: srv.URL, APIKey: "k", Model: "m"}
		if _, err := c.Decide(t.Context(), "x", safetyQuestions); !errors.Is(err, ErrProtocol) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestDecideTypeSafeErrorDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"detail":"unknown model jev-x"}`)
	}))
	defer srv.Close()
	c := &Client{Format: FormatTypeSafe, BaseURL: srv.URL, APIKey: "ts-test-key", Model: "jev-x"}
	_, err := c.Decide(t.Context(), "x", safetyQuestions)
	var api *APIError
	if !errors.As(err, &api) || api.Detail() != "unknown model jev-x" {
		t.Fatalf("%v", err)
	}
}

func TestTypeSafeProbeAndNoChat(t *testing.T) {
	srv, got, payload := serve(t, `{"answers":{"probe":{"type":"noul","noul":0.9}}}`)
	c := &Client{Format: FormatTypeSafe, BaseURL: srv.URL, APIKey: "k", Model: "jev-latest"}
	if err := c.Test(t.Context()); err != nil || got.URL.Path != "/systemone" || !strings.Contains(string(*payload), `"state":"ping"`) {
		t.Fatalf("probe %v %s %s", err, got.URL.Path, *payload)
	}
	if _, err := c.Stream(t.Context(), Request{Messages: []Message{{Role: "user", Content: "hi"}}}, nil); !errors.Is(err, ErrNoChat) {
		t.Fatalf("chat %v", err)
	}
}

func TestListModelsTypeSafe(t *testing.T) {
	for _, body := range []string{`{"data":[{"id":"jev-latest"},{"id":"jev-preview"}]}`, `{"models":["jev-preview","jev-latest"]}`, `["jev-latest",{"name":"jev-preview"}]`} {
		srv, _, _ := serve(t, body)
		c := &Client{Format: FormatTypeSafe, BaseURL: srv.URL, APIKey: "k"}
		ids, err := c.ListModels(t.Context())
		if err != nil || strings.Join(ids, ",") != "jev-latest,jev-preview" {
			t.Fatalf("%s: %v %v", body, ids, err)
		}
	}
}
