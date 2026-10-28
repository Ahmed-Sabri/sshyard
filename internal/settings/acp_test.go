package settings

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestACPConfigRoundTripAndUnknownAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s := Default()
	s.AIDefaultAgent = "acp"
	s.AIACP = ACPConfig{Command: "/opt/custom-acp", Args: []string{"--stdio", "argument with spaces", ""}}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.AIDefaultAgent != "acp" || !reflect.DeepEqual(got.AIACP, s.AIACP) {
		t.Fatalf("ACP configuration not preserved: %+v", got.AIACP)
	}
	got.AIDefaultAgent = "unknown-backend"
	got.Normalize()
	if got.AIDefaultAgent != "unknown-backend" {
		t.Fatal("unknown backend silently replaced with built-in")
	}
}
