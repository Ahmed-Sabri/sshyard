package sftpsvc

import (
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/sshtest"
)

// A listing cleans up at most maxLeftovers leftovers, as a save did,
// however many the folder (or the server) says there are.
func TestListingCleanupCapped(t *testing.T) {
	old := time.Now().Add(-2 * staleLeftover)
	reqs := func(ffs *sshtest.FakeFS) (n int) {
		for _, r := range ffs.Seen() {
			if strings.Contains(r, "/.sshyard-") {
				n++
			}
		}
		return n
	}
	t.Run("stale copies", func(t *testing.T) {
		ffs := &sshtest.FakeFS{Files: map[string][]byte{"/d/f": []byte("x")}}
		c := connectFake(t, ffs)
		ents := []fs.FileInfo{pagedInfo{name: "f", mode: 0o644, mtime: old}}
		for i := range 3 * maxLeftovers {
			ents = append(ents, pagedInfo{name: fmt.Sprintf(".sshyard-%012x-f", i), mode: 0o644, mtime: old})
		}
		if changed, _ := c.cleanLeftovers("/d", ents); !changed {
			t.Fatal("cleaned nothing")
		}
		if n := reqs(ffs); n != maxLeftovers {
			t.Fatalf("%d requests for leftovers, want %d", n, maxLeftovers)
		}
	})
	t.Run("backups", func(t *testing.T) {
		ffs := &sshtest.FakeFS{Files: map[string][]byte{}}
		c := connectFake(t, ffs)
		var ents []fs.FileInfo
		for i := range 3 * maxLeftovers {
			n := fmt.Sprintf("f%d", i)
			ents = append(ents, pagedInfo{name: n, mode: 0o644, mtime: old},
				pagedInfo{name: fmt.Sprintf(".sshyard-old-%012x-%s", i, n), mode: 0o644, mtime: old})
		}
		c.cleanLeftovers("/d", ents)
		if n := reqs(ffs); n != maxLeftovers {
			t.Fatalf("%d requests for leftovers, want %d", n, maxLeftovers)
		}
	})
	// However many there are, a listing renames nothing: the backups of
	// missing files are reported, up to the cap.
	t.Run("missing files", func(t *testing.T) {
		ffs := &sshtest.FakeFS{Files: map[string][]byte{}}
		c := connectFake(t, ffs)
		var ents []fs.FileInfo
		for i := range 3 * maxLeftovers {
			ents = append(ents, pagedInfo{name: fmt.Sprintf(".sshyard-old-%012x-g%d", i, i), mode: 0o644, mtime: old})
		}
		_, un := c.cleanLeftovers("/d", ents)
		if len(un) != maxLeftovers {
			t.Fatalf("%d reported", len(un))
		}
		if n := reqs(ffs); n != 0 {
			t.Fatalf("%d requests: %q", n, ffs.Seen())
		}
	})
}
