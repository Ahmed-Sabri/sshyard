package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAIDefaults(t *testing.T) {
	s := Default()
	if s.AIDefaultAgent != "builtin" || s.AIDefaultMode != "ask" || s.AIReviewProvider != "" {
		t.Fatalf("defaults %+v", s)
	}
	if s.AIContextLines != 200 || s.AIAuditRetentionDays != 90 || !s.AIRedact {
		t.Fatalf("defaults %+v", s)
	}
	if len(s.AIProviders) != 0 || len(s.AIConsentHosts) != 0 {
		t.Fatalf("defaults %+v", s)
	}
}

func TestAINormalize(t *testing.T) {
	s := Default()
	s.AIDefaultAgent = "  "
	s.AIDefaultMode = "nope"
	s.AIContextLines = 3
	s.AIAuditRetentionDays = 0
	s.AIReviewProvider = "missing"
	s.AIProviders = []AIProvider{{ID: "p", Name: "n", BaseURL: "https://example.test/v1", Model: "m"}}
	s.Normalize()
	if s.AIDefaultAgent != "builtin" || s.AIDefaultMode != "ask" || s.AIContextLines != 50 || s.AIAuditRetentionDays != 90 {
		t.Fatalf("normalized %+v", s)
	}
	if s.AIReviewProvider != "" {
		t.Fatalf("unknown review provider kept: %q", s.AIReviewProvider)
	}

	// "full" from a file is kept; the UI is what requires the confirmation.
	s = Default()
	s.AIDefaultMode = "full"
	s.AIContextLines = 5000
	s.AIAuditRetentionDays = 30
	s.AIRedact = false
	s.Normalize()
	if s.AIDefaultMode != "full" || s.AIContextLines != 2000 || s.AIAuditRetentionDays != 30 || s.AIRedact {
		t.Fatalf("kept %+v", s)
	}

	// A file that never mentions the AI fields keeps the defaults.
	p := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(p, []byte(`{"theme":"dark"}`), 0o600)
	got, err := Load(p)
	if err != nil || got.AIDefaultAgent != "builtin" || got.AIDefaultMode != "ask" || got.AIContextLines != 200 || !got.AIRedact || got.AIAuditRetentionDays != 90 {
		t.Fatalf("old file %+v %v", got, err)
	}
}

func TestAIProviderRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	s := Default()
	s.AIDefaultAgent = "builtin"
	s.AIDefaultMode = "review"
	s.AIReviewProvider = "p1"
	s.AIContextLines = 80
	s.AIConsentHosts = []string{"h1"}
	s.AIProviders = []AIProvider{{
		ID: "p1", Name: "lab", BaseURL: "https://models.example.test/v1", Model: "fake-model",
		Headers: map[string]string{"X-Title": "SSHYard"}, Temperature: 0.2, MaxTokens: 400,
	}}
	if err := Save(p, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil || got.AIDefaultMode != "review" || got.AIReviewProvider != "p1" || got.AIContextLines != 80 {
		t.Fatalf("load %+v %v", got, err)
	}
	if len(got.AIProviders) != 1 || got.AIProviders[0].Headers["X-Title"] != "SSHYard" || got.AIProviders[0].Model != "fake-model" {
		t.Fatalf("provider %+v", got.AIProviders)
	}
	if len(got.AIConsentHosts) != 1 || got.AIConsentHosts[0] != "h1" {
		t.Fatalf("consent %+v", got.AIConsentHosts)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "apikey") || strings.Contains(string(b), "secret") {
		t.Fatalf("secret-shaped field in settings: %s", b)
	}
}

func TestProviderURLAndHeaders(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.test", "https://", "http://", "/v1", "https://user:pw@example.test/v1"} {
		if _, err := CheckBaseURL(raw); err == nil {
			t.Errorf("%q accepted", raw)
		}
	}
	if warn, err := CheckBaseURL("https://api.example.test/v1"); err != nil || warn != nil {
		t.Fatalf("https: warn %v err %v", warn, err)
	}
	if warn, err := CheckBaseURL("http://127.0.0.1:9/v1"); err != nil || warn != nil {
		t.Fatalf("loopback http: warn %v err %v", warn, err)
	}
	if warn, err := CheckBaseURL("http://api.example.test/v1"); err != nil || warn == nil {
		t.Fatalf("public http: warn %v err %v", warn, err)
	}

	if err := CheckHeaders(map[string]string{"X-Title": "SSHYard"}); err != nil {
		t.Fatal(err)
	}
	if err := CheckHeaders(map[string]string{"X-Bad": "a\nb"}); err == nil {
		t.Fatal("newline header accepted")
	}
	if err := CheckHeaders(map[string]string{"Authorization": "Bearer test-key-not-real"}); err == nil {
		t.Fatal("Authorization header accepted")
	}
	if err := CheckHeaders(map[string]string{"authorization": "x"}); err == nil {
		t.Fatal("authorization header accepted")
	}

	s := Default()
	s.AIProviders = []AIProvider{{
		ID: "p", Name: "n", BaseURL: " https://api.example.test/v1 ", Model: " m ",
		Headers: map[string]string{"Authorization": "Bearer test-key-not-real", "X-Ok": "v", "X-Bad": "a\r\nb"},
	}}
	s.Normalize()
	h := s.AIProviders[0].Headers
	if s.AIProviders[0].BaseURL != "https://api.example.test/v1" || s.AIProviders[0].Model != "m" {
		t.Fatalf("trimmed %+v", s.AIProviders[0])
	}
	if _, ok := h["Authorization"]; ok {
		t.Fatalf("Authorization kept: %+v", h)
	}
	if _, ok := h["X-Bad"]; ok || h["X-Ok"] != "v" {
		t.Fatalf("headers %+v", h)
	}
}
