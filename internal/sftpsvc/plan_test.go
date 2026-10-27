package sftpsvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Licoy/sshyard/internal/sshtest"
)

// withoutPager plans as on a server giving no second SFTP session.
func withoutPager(t *testing.T) {
	noPager = true
	t.Cleanup(func() { noPager = false })
}

// A folder far larger than the limit stops a plan as soon as a listing
// reply passes it, not after the server sent it all.
func TestPlanDownloadStopsEarlyInHugeFolder(t *testing.T) {
	old := PlanMaxEntries
	PlanMaxEntries = 1000
	t.Cleanup(func() { PlanMaxEntries = old })
	ffs := &sshtest.FakeFS{Many: func(p string) (int, bool) { return 400_000, p == "/huge" }}
	c := connectFake(t, ffs)
	_, _, err := c.PlanDownload(context.Background(), []string{"/huge"}, t.TempDir())
	if !errors.Is(err, ErrPlanTooLarge) {
		t.Fatalf("%v, want ErrPlanTooLarge", err)
	}
	// A reply is up to 100 names; what was sent past the limit is the
	// reply that passed it, and what the server had sent ahead.
	if n := ffs.Served.Load(); n > int64(PlanMaxEntries)+200 {
		t.Fatalf("server sent %d names for a limit of %d", n, PlanMaxEntries)
	}
}

// The limit counts what is in all folders, listed or planned.
func TestPlanDownloadLimitAcrossFolders(t *testing.T) {
	old := PlanMaxEntries
	PlanMaxEntries = 242
	t.Cleanup(func() { PlanMaxEntries = old })
	ffs := &sshtest.FakeFS{
		Dirs: map[string][]sshtest.FakeEntry{"/": {{Name: "a", Dir: true}}, "/a": {{Name: "b", Dir: true}, {Name: "c", Dir: true}}},
		Many: func(p string) (int, bool) { return 120, p == "/a/b" || p == "/a/c" },
	}
	c := connectFake(t, ffs)
	if _, _, err := c.PlanDownload(context.Background(), []string{"/a"}, t.TempDir()); !errors.Is(err, ErrPlanTooLarge) {
		t.Fatalf("%v, want ErrPlanTooLarge", err)
	}
	PlanMaxEntries = 243 // a, b, c and 240 files
	items, _, err := c.PlanDownload(context.Background(), []string{"/a"}, t.TempDir())
	if err != nil || len(items) != 243 {
		t.Fatalf("%d %v", len(items), err)
	}
}

// Listed a reply at a time or whole, a plan is the same; folders of
// several replies are listed through.
func TestPlanDownloadPagerSameAsWhole(t *testing.T) {
	ffs := &sshtest.FakeFS{
		Dirs: map[string][]sshtest.FakeEntry{"/": {{Name: "d", Dir: true}}, "/d": {{Name: "many", Dir: true}, {Name: "x.txt", Size: 7}}},
		Many: func(p string) (int, bool) { return 345, p == "/d/many" },
	}
	c := connectFake(t, ffs)
	local := t.TempDir()
	paged, _, err := c.PlanDownload(context.Background(), []string{"/d"}, local)
	if err != nil || len(paged) != 348 {
		t.Fatalf("%d %v", len(paged), err)
	}
	withoutPager(t)
	whole, _, err := c.PlanDownload(context.Background(), []string{"/d"}, local)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paged, whole) {
		t.Fatal("paged and whole plans differ")
	}
	if it := paged[len(paged)-1]; it.Rel != "d/x.txt" || it.Size != 7 || it.IsDir {
		t.Fatalf("%+v", it)
	}
}

// On a real server: sizes, folders and links read by the pager.
func TestPlanDownloadRealServer(t *testing.T) {
	c, root := connect(t)
	d := filepath.Join(root, "proj")
	os.MkdirAll(filepath.Join(d, "src", "deep"), 0o755)
	os.WriteFile(filepath.Join(d, "a.txt"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(d, "src", "deep", "b.bin"), make([]byte, 1234), 0o600)
	os.WriteFile(filepath.Join(d, "aux.c"), []byte("x"), 0o644)
	os.Symlink("a.txt", filepath.Join(d, "alink"))
	os.Symlink("src", filepath.Join(d, "srclink"))
	os.Symlink("nowhere", filepath.Join(d, "broken"))
	// a link to a folder that leads back up: left out, not a loop
	os.Symlink("..", filepath.Join(d, "src", "up"))
	local := t.TempDir()
	plan := func() ([]Item, []Skip) {
		items, skips, err := c.PlanDownload(context.Background(), []string{d}, local)
		if err != nil {
			t.Fatal(err)
		}
		return items, skips
	}
	items, skips := plan()
	var got []string
	for _, it := range items {
		got = append(got, fmt.Sprintf("%s:%v:%d", it.Rel, it.IsDir, it.Size))
	}
	want := "[proj:true:0 proj/a.txt:false:5 proj/alink:false:5 proj/src:true:0 proj/src/deep:true:0 proj/src/deep/b.bin:false:1234]"
	if fmt.Sprint(got) != want {
		t.Fatalf("items %v\nwant  %s", got, want)
	}
	var sk []string
	for _, s := range skips {
		sk = append(sk, fmt.Sprintf("%s:%d", s.Rel, s.Reason))
	}
	if fmt.Sprint(sk) != fmt.Sprintf("[proj/aux.c:%d proj/broken:%d proj/src/up:%d proj/srclink:%d]", SkipName, SkipBrokenLink, SkipLinkedFolder, SkipLinkedFolder) {
		t.Fatalf("skipped %v", sk)
	}
	withoutPager(t)
	items2, skips2 := plan()
	if !reflect.DeepEqual(items, items2) || !reflect.DeepEqual(skips, skips2) {
		t.Fatal("paged and whole plans differ")
	}
}

// A link chosen to download that leads to a folder is followed: its
// files download under the link's name.
func TestPlanDownloadFollowsChosenLink(t *testing.T) {
	c, root := connect(t)
	os.MkdirAll(filepath.Join(root, "real", "in"), 0o755)
	os.WriteFile(filepath.Join(root, "real", "in", "f"), []byte("f"), 0o644)
	os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link"))
	// a link to itself's folder: met again, a loop
	os.Symlink(".", filepath.Join(root, "self"))
	local := t.TempDir()
	items, skips, err := c.PlanDownload(context.Background(), []string{root + "/link"}, local)
	if err != nil || len(skips) != 0 || fmt.Sprint(rels(items)) != "[link link/in link/in/f]" {
		t.Fatalf("%v %+v %v", rels(items), skips, err)
	}
	if items[2].Dst != filepath.Join(local, "link", "in", "f") || items[2].Src != root+"/link/in/f" {
		t.Fatalf("%+v", items[2])
	}
	// Chosen, a link to the folder it is in leads to itself only once:
	// the link inside is left out.
	items, skips, err = c.PlanDownload(context.Background(), []string{root + "/self"}, local)
	if err != nil || len(items) == 0 || items[0].Rel != "self" {
		t.Fatalf("%v %v", rels(items), err)
	}
	found := false
	for _, s := range skips {
		found = found || s.Rel == "self/self" && s.Reason == SkipLinkedFolder
	}
	if !found {
		t.Fatalf("%+v", skips)
	}
	// A broken link chosen is left out, said why.
	os.Symlink("nowhere", filepath.Join(root, "gone"))
	items, skips, err = c.PlanDownload(context.Background(), []string{root + "/gone"}, local)
	if err != nil || len(items) != 0 || len(skips) != 1 || skips[0].Reason != SkipBrokenLink {
		t.Fatalf("%+v %+v %v", items, skips, err)
	}
}

// A hostile server's reply too long or with a count it does not hold is
// an error, not a large allocation.
func TestParseNamesHostile(t *testing.T) {
	b := []byte{0xff, 0xff, 0xff, 0xff}
	if _, err := parseNames(b); err == nil {
		t.Fatal("a count of 4G names in 4 bytes taken")
	}
	b = appendString(appendString([]byte{0, 0, 0, 1}, "n"), "long")
	if _, err := parseNames(b); err == nil {
		t.Fatal("missing attributes taken")
	}
	// size 9, mode 0100644 (a file), then "." left out
	b = append(b, 0, 0, 0, attrSize|attrPerms, 0, 0, 0, 0, 0, 0, 0, 9, 0, 0, 0x81, 0xa4)
	b[3] = 2
	b = append(appendString(appendString(b, "."), ""), 0, 0, 0, 0)
	fis, err := parseNames(b)
	if err != nil || len(fis) != 1 || fis[0].Name() != "n" || fis[0].Size() != 9 || fis[0].Mode() != 0o644 {
		t.Fatalf("%+v %v", fis, err)
	}
	if m := posixMode(0o040755); m != os.ModeDir|0o755 {
		t.Fatal(m)
	}
	if m := posixMode(0o120777); m != os.ModeSymlink|0o777 {
		t.Fatal(m)
	}
}
