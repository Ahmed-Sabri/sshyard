package assets

import (
	"path/filepath"
	"testing"
)

func TestAIPolicyNormalize(t *testing.T) {
	h := Host{Address: "h", User: "u", AIPolicy: AIPolicy{
		Mode:          " FULL ",
		WritableRoots: []string{" /home/u ", "", "/home/u", "a\x00b"},
		Sensitive:     []string{" ~/.ssh/*.pem ", "  "},
	}}
	h.Normalize()
	if h.AIPolicy.Mode != "full" {
		t.Fatalf("mode %q", h.AIPolicy.Mode)
	}
	if len(h.AIPolicy.WritableRoots) != 1 || h.AIPolicy.WritableRoots[0] != "/home/u" {
		t.Fatalf("roots %q", h.AIPolicy.WritableRoots)
	}
	if len(h.AIPolicy.Sensitive) != 1 || h.AIPolicy.Sensitive[0] != "~/.ssh/*.pem" {
		t.Fatalf("sensitive %q", h.AIPolicy.Sensitive)
	}

	// Empty mode inherits. An unknown mode inherits too. "full" is kept;
	// the editor asks before saving it.
	h.AIPolicy.Mode = "nope"
	h.Normalize()
	if h.AIPolicy.Mode != "" {
		t.Fatalf("invalid mode kept: %q", h.AIPolicy.Mode)
	}
	h.AIPolicy.Mode = ""
	h.Normalize()
	if h.AIPolicy.Mode != "" {
		t.Fatal("empty mode filled in")
	}
}

func TestAIPolicyPersists(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.Add(Host{Name: "web", Address: "10.0.0.5", User: "root", AIPolicy: AIPolicy{
		Mode: "review", WritableRoots: []string{"/var/www"}, Sensitive: []string{"/etc/secret/*"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Open(filepath.Join(dir, "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Get(h.ID)
	if !ok || got.AIPolicy.Mode != "review" || len(got.AIPolicy.WritableRoots) != 1 || got.AIPolicy.WritableRoots[0] != "/var/www" || got.AIPolicy.Sensitive[0] != "/etc/secret/*" {
		t.Fatalf("reloaded %+v %v", got.AIPolicy, ok)
	}
}
