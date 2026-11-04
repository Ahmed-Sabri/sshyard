package locales

import (
	"cmp"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/egoist/mygo/i18n"
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

func names(t *testing.T, s string) []i18n.Arg {
	t.Helper()
	m, err := i18n.ParseMessage(s)
	if err != nil {
		t.Fatal(err)
	}
	args := m.Args()
	slices.SortFunc(args, func(a, b i18n.Arg) int { return cmp.Compare(a.Name, b.Name) })
	return args
}

// Every catalog must translate every source key, preserving ICU argument names and kinds.
func TestKeyCoverage(t *testing.T) {
	en := load(t, "en.json")
	if len(en) < 1000 {
		t.Fatalf("only %d messages", len(en))
	}
	for _, lang := range settings.Languages {
		t.Run(lang, func(t *testing.T) {
			translated := load(t, lang+".json")
			for k, v := range en {
				z, ok := translated[k]
				if !ok {
					t.Errorf("%s lacks %q", lang, k)
					continue
				}
				if strings.TrimSpace(z) == "" {
					t.Errorf("%s %q is empty", lang, k)
				}
				if !slices.Equal(names(t, v), names(t, z)) {
					t.Errorf("%q arguments: en %v, %s %v", k, names(t, v), lang, names(t, z))
				}
			}
			for k := range translated {
				if _, ok := en[k]; !ok {
					t.Errorf("%s has extra key %q", lang, k)
				}
			}
		})
	}
}

func TestSupportedLanguages(t *testing.T) {
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
	if !slices.Equal(langs, []string{"en", "ja", "ko", "zh-Hans", "zh-Hant"}) {
		t.Errorf("languages %v, want all five supported languages", langs)
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

func TestRegionalLanguages(t *testing.T) {
	for _, tc := range []struct{ region, language string }{{"zh-TW", "zh-Hant"}, {"zh-HK", "zh-Hant"}, {"zh-CN", "zh-Hans"}, {"ja-JP", "ja"}, {"ko-KR", "ko"}} {
		t.Run(tc.region, func(t *testing.T) {
			if got, want := In(tc.region).Settings(), In(tc.language).Settings(); got != want || got == In("en").Settings() {
				t.Errorf("Settings = %q, want %q", got, want)
			}
		})
	}
}

func TestNativeControlTranslations(t *testing.T) {
	for _, lang := range settings.Languages {
		messages := load(t, lang+".json")
		if got := Catalog.Format(lang, "mygo.copy", nil); got != messages["mygo.copy"] || got == "" {
			t.Errorf("%s native copy = %q", lang, got)
		}
		if lang != "en" && messages["mygo.copy"] == "Copy" {
			t.Errorf("%s native Copy remains English", lang)
		}
	}
}
