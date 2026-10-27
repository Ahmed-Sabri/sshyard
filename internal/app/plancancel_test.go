package app

import (
	"sync"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/sshtest"
)

// While a download is planned the pane says so, with Cancel; canceled,
// the plan leaves nothing behind, not even when the server answers again.
func TestPlanningCanceled(t *testing.T) {
	var ffs *sshtest.FakeFS
	var once sync.Once
	ffs = &sshtest.FakeFS{
		Dirs: map[string][]sshtest.FakeEntry{
			"/":  {{Name: "d", Dir: true}},
			"/d": {{Name: "x.txt", Size: 1}},
		},
		Files: map[string][]byte{"/d/x.txt": []byte("x")},
		// The server stops answering once asked for the folder's names.
		Listing: func(p string) {
			if p == "/d" {
				once.Do(ffs.Stop)
			}
		},
	}
	_, tt, fc, _, local := hostilePane(t, ffs)
	t.Cleanup(ffs.Resume)
	waitFor(t, tt, "listing", func() bool { return tt.HasText("d") })
	download(t, tt, "d")
	waitFor(t, tt, "planning notice", func() bool { return tt.HasText("Preparing the download…") })
	time.Sleep(100 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Preparing the download…") {
		t.Fatal("the notice went before the plan was done")
	}
	mustClick(t, tt, "Cancel")
	tt.Frame()
	if tt.HasText("Preparing the download…") || fc.fs.planning != nil {
		t.Fatal("still planning after Cancel")
	}
	ffs.Resume()
	for range 30 {
		time.Sleep(10 * time.Millisecond)
		tt.Frame()
	}
	if n := len(fc.fs.transfers.List()); n != 0 {
		t.Fatalf("%d transfers queued", n)
	}
	if fc.fs.remote.err != "" || fc.fs.local.err != "" || fc.fs.conflict != nil {
		t.Fatalf("left behind: %q %q %v", fc.fs.remote.err, fc.fs.local.err, fc.fs.conflict)
	}
	if fs := files(local); len(fs) != 0 {
		t.Fatalf("downloaded %q", fs)
	}
	// A plan that is not canceled goes as before, its notice with it.
	download(t, tt, "d")
	waitFor(t, tt, "download", func() bool { return len(fc.fs.transfers.List()) == 1 })
	waitFor(t, tt, "notice gone", func() bool { return !tt.HasText("Preparing the download…") })
}
