package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Licoy/sshyard/internal/sshtest"
)

// TestFilesSCPBadge: a server without SFTP shows its files through its
// shell and scp, with a badge in the pane header saying so.
func TestFilesSCPBadge(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test server runs commands with /bin/sh")
	}
	_, tt, fc, _ := filesPaneOn(t, func(srv *sshtest.Server) {
		srv.NoSFTP = true
		srv.Exec = sshtest.ShellExec(srv.SFTPRoot)
		os.WriteFile(filepath.Join(srv.SFTPRoot, "hello.txt"), []byte("hi"), 0o644)
	})
	waitFor(t, tt, "the listing", func() bool { return tt.HasText("hello.txt") })
	if !fc.fs.remote.client.SCP() || !tt.HasText("SCP") {
		t.Fatalf("no SCP badge: %q", tt.Texts())
	}
	if !tt.HasText(en.ScpFallbackNote()) {
		t.Errorf("the badge does not say why: %q", tt.Texts())
	}
}
