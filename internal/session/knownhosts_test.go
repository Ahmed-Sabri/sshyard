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

// TestKnownHostsReplace: the host's keys give way to the new one, which is
// trusted at once; other hosts keep theirs.
func TestKnownHostsReplace(t *testing.T) {
	k, err := OpenKnownHosts(filepath.Join(t.TempDir(), "known_hosts"))
	if err != nil {
		t.Fatal(err)
	}
	old, old2, web, fresh := hostKey(t), hostKey(t), hostKey(t), hostKey(t)
	k.Add("vps.lan:2222", old)
	k.Add("web.lan:22", web)
	k.Add("vps.lan:2222", old2)
	addr := &net.TCPAddr{IP: net.IPv4(10, 0, 0, 5), Port: 2222}
	var changed *HostKeyChangedError
	if err := k.Callback()("vps.lan:2222", addr, fresh); !errors.As(err, &changed) || len(changed.Old) != 2 {
		t.Fatalf("changed: %v", err)
	}
	if got := k.For("vps.lan:2222"); len(got) != 2 || got[0].Line != 1 || got[1].Line != 3 {
		t.Fatalf("for: %+v", got)
	}
	if err := k.Replace("vps.lan:2222", fresh); err != nil {
		t.Fatal(err)
	}
	if err := k.Callback()("vps.lan:2222", addr, fresh); err != nil {
		t.Fatalf("new key not trusted: %v", err)
	}
	if err := k.Callback()("vps.lan:2222", addr, old); err == nil {
		t.Fatal("old key still trusted")
	}
	if err := k.Callback()("web.lan:22", addr, web); err != nil {
		t.Fatalf("other host lost its key: %v", err)
	}
	if l := k.List(); len(l) != 2 || l[0].Hosts[0] != "web.lan" || l[1].Hosts[0] != "[vps.lan]:2222" {
		t.Fatalf("list: %+v", l)
	}
}

// TestKnownHostsForget: the host is unknown again, hashed lines naming it
// included, and the rest of the file stays as it was.
func TestKnownHostsForget(t *testing.T) {
	k, err := OpenKnownHosts(filepath.Join(t.TempDir(), "known_hosts"))
	if err != nil {
		t.Fatal(err)
	}
	old, web := hostKey(t), hostKey(t)
	file := "# kept\n" + knownhosts.Line([]string{knownhosts.HashHostname("vps.lan")}, old) + "\n" + knownhosts.Line([]string{"web.lan"}, web) + "\n"
	os.WriteFile(k.Path(), []byte(file), 0o600)
	if err := k.reload(); err != nil {
		t.Fatal(err)
	}
	if got := k.For("vps.lan:22"); len(got) != 1 || !got[0].Hashed() {
		t.Fatalf("for: %+v", got)
	}
	if err := k.Forget("vps.lan:22"); err != nil {
		t.Fatal(err)
	}
	addr := &net.TCPAddr{IP: net.IPv4(10, 0, 0, 5), Port: 22}
	var unknown *UnknownHostError
	if err := k.Callback()("vps.lan:22", addr, old); !errors.As(err, &unknown) {
		t.Fatalf("forgotten host: %v", err)
	}
	b, _ := os.ReadFile(k.Path())
	if want := "# kept\n" + knownhosts.Line([]string{"web.lan"}, web) + "\n"; string(b) != want {
		t.Fatalf("file:\n%s", b)
	}
}

// TestKnownHostsRemoveStale: a line changed since it was listed is not
// removed.
func TestKnownHostsRemoveStale(t *testing.T) {
	k, err := OpenKnownHosts(filepath.Join(t.TempDir(), "known_hosts"))
	if err != nil {
		t.Fatal(err)
	}
	k.Add("a.lan:22", hostKey(t))
	k.Add("b.lan:22", hostKey(t))
	listed := k.List()
	other := knownhosts.Line([]string{"c.lan"}, hostKey(t)) + "\n" + knownhosts.Line([]string{"d.lan"}, listed[1].Key) + "\n"
	os.WriteFile(k.Path(), []byte(other), 0o600)
	if err := k.Remove(listed[0]); !errors.Is(err, ErrKnownHostsChanged) {
		t.Fatalf("stale remove: %v", err)
	}
	if b, _ := os.ReadFile(k.Path()); string(b) != other {
		t.Fatalf("file changed:\n%s", b)
	}
	if err := k.Remove(listed[1]); !errors.Is(err, ErrKnownHostsChanged) {
		t.Fatalf("line of another host removed: %v", err)
	}
	if err := k.reload(); err != nil {
		t.Fatal(err)
	}
	if err := k.Remove(k.List()[1]); err != nil {
		t.Fatal(err)
	}
	if l := k.List(); len(l) != 1 || l[0].Hosts[0] != "c.lan" {
		t.Fatalf("list: %+v", l)
	}
}
