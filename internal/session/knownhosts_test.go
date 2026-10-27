package session

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func hostKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, _ := ssh.NewPublicKey(pub)
	return k
}

// TestKnownHostsMerge: lines another file has are added once, the rest
// (comments, junk) left out, and the keys are trusted at once.
func TestKnownHostsMerge(t *testing.T) {
	dir := t.TempDir()
	a, err := OpenKnownHosts(filepath.Join(dir, "a"))
	if err != nil {
		t.Fatal(err)
	}
	old, web := hostKey(t), hostKey(t)
	a.Add("old.lan:22", old)
	other := knownhosts.Line([]string{"old.lan"}, old) + "\n# a comment\nnot a key line\n" + knownhosts.Line([]string{"web.lan"}, web)
	n, err := a.Merge([]byte(other))
	if err != nil || n != 1 {
		t.Fatalf("merged %d, %v", n, err)
	}
	if n, _ := a.Merge([]byte(other)); n != 0 {
		t.Fatalf("merged %d again", n)
	}
	b, _ := os.ReadFile(a.Path())
	if strings.Contains(string(b), "comment") || strings.Contains(string(b), "not a key") || strings.Count(string(b), "\n") != 2 {
		t.Fatalf("file:\n%s", b)
	}
	addr := &net.TCPAddr{IP: net.IPv4(10, 0, 0, 5), Port: 22}
	if err := a.Callback()("web.lan:22", addr, web); err != nil {
		t.Fatalf("merged key not trusted: %v", err)
	}
	var unknown *UnknownHostError
	if err := a.Callback()("new.lan:22", addr, web); !errors.As(err, &unknown) {
		t.Fatalf("unknown host: %v", err)
	}
}
