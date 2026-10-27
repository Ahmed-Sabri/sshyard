package sftpsvc

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sshtest"
	"golang.org/x/crypto/ssh"
)

func TestHasKey(t *testing.T) {
	_, a, _ := ed25519.GenerateKey(rand.Reader)
	_, b, _ := ed25519.GenerateKey(rand.Reader)
	sa, _ := ssh.NewSignerFromKey(a)
	sb, _ := ssh.NewSignerFromKey(b)
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sa.PublicKey())))
	data := []byte("# comment\n\nnot a key\nfrom=\"10.0.0.1\",no-pty " + line + " other comment\n")
	if !HasKey(data, sa.PublicKey()) {
		t.Fatal("a key with options and a comment is not seen")
	}
	if HasKey(data, sb.PublicKey()) {
		t.Fatal("another key is seen")
	}
}

// TestInstallKey installs a key over SFTP, then logs in with it alone,
// against a server that, like OpenSSH's StrictModes, refuses an
// authorized_keys anyone may write to.
func TestInstallKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sftp test server does not support Windows paths")
	}
	c, root := connect(t)
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.AuthorizedKeysFile = filepath.Join(root, ".ssh", "authorized_keys")
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " me@test"
	keyOnly := func() error {
		kh, _ := session.OpenKnownHosts(filepath.Join(t.TempDir(), "kh"))
		kh.Add(srv.Addr, srv.HostKey.PublicKey())
		blk, _ := ssh.MarshalPrivateKey(priv, "")
		pemKey := pem.EncodeToMemory(blk)
		conn, err := session.Dial(context.Background(), session.Config{Addr: srv.Addr, User: "demo", HostKey: kh.Callback(),
			Credentials: func(context.Context) (session.Credentials, error) {
				return session.Credentials{PrivateKey: pemKey}, nil
			}})
		if err == nil {
			conn.Close()
		}
		return err
	}
	if keyOnly() == nil {
		t.Fatal("logged in before the install")
	}
	added, err := c.InstallKey(signer.PublicKey(), line)
	if err != nil || !added {
		t.Fatal(added, err)
	}
	for p, want := range map[string]os.FileMode{filepath.Join(root, ".ssh"): 0o700, srv.AuthorizedKeysFile: 0o600} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != want {
			t.Fatalf("%s: %v %v, want %v", p, fi.Mode().Perm(), err, want)
		}
	}
	if err := keyOnly(); err != nil {
		t.Fatalf("key-only login after the install: %v", err)
	}
	// Again: nothing is added.
	if added, err := c.InstallKey(signer.PublicKey(), line); err != nil || added {
		t.Fatal(added, err)
	}
	b, _ := os.ReadFile(srv.AuthorizedKeysFile)
	if n := bytes.Count(b, []byte("ssh-ed25519")); n != 1 {
		t.Fatalf("%d lines:\n%s", n, b)
	}
	// A file with other keys and no final newline gets one before ours.
	os.WriteFile(srv.AuthorizedKeysFile, []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOtherKeyOtherKeyOtherKeyOtherKeyOtherKey0 old"), 0o600)
	if added, err := c.InstallKey(signer.PublicKey(), line); err != nil || !added {
		t.Fatal(added, err)
	}
	b, _ = os.ReadFile(srv.AuthorizedKeysFile)
	if !strings.HasSuffix(string(b), " old\n"+line+"\n") {
		t.Fatalf("appended badly:\n%q", b)
	}
	// An authorized_keys that is there keeps its mode.
	os.WriteFile(srv.AuthorizedKeysFile, []byte("# mine\n"), 0o600)
	os.Chmod(srv.AuthorizedKeysFile, 0o640)
	if added, err := c.InstallKey(signer.PublicKey(), line); err != nil || !added {
		t.Fatal(added, err)
	}
	if fi, _ := os.Stat(srv.AuthorizedKeysFile); fi.Mode().Perm() != 0o640 {
		t.Fatalf("the install changed authorized_keys to %v", fi.Mode().Perm())
	}
	if err := keyOnly(); err != nil {
		t.Fatalf("key-only login with authorized_keys at 0640: %v", err)
	}
	// No temporary names are left behind.
	for _, d := range []string{root, filepath.Join(root, ".ssh")} {
		ents, _ := os.ReadDir(d)
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), ".sshyard-") {
				t.Fatalf("left %s in %s", e.Name(), d)
			}
		}
	}
	// A ~/.ssh anyone may write to is kept as it is, and refused.
	os.Chmod(filepath.Join(root, ".ssh"), 0o777)
	if _, err := c.InstallKey(signer.PublicKey(), line); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(root, ".ssh")); fi.Mode().Perm() != 0o777 {
		t.Fatalf("the install changed ~/.ssh to %v", fi.Mode().Perm())
	}
	if keyOnly() == nil {
		t.Fatal("logged in with ~/.ssh at 0777")
	}
}

// TestInstallKeyPrivateBeforeRename watches each rename the install
// makes: what is renamed into place has its private mode by then; and a
// ~/.ssh or authorized_keys that appears meanwhile is kept and used, with
// no temporary left behind.
func TestInstallKeyPrivateBeforeRename(t *testing.T) {
	c, root := connect(t)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " me@test"
	dir, file := filepath.Join(root, ".ssh"), filepath.Join(root, ".ssh", "authorized_keys")
	type seen struct {
		target string
		mode   os.FileMode
	}
	var renames []seen
	race := map[string]func(){}
	beforeRename = func(tmp, p string) {
		fi, err := os.Lstat(tmp)
		if err != nil {
			t.Errorf("%s: %v", tmp, err)
			return
		}
		renames = append(renames, seen{p, fi.Mode().Perm()})
		if f := race[p]; f != nil {
			f()
		}
	}
	t.Cleanup(func() { beforeRename = nil })
	noTemps := func() {
		t.Helper()
		for _, d := range []string{root, dir} {
			ents, _ := os.ReadDir(d)
			for _, e := range ents {
				if strings.HasPrefix(e.Name(), ".sshyard-") {
					t.Fatalf("left %s in %s", e.Name(), d)
				}
			}
		}
	}

	if added, err := c.InstallKey(signer.PublicKey(), line); err != nil || !added {
		t.Fatal(added, err)
	}
	want := []seen{{dir, 0o700}, {file, 0o600}}
	if len(renames) != 2 || renames[0] != want[0] || renames[1] != want[1] {
		t.Fatalf("renames %+v, want %+v", renames, want)
	}
	noTemps()

	// Both appear just before their rename: the ones that appeared are
	// kept, with their modes and content, and the key is appended.
	os.RemoveAll(dir)
	renames = nil
	race[dir] = func() { os.Mkdir(dir, 0o750) }
	race[file] = func() { os.WriteFile(file, []byte("# theirs"), 0o640) }
	if added, err := c.InstallKey(signer.PublicKey(), line); err != nil || !added {
		t.Fatal(added, err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o750 {
		t.Errorf("~/.ssh replaced: %v", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(file)
	if fi, _ := os.Stat(file); string(b) != "# theirs\n"+line+"\n" || fi.Mode().Perm() != 0o640 {
		t.Errorf("authorized_keys %q, %v", b, fi.Mode().Perm())
	}
	noTemps()

	// A file in place of ~/.ssh is refused.
	os.RemoveAll(dir)
	race = map[string]func(){dir: func() { os.WriteFile(dir, nil, 0o600) }}
	if _, err := c.InstallKey(signer.PublicKey(), line); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("err %v", err)
	}
	noTemps()
}
