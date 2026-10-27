//go:build !windows

package sftpsvc

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/Licoy/sshyard/internal/sshtest"
)

func TestLocalMode(t *testing.T) {
	for m, want := range map[fs.FileMode]fs.FileMode{
		0o755: 0o644, 0o700: 0o600, 0o777: 0o644, 0o666: 0o644, 0o640: 0o640, 0o600: 0o600,
		0o444: 0o644, 0o000: 0o600, 0o711: 0o600, fs.ModeSetuid | 0o4755: 0o644,
	} {
		if got := LocalMode(m); got != want {
			t.Errorf("%v: %v, want %v", m, got, want)
		}
	}
}

// Downloaded files never keep the server's execute bits, by the queue
// or by Client.Download.
func TestDownloadDropsExecuteBits(t *testing.T) {
	c, root := connect(t)
	modes := map[string]fs.FileMode{"run.sh": 0o755, "own": 0o700, "all": 0o777, "conf": 0o640}
	for n, m := range modes {
		p := filepath.Join(root, n)
		os.WriteFile(p, []byte(n), 0o600)
		os.Chmod(p, m)
	}
	ts := &Transfers{Client: func(context.Context) (*Client, error) { return c, nil }, Logf: func(string, ...any) {}}
	queued, direct := t.TempDir(), t.TempDir()
	for n := range modes {
		id := ts.Add(Download, filepath.Join(root, n), filepath.Join(queued, n), n)
		waitState(t, ts, id, Done)
		if _, err := c.Download(context.Background(), filepath.Join(root, n), direct, nil); err != nil {
			t.Fatal(err)
		}
	}
	for n, m := range modes {
		for _, dir := range []string{queued, direct} {
			fi, err := os.Stat(filepath.Join(dir, n))
			if err != nil {
				t.Fatal(err)
			}
			if got := fi.Mode().Perm(); got != LocalMode(m) || got&0o111 != 0 {
				t.Errorf("%s from %v: %v", filepath.Join(dir, n), m, got)
			}
		}
	}
}

// Client.Download refuses a name not safe here, writing nothing.
func TestDownloadUnsafeName(t *testing.T) {
	ffs := &sshtest.FakeFS{
		Dirs:  map[string][]sshtest.FakeEntry{"/": {{Name: "CON.txt", Size: 1}, {Name: `..\x`, Size: 1}}},
		Files: map[string][]byte{"/CON.txt": []byte("c"), `/..\x`: []byte("x")},
	}
	c := connectFake(t, ffs)
	parent := t.TempDir()
	local := filepath.Join(parent, "in")
	os.Mkdir(local, 0o755)
	for _, p := range []string{"/CON.txt", `/..\x`} {
		if got, err := c.Download(context.Background(), p, local, nil); !errors.Is(err, ErrUnsafeName) {
			t.Errorf("%s: %q %v", p, got, err)
		}
	}
	if got := files(parent); len(got) != 0 {
		t.Fatalf("written: %v", got)
	}
}
