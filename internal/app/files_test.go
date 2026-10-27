package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/sftpsvc"
	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/egoist/mygo/ui"
)

// filesPane opens a files pane on a test server serving a temporary
// folder, returning the folder and the pane.
func filesPane(t *testing.T) (*App, *ui.Tester, *conn, string) {
	t.Helper()
	return filesPaneOn(t, nil)
}

// filesPaneOn is filesPane with setup run on the server first.
func filesPaneOn(t *testing.T, setup func(*sshtest.Server)) (*App, *ui.Tester, *conn, string) {
	t.Helper()
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv.SFTPRoot = root
	if setup != nil {
		setup(srv)
	}
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "box", "secret")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	a.selected = h.ID
	tt.Frame()
	mustClick(t, tt, "Open Files")
	fc := a.focused()
	waitFor(t, tt, "files connected", func() bool { return fc.fs.remote.client != nil })
	return a, tt, fc, root
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

func center(t *testing.T, tt *ui.Tester, s string) (float32, float32) {
	t.Helper()
	r, ok := tt.Find(s)
	if !ok {
		t.Fatalf("no %q in %q", s, tt.Texts())
	}
	return r.X + r.W/2, r.Y + r.H/2
}

// nameAt is the middle of the icon of the row of name.
func nameAt(t *testing.T, tt *ui.Tester, name string) (float32, float32) {
	t.Helper()
	r, ok := tt.Find(name)
	if !ok {
		t.Fatalf("no %q in %q", name, tt.Texts())
	}
	return r.X + 18, r.Y + r.H/2
}

func TestFilesContextMenu(t *testing.T) {
	a, tt, fc, root := filesPane(t)
	write(t, filepath.Join(root, "a.txt"), "hello")
	a.refreshSides(fc)
	waitFor(t, tt, "a.txt", func() bool { return tt.HasText("a.txt") })
	remote := sftpsvc.RemoteJoin(fc.fs.remote.dir, "a.txt")

	menu := func() []string {
		t.Helper()
		if err := tt.RightClick("a.txt"); err != nil {
			t.Fatal(err)
		}
		return menuOf(a)
	}
	choose := func(item string) {
		t.Helper()
		menu()
		if err := chooseMenu(a, tt, item); err != nil {
			t.Fatalf("%v: menu %q", err, menuOf(a))
		}
		tt.Frame()
	}
	got := menu()
	for _, want := range []string{"Edit", "Open With Default App", "Download", "Rename", "Permissions…", "Copy Path", "Delete…", "New Folder…", "New File…", "Refresh"} {
		if !slices.Contains(got, want) {
			t.Errorf("menu %q has no %q", got, want)
		}
	}
	chooseMenu(a, tt, "Copy Path")
	tt.Frame()
	if tt.Clipboard() != remote {
		t.Errorf("clipboard %q, want %q", tt.Clipboard(), remote)
	}

	// Permissions. Windows reports a writable file as 0666 and Chmod
	// cannot store 0600, so both the dialog and the result stay 0666.
	perm, mode := "644", os.FileMode(0o600)
	if runtime.GOOS == "windows" {
		perm, mode = "666", 0o666
	}
	choose("Permissions…")
	if fc.fs.ask == nil || fc.fs.ask.kind != askChmod || fc.fs.ask.value != perm {
		t.Fatalf("permissions dialog %+v", fc.fs.ask)
	}
	fc.fs.ask.value = "600"
	tt.Frame()
	mustClick(t, tt, "Save")
	if fi, err := os.Stat(filepath.Join(root, "a.txt")); err != nil || fi.Mode().Perm() != mode {
		t.Errorf("mode %v %v", fi.Mode(), err)
	}
	if fc.fs.ask != nil {
		t.Error("permissions dialog still open")
	}
	// A bad mode is said in the dialog.
	choose("Permissions…")
	fc.fs.ask.value = "999"
	tt.Frame()
	mustClick(t, tt, "Save")
	if fc.fs.ask == nil || fc.fs.ask.err == "" {
		t.Error("bad mode taken")
	}
	mustClick(t, tt, "Cancel")

	// Rename
	choose("Rename")
	if fc.fs.ask == nil || fc.fs.ask.kind != askRename || fc.fs.ask.value != "a.txt" {
		t.Fatalf("rename dialog %+v", fc.fs.ask)
	}
	fc.fs.ask.value = "b.txt"
	tt.Frame()
	mustClick(t, tt, "Rename")
	waitFor(t, tt, "rename", func() bool { return read(filepath.Join(root, "b.txt")) == "hello" && tt.HasText("b.txt") })

	// New file, from the menu of the folder
	if err := tt.RightClick("b.txt"); err != nil {
		t.Fatal(err)
	}
	chooseMenu(a, tt, "New File…")
	tt.Frame()
	tt.Type("new.txt")
	tt.Frame()
	mustClick(t, tt, "Create")
	waitFor(t, tt, "new file", func() bool { _, err := os.Stat(filepath.Join(root, "new.txt")); return err == nil })

	// Delete, after confirming
	if err := tt.RightClick("b.txt"); err != nil {
		t.Fatal(err)
	}
	chooseMenu(a, tt, "Delete…")
	tt.Frame()
	if !tt.HasText(`Delete “b.txt”?`) {
		t.Fatalf("no confirmation in %q", tt.Texts())
	}
	mustClick(t, tt, "Delete")
	waitFor(t, tt, "delete", func() bool {
		_, err := os.Stat(filepath.Join(root, "b.txt"))
		return os.IsNotExist(err) && !tt.HasText("b.txt")
	})

	// A local entry's menu uploads.
	write(t, filepath.Join(fc.fs.local.dir, "up.txt"), "up")
	a.refreshSides(fc)
	waitFor(t, tt, "up.txt", func() bool { return tt.HasText("up.txt") })
	tt.RightClick("up.txt")
	if got := menuOf(a); !slices.Contains(got, "Upload") || slices.Contains(got, "Permissions…") {
		t.Errorf("local menu %q", got)
	}
	chooseMenu(a, tt, "Upload")
	waitFor(t, tt, "menu upload", func() bool { return read(filepath.Join(root, "up.txt")) == "up" })
}

func TestFilesConflicts(t *testing.T) {
	a, tt, fc, root := filesPane(t)
	local := fc.fs.local.dir
	for _, n := range []string{"a.txt", "c.txt", "d.txt"} {
		write(t, filepath.Join(local, n), "new "+n)
		write(t, filepath.Join(root, n), "old "+n)
	}
	paths := []string{filepath.Join(local, "a.txt"), filepath.Join(local, "c.txt"), filepath.Join(local, "d.txt")}
	a.Upload(fc, paths, fc.fs.remote.dir)
	waitFor(t, tt, "conflict", func() bool { return tt.HasText("Replace the existing file?") })
	if !tt.HasText("“a.txt” already exists") || !tt.HasText("Do this for the other 2 too") {
		t.Errorf("dialog %q", tt.Texts())
	}
	mustClick(t, tt, "Keep Both")
	waitFor(t, tt, "second conflict", func() bool { return tt.HasText("“c.txt” already exists") })
	mustClick(t, tt, "Skip")
	waitFor(t, tt, "third conflict", func() bool { return tt.HasText("“d.txt” already exists") })
	if tt.HasText("Do this for the other") {
		t.Error("apply to all offered for the last one")
	}
	mustClick(t, tt, "Replace")
	waitFor(t, tt, "uploads", func() bool {
		return read(filepath.Join(root, "a (2).txt")) == "new a.txt" && read(filepath.Join(root, "d.txt")) == "new d.txt"
	})
	fc.fs.transfers.Wait()
	if read(filepath.Join(root, "a.txt")) != "old a.txt" || read(filepath.Join(root, "c.txt")) != "old c.txt" {
		t.Error("kept or skipped files changed")
	}

	// Once for all
	a.Upload(fc, paths, fc.fs.remote.dir)
	waitFor(t, tt, "conflict", func() bool { return tt.HasText("Do this for the other 2 too") })
	mustClick(t, tt, "Do this for the other 2 too")
	mustClick(t, tt, "Replace")
	waitFor(t, tt, "replaced", func() bool {
		return read(filepath.Join(root, "a.txt")) == "new a.txt" && read(filepath.Join(root, "c.txt")) == "new c.txt"
	})
	if tt.HasText("Replace the existing file?") {
		t.Error("asked again")
	}

	// Cancel queues nothing.
	write(t, filepath.Join(local, "a.txt"), "newer")
	n := len(fc.fs.transfers.List())
	a.Upload(fc, paths[:1], fc.fs.remote.dir)
	waitFor(t, tt, "conflict", func() bool { return tt.HasText("Replace the existing file?") })
	mustClick(t, tt, "Cancel")
	tt.Frame()
	if len(fc.fs.transfers.List()) != n || fc.fs.conflict != nil {
		t.Error("canceled transfer queued")
	}
}

func TestFilesDragDropAndQueue(t *testing.T) {
	a, tt, fc, root := filesPane(t)
	local := fc.fs.local.dir
	write(t, filepath.Join(local, "drag.txt"), "dragged")
	write(t, filepath.Join(root, "down.txt"), "down")
	a.refreshSides(fc)
	waitFor(t, tt, "listings", func() bool { return tt.HasText("drag.txt") && tt.HasText("down.txt") })

	// A local row dragged onto the remote side uploads.
	drag := func(from, to string) {
		x, y := nameAt(t, tt, from)
		tx, ty := center(t, tt, to)
		tt.ClickAt(x, y)        // a row chosen drags; elsewhere, a drag chooses
		tt.Advance(time.Second) // not a double click
		tt.Frame()
		tt.Press(x, y)
		tt.Move(x+10, y+5)
		tt.Move(tx, ty+60)
		tt.Release(tx, ty+60)
		tt.Frame()
	}
	drag("drag.txt", "Remote")
	waitFor(t, tt, "drag upload", func() bool { return read(filepath.Join(root, "drag.txt")) == "dragged" })
	// The remote side lists it, not its partial file, before the next
	// drag picks a row there.
	waitFor(t, tt, "listed", func() bool {
		es := fc.fs.remote.entries
		return len(es) == 2 && es[0].Name == "down.txt" && es[1].Name == "drag.txt"
	})
	tt.Frame()
	// and a remote one onto the local side downloads.
	drag("down.txt", "Local")
	waitFor(t, tt, "drag download", func() bool { return read(filepath.Join(local, "down.txt")) == "down" })

	// A folder dropped from the file manager uploads with what is in it.
	tree := filepath.Join(t.TempDir(), "tree")
	write(t, filepath.Join(tree, "x", "y", "z.txt"), "deep")
	write(t, filepath.Join(tree, "top.txt"), "top")
	x, y := center(t, tt, "Remote")
	if !tt.DropFiles(x, y+60, tree) {
		t.Fatal("drop not taken")
	}
	waitFor(t, tt, "folder upload", func() bool {
		return read(filepath.Join(root, "tree", "x", "y", "z.txt")) == "deep" && read(filepath.Join(root, "tree", "top.txt")) == "top"
	})
	fc.fs.transfers.Wait()
	tt.Frame()

	// The queue is in a sheet the header's button opens: rows say they
	// are done, and clear.
	if tt.HasText("z.txt") {
		t.Fatal("queue shown in the pane")
	}
	mustClick(t, tt, "Transfers")
	waitFor(t, tt, "rows", func() bool { return tt.HasText("z.txt") && tt.HasText("Done") })
	if !tt.HasText("0 active · 4 of 4 done") {
		t.Errorf("summary in %q", tt.Texts())
	}
	mustClick(t, tt, "Clear finished")
	if len(fc.fs.transfers.List()) != 0 || !tt.HasText("No transfers yet") {
		t.Error("not cleared")
	}
	mustClick(t, tt, "Close")
	tt.Frame()
	if fc.fs.queueOpen || tt.HasText("No transfers yet") {
		t.Error("sheet not closed")
	}
}

// TestFilesTransfersBadge counts what is still to go on the header's
// transfers button, and failures in red once nothing is.
func TestFilesTransfersBadge(t *testing.T) {
	_, tt, fc, root := filesPane(t)
	trs := fc.fs.transfers
	write(t, filepath.Join(root, "big.bin"), string(make([]byte, 1<<20)))
	trs.SetConcurrency(1)
	dst := filepath.Join(fc.fs.local.dir, "big.bin")
	for i, name := range []string{"a.bin", "b.bin", "c.bin"} {
		id := trs.Add(sftpsvc.Download, sftpsvc.RemoteJoin(fc.fs.remote.dir, "big.bin"), dst+strconv.Itoa(i), name)
		trs.Pause(id)
	}
	badge := func() bool { return slices.Contains(tt.Texts(), "3") }
	tt.Frame()
	if !badge() {
		t.Fatalf("no badge in %q", tt.Texts())
	}
	trs.CancelAll()
	tt.Frame()
	if !badge() || tt.HasText("a.bin") {
		t.Fatalf("no failure badge, or the queue in the pane: %q", tt.Texts())
	}
	trs.ClearFinished()
	tt.Frame()
	if badge() {
		t.Errorf("badge with nothing queued: %q", tt.Texts())
	}
}

// TestFilesSplit resizes the sides by dragging the line between them.
func TestFilesSplit(t *testing.T) {
	_, tt, fc, _ := filesPane(t)
	if fc.fs.split != 50 {
		t.Fatalf("split %v", fc.fs.split)
	}
	lx, ly := center(t, tt, "Local")
	rx, _ := center(t, tt, "Remote")
	r, ok := tt.Find("Divider")
	if !ok {
		t.Fatalf("no divider in %q", tt.Texts())
	}
	x := r.X + r.W/2
	if x < lx || x > rx {
		t.Fatalf("divider at %v, not between %v and %v", x, lx, rx)
	}
	tt.Press(x, ly+100)
	tt.Move(x-10, ly+100)
	tt.Move(x-120, ly+100)
	tt.Release(x-120, ly+100)
	tt.Frame()
	if fc.fs.split >= 50 {
		t.Errorf("split %v after dragging left", fc.fs.split)
	}
}

func TestFilesQueueControls(t *testing.T) {
	_, tt, fc, root := filesPane(t)
	trs := fc.fs.transfers
	// A big file and one byte at a time keeps them going.
	write(t, filepath.Join(root, "big.bin"), string(make([]byte, 4<<20)))
	trs.SetConcurrency(1)
	fc.fs.queueOpen = true
	dst := filepath.Join(fc.fs.local.dir, "big.bin")
	id := trs.Add(sftpsvc.Download, sftpsvc.RemoteJoin(fc.fs.remote.dir, "big.bin"), dst, "big.bin")
	id2 := trs.Add(sftpsvc.Download, sftpsvc.RemoteJoin(fc.fs.remote.dir, "big.bin"), dst+"2", "big2.bin")
	trs.Pause(id)
	trs.Pause(id2)
	tt.Frame()
	if !tt.HasText("Paused at") {
		t.Fatalf("no paused row in %q", tt.Texts())
	}
	mustClick(t, tt, "Resume all")
	waitFor(t, tt, "done", func() bool {
		ts := trs.List()
		return len(ts) == 2 && ts[0].State == sftpsvc.Done && ts[1].State == sftpsvc.Done
	})
	tt.Frame()
	// Cancel and retry, one row at a time.
	id3 := trs.Add(sftpsvc.Download, sftpsvc.RemoteJoin(fc.fs.remote.dir, "big.bin"), dst+"3", "big3.bin")
	trs.Pause(id3)
	tt.Frame()
	mustClick(t, tt, "Cancel big3.bin")
	tt.Frame()
	if !tt.HasText("Canceled") {
		t.Fatalf("no canceled row in %q", tt.Texts())
	}
	mustClick(t, tt, "Retry big3.bin")
	waitFor(t, tt, "retried", func() bool { return read(dst+"3") == read(filepath.Join(root, "big.bin")) })
}

// TestFilesTenThousand lists 10,000 remote files and scrolls through
// them: the table builds only the rows it shows, so a frame costs about
// what it does with 200 files (compared rather than timed, as -race is
// slow).
func TestFilesTenThousand(t *testing.T) {
	a, tt, fc, root := filesPane(t)
	// Kept from before either listing: listRemote replaces remote.dir
	// with the folder it opened. The server wants this SFTP path; on
	// Windows the local path is not one, and joining it on doubles it.
	home := fc.fs.remote.dir
	scroll := func(n int) time.Duration {
		name := fmt.Sprintf("many%d", n)
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := range n {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%05d.txt", i)), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		a.listRemote(&fc.fs.remote, sftpsvc.RemoteJoin(home, name))
		waitFor(t, tt, "listed", func() bool { return len(fc.fs.remote.entries) == n })
		tt.Frame()
		r, ok := tt.Find("f00000.txt")
		if !ok {
			t.Fatalf("first row missing: %q", tt.Texts())
		}
		const frames = 40
		start := time.Now()
		for i := range frames {
			dy := float32(60)
			if i%2 == 1 && n < 1000 { // stay inside the short list
				dy = -60
			}
			tt.Wheel(r.X+r.W/2, r.Y+r.H/2, 0, dy)
			tt.Advance(16 * time.Millisecond)
		}
		if n := len(tt.Texts()); n > 400 {
			t.Errorf("%d texts in a frame: rows are not virtualized", n)
		}
		return time.Since(start) / frames
	}
	small := scroll(200)
	big := scroll(10000)
	t.Logf("%v per frame with 200 files, %v with 10,000", small, big)
	if tt.HasText("f00000.txt") {
		t.Error("did not scroll")
	}
	if big > 3*small+20*time.Millisecond {
		t.Errorf("10,000 files: %v per frame, 200 files: %v", big, small)
	}
}

// TestFilesDragBetweenPanes: rows drag between two files panes too. A
// local row of one uploads to the other's remote folder; a remote row of
// one downloads into the other's local folder, on its own connection.
func TestFilesDragBetweenPanes(t *testing.T) {
	a, tt, fc, root := filesPane(t)
	id, err := a.OpenFiles(fc.host.ID, openAt{pane: fc.id, edge: layout.Right}, fc)
	if err != nil {
		t.Fatal(err)
	}
	fc2 := a.conns[id]
	waitFor(t, tt, "second pane connected", func() bool { return fc2.fs.remote.client != nil })
	write(t, filepath.Join(root, "sub", "in-sub.txt"), "sub")
	write(t, filepath.Join(root, "from-remote.txt"), "remote one")
	write(t, filepath.Join(fc.fs.local.dir, "from-local.txt"), "local one")
	other := filepath.Join(t.TempDir(), "elsewhere")
	write(t, filepath.Join(other, "keep.txt"), "x")
	a.listLocal(&fc2.fs.local, other)
	a.listRemote(&fc2.fs.remote, sftpsvc.RemoteJoin(fc.fs.remote.dir, "sub"))
	a.refreshSides(fc)
	waitFor(t, tt, "listings", func() bool {
		return tt.HasText("in-sub.txt") && tt.HasText("keep.txt") && tt.HasText("from-remote.txt") && tt.HasText("from-local.txt")
	})
	drag := func(from, to string) {
		t.Helper()
		x, y := nameAt(t, tt, from)
		tx, ty := center(t, tt, to)
		tt.ClickAt(x, y)        // a row chosen drags; elsewhere, a drag chooses
		tt.Advance(time.Second) // not a double click
		tt.Frame()
		tt.Press(x, y)
		tt.Move(x+10, y+5)
		tt.Move(tx, ty)
		tt.Release(tx, ty)
		tt.Frame()
	}
	// pane 1's local row onto pane 2's remote list (its "sub" folder)
	drag("from-local.txt", "in-sub.txt")
	waitFor(t, tt, "cross-pane upload", func() bool { return read(filepath.Join(root, "sub", "from-local.txt")) == "local one" })
	// pane 1's remote row onto pane 2's local list
	drag("from-remote.txt", "keep.txt")
	waitFor(t, tt, "cross-pane download", func() bool { return read(filepath.Join(other, "from-remote.txt")) == "remote one" })
	// a remote row onto another pane's remote side is not a transfer
	drag("from-remote.txt", "in-sub.txt")
	fc.fs.transfers.Wait()
	fc2.fs.transfers.Wait()
	if _, err := os.Stat(filepath.Join(root, "sub", "from-remote.txt")); err == nil {
		t.Error("copied between remotes")
	}
}

// TestFilesClickName: a row's name drags it, and still chooses the row
// when clicked and opens it when double-clicked.
func TestFilesClickName(t *testing.T) {
	a, tt, fc, root := filesPane(t)
	write(t, filepath.Join(root, "a.txt"), "hello")
	write(t, filepath.Join(root, "dir", "inner.txt"), "x")
	a.refreshSides(fc)
	waitFor(t, tt, "a.txt", func() bool { return tt.HasText("a.txt") })
	s := &fc.fs.remote

	mustClick(t, tt, "a.txt")
	tt.Frame()
	if e, ok := selected(s.sel, s.entries); !ok || e.Name != "a.txt" {
		t.Fatalf("clicking the name chose %d of %v", s.sel, s.entries)
	}
	mustClick(t, tt, "dir")
	mustClick(t, tt, "dir")
	waitFor(t, tt, "into dir", func() bool { return tt.HasText("inner.txt") })
	tt.Frame()
	if s.sel != -1 {
		t.Errorf("a new folder starts with row %d chosen", s.sel)
	}
	mustClick(t, tt, "inner.txt")
	tt.Frame()
	if e, ok := selected(s.sel, s.entries); !ok || e.Name != "inner.txt" {
		t.Fatalf("a click after opening a folder chose %d of %v", s.sel, s.entries)
	}
	tt.Advance(time.Second) // not a double click with the one before
	mustClick(t, tt, "inner.txt")
	mustClick(t, tt, "inner.txt")
	waitFor(t, tt, "editor", func() bool {
		ed := a.focused()
		return ed != nil && ed.ed != nil
	})
}
