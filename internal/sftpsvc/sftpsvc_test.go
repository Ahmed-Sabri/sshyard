package sftpsvc

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sshtest"
)

func connect(t *testing.T) (*Client, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// The test server is pkg/sftp serving this machine's disk, and on
		// Windows it mangles absolute paths ("/C:/..."). Real remotes are
		// POSIX servers; the client code is covered on Linux and macOS.
		t.Skip("sftp test server does not support Windows paths")
	}
	root := t.TempDir()
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	srv.SFTPRoot = root
	t.Cleanup(srv.Close)
	kh, _ := session.OpenKnownHosts(filepath.Join(t.TempDir(), "kh"))
	kh.Add(srv.Addr, srv.HostKey.PublicKey())
	conn, err := session.Dial(context.Background(), session.Config{Addr: srv.Addr, User: "demo", HostKey: kh.Callback(),
		Credentials: func(context.Context) (session.Credentials, error) {
			return session.Credentials{Password: "secret"}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c, err := New(conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, root
}

func TestListUploadDownload(t *testing.T) {
	c, root := connect(t)
	os.Mkdir(filepath.Join(root, "Zdir"), 0o755)
	os.Mkdir(filepath.Join(root, "adir"), 0o755)
	os.WriteFile(filepath.Join(root, "b.txt"), []byte("bee"), 0o644)
	os.Symlink(filepath.Join(root, "adir"), filepath.Join(root, "link"))

	home, err := c.Home()
	if err != nil || home != root {
		t.Fatalf("home %q %v", home, err)
	}
	es, err := c.List(home)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name)
	}
	if len(es) != 4 || es[0].Name != "adir" || es[1].Name != "link" || es[2].Name != "Zdir" || es[3].Name != "b.txt" || es[3].Size != 3 {
		t.Fatalf("listing %v", names)
	}

	local := t.TempDir()
	data := make([]byte, 1<<20+123)
	rand.Read(data)
	src := filepath.Join(local, "blob.bin")
	os.WriteFile(src, data, 0o640)

	var last, calls atomic.Int64
	remote, err := c.Upload(context.Background(), src, RemoteJoin(home, "adir"), func(done, total int64) {
		if total != int64(len(data)) {
			t.Errorf("total %d", total)
		}
		last.Store(done)
		calls.Add(1)
	})
	if err != nil {
		t.Fatal(err)
	}
	if remote != filepath.Join(root, "adir", "blob.bin") || last.Load() != int64(len(data)) || calls.Load() < 2 {
		t.Errorf("upload %q last %d calls %d", remote, last.Load(), calls.Load())
	}
	if got, _ := os.ReadFile(remote); !bytes.Equal(got, data) {
		t.Error("uploaded bytes differ")
	}

	out := t.TempDir()
	got, err := c.Download(context.Background(), remote, out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(got); !bytes.Equal(b, data) || filepath.Base(got) != "blob.bin" {
		t.Error("downloaded bytes differ")
	}
	if left, _ := filepath.Glob(filepath.Join(out, ".*part*")); len(left) != 0 {
		t.Errorf("temp files left %v", left)
	}

	if RemoteParent(RemoteJoin(home, "adir")) != home {
		t.Error("parent")
	}
	if _, err := c.List(RemoteJoin(home, "missing")); err == nil {
		t.Error("listing a missing directory")
	}
	if _, err := c.Download(context.Background(), RemoteJoin(home, "adir"), out, nil); err == nil {
		t.Error("downloaded a directory")
	}
}

func TestCanceledTransfersLeaveNothing(t *testing.T) {
	c, root := connect(t)
	local := t.TempDir()
	src := filepath.Join(local, "big.bin")
	os.WriteFile(src, make([]byte, 4<<20), 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := c.Upload(ctx, src, root, func(done, total int64) {
		if done > 0 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("upload: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "big.bin")); !os.IsNotExist(err) {
		t.Error("partial upload left")
	}

	os.WriteFile(filepath.Join(root, "r.bin"), make([]byte, 4<<20), 0o644)
	ctx, cancel = context.WithCancel(context.Background())
	_, err = c.Download(ctx, filepath.Join(root, "r.bin"), local, func(done, total int64) {
		if done > 0 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("download: %v", err)
	}
	if es, _ := ListLocal(local); len(es) != 1 {
		t.Errorf("local dir %v", es)
	}
}

func TestMkdir(t *testing.T) {
	c, root := connect(t)
	p, err := c.Mkdir(root, "new dir")
	if err != nil || p != RemoteJoin(root, "new dir") {
		t.Fatalf("mkdir %q %v", p, err)
	}
	if fi, err := os.Stat(filepath.Join(root, "new dir")); err != nil || !fi.IsDir() {
		t.Fatalf("remote folder missing: %v", err)
	}
	if _, err := c.Mkdir(root, "new dir"); err == nil {
		t.Error("made a folder twice")
	}
	local := t.TempDir()
	if _, err := MkdirLocal(local, "here"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(local, "here")); err != nil || !fi.IsDir() {
		t.Fatal("local folder missing")
	}
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`} {
		if _, err := c.Mkdir(root, bad); err != ErrBadName {
			t.Errorf("remote %q: %v", bad, err)
		}
		if _, err := MkdirLocal(local, bad); err != ErrBadName {
			t.Errorf("local %q: %v", bad, err)
		}
	}
}
