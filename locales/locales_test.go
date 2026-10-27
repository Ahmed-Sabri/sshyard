package locales

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func load(t *testing.T, name string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	raw := map[string]any{}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	out := map[string]string{}
	for k, v := range raw {
		if strings.HasPrefix(k, "@") {
			continue
		}
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%s: %s is not a string", name, k)
		}
		out[k] = s
	}
	return out
}

var placeholder = regexp.MustCompile(`\{([a-zA-Z]+)\}`)

func names(s string) []string {
	var out []string
	for _, m := range placeholder.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	slices.Sort(out)
	return out
}

// Every message of the source language has a Chinese translation with the
// same placeholders, and Chinese has no message English lacks.
func TestKeyCoverage(t *testing.T) {
	en, zh := load(t, "en.json"), load(t, "zh-Hans.json")
	if len(en) < 50 {
		t.Fatalf("only %d messages", len(en))
	}
	for k, v := range en {
		z, ok := zh[k]
		if !ok {
			t.Errorf("zh-Hans lacks %q", k)
			continue
		}
		if z == "" {
			t.Errorf("zh-Hans %q is empty", k)
		}
		if !slices.Equal(names(v), names(z)) {
			t.Errorf("%q placeholders: en %v, zh-Hans %v", k, names(v), names(z))
		}
	}
	for k := range zh {
		if _, ok := en[k]; !ok {
			t.Errorf("zh-Hans has %q, which en lacks", k)
		}
	}
}

func TestOnlyTwoLanguages(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var langs []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			langs = append(langs, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	slices.Sort(langs)
	if !slices.Equal(langs, []string{"en", "zh-Hans"}) {
		t.Errorf("languages %v, want en and zh-Hans", langs)
	}
}

func TestGeneratedAccessors(t *testing.T) {
	if got := In("zh-Hans").Hosts(); got != "主机" {
		t.Errorf("zh-Hans Hosts = %q", got)
	}
	if got := In("zh-CN").Settings(); got != "设置" {
		t.Errorf("zh-CN falls back to zh-Hans: Settings = %q", got)
	}
	if got := In("en").ErrAuth("root", "example.com"); !strings.Contains(got, "root@example.com") {
		t.Errorf("en ErrAuth = %q", got)
	}
	if got := In("fr").Connect(); got != "Connect" {
		t.Errorf("fr falls back to en: %q", got)
	}
}
