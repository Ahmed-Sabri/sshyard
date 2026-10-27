package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/theme"
)

func TestRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "settings.json")
	s, err := Load(p)
	if err != nil || !reflect.DeepEqual(s, Default()) {
		t.Fatalf("missing file: %+v %v", s, err)
	}
	s.Theme, s.Language, s.TerminalFontSize = theme.Dark, "zh-Hans", 15
	if err := Save(p, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil || !reflect.DeepEqual(got, s) {
		t.Fatalf("got %+v %v, want %+v", got, err, s)
	}
}

func TestNormalize(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(p, []byte(`{"theme":"sepia","language":"fr","terminalFontSize":99,"transferConcurrency":0,"openConfirmMB":5000}`), 0o600)
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Theme != theme.Auto || s.Language != "" || s.TerminalFontSize != 13 || s.TransferConcurrency != 3 || s.OpenConfirmMB != 10 {
		t.Errorf("not normalized: %+v", s)
	}
}

func TestFontsPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s := Default()
	if s.UIFont != "" || s.TerminalFont != "" || !s.MonospacedOnly {
		t.Fatalf("default fonts %+v", s)
	}
	s.UIFont, s.TerminalFont, s.TerminalFontSize, s.MonospacedOnly = "Noto Sans", "JetBrains Mono", 15, false
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || !reflect.DeepEqual(got, s) {
		t.Fatalf("got %+v %v, want %+v", got, err, s)
	}
	// A file of before the font settings keeps the monospaced filter on.
	os.WriteFile(path, []byte(`{"theme":"dark","terminalFontSize":14}`), 0o600)
	if got, _ := Load(path); !got.MonospacedOnly || got.TerminalFont != "" {
		t.Errorf("old file %+v", got)
	}
}

func TestOSC52Policy(t *testing.T) {
	if g, h := ResolveOSC52Write(OSC52Ask, ""), ResolveOSC52Write(OSC52Deny, OSC52Allow); g != OSC52Ask || h != OSC52Allow {
		t.Fatalf("resolve %s %s", g, h)
	}
	if ResolveOSC52Write(OSC52Deny, "") != OSC52Deny {
		t.Fatal("empty host follows the global policy")
	}
	if ResolveOSC52Write("nope", "nope") != OSC52Ask {
		t.Fatal("invalid policy is ask")
	}
	s := Default()
	if s.OSC52Write != OSC52Ask || s.OSC52MaxBytes != OSC52MaxDefault {
		t.Fatalf("default %+v", s)
	}
	s.OSC52Write, s.OSC52MaxBytes = "nope", 10
	s.Normalize()
	if s.OSC52Write != OSC52Ask || s.OSC52MaxBytes != OSC52MaxDefault {
		t.Fatalf("normalized %+v", s)
	}
	s.OSC52Write, s.OSC52MaxBytes = OSC52Ask, OSC52MaxMin
	s.Normalize()
	if s.OSC52Write != OSC52Ask || s.OSC52MaxBytes != OSC52MaxMin {
		t.Fatalf("kept %+v", s)
	}
	s.OSC52MaxBytes = OSC52MaxMax + 1
	s.Normalize()
	if s.OSC52MaxBytes != OSC52MaxDefault {
		t.Fatalf("ceiling %+v", s)
	}
	p := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(p, []byte(`{"theme":"dark"}`), 0o600)
	got, err := Load(p)
	if err != nil || got.OSC52Write != OSC52Ask || got.OSC52MaxBytes != OSC52MaxDefault {
		t.Fatalf("old file %+v %v", got, err)
	}
}

func TestMetricsDefaults(t *testing.T) {
	d := Default()
	if !d.MetricsEnabled || d.MetricsInterval != 2 || d.MetricsMount != "" {
		t.Fatalf("defaults %+v", d)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	// A file from before metrics keeps the bar on, at 2 seconds, with no extra mount.
	os.WriteFile(path, []byte(`{"theme":"dark","terminalFontSize":14}`), 0o600)
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.MetricsEnabled || got.MetricsInterval != 2 || got.MetricsMount != "" || got.TerminalFontSize != 14 {
		t.Fatalf("old file %+v", got)
	}
	os.WriteFile(path, []byte(`{"metricsEnabled":false,"metricsInterval":10,"metricsMount":"/mnt/my disk"}`), 0o600)
	got, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.MetricsEnabled || got.MetricsInterval != 10 || got.MetricsMount != "/mnt/my disk" {
		t.Fatalf("kept %+v", got)
	}
	os.WriteFile(path, []byte(`{"metricsEnabled":false,"metricsInterval":7,"metricsMount":"/data/$(reboot)"}`), 0o600)
	got, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.MetricsEnabled || got.MetricsInterval != 2 || got.MetricsMount != "" {
		t.Fatalf("normalized %+v", got)
	}
}

func TestNormalizeFonts(t *testing.T) {
	for in, want := range map[string]string{
		"  Fira Code ":           "Fira Code",
		"Menlo, monospace":       "",
		"a\nb":                   "",
		strings.Repeat("x", 200): "",
		"思源黑体":                   "思源黑体",
	} {
		s := Default()
		s.UIFont, s.TerminalFont = in, in
		s.Normalize()
		if s.UIFont != want || s.TerminalFont != want {
			t.Errorf("%q: %q %q, want %q", in, s.UIFont, s.TerminalFont, want)
		}
	}
}

// TestMaterials: the sidebar is translucent and the content opaque by
// default, as before the setting, and each keeps its own choice.
func TestMaterials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte(`{"theme":"dark","contentMaterial":"frosted"}`), 0o600)
	s, err := Load(path)
	if err != nil || s.ChromeMaterial != MaterialTranslucent || s.ContentMaterial != MaterialOpaque {
		t.Fatalf("old file %+v %v", s, err)
	}
	s.ChromeMaterial, s.ContentMaterial = MaterialOpaque, MaterialTranslucent
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(path); got.ChromeMaterial != MaterialOpaque || got.ContentMaterial != MaterialTranslucent {
		t.Errorf("got %+v", got)
	}
}

func TestOSC52ExplicitPoliciesPersist(t *testing.T) {
	for _, policy := range []string{OSC52Allow, OSC52Ask, OSC52Deny} {
		s := Default()
		s.OSC52Write = policy
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil || got.OSC52Write != policy {
			t.Fatalf("policy %s became %s: %v", policy, got.OSC52Write, err)
		}
		if ResolveOSC52Write(policy, "") != policy {
			t.Fatalf("global %s changed", policy)
		}
		if ResolveOSC52Write("invalid", policy) != policy {
			t.Fatalf("host %s changed", policy)
		}
	}
}
