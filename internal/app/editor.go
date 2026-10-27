package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/sftpsvc"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// editState is an editor pane's file: the remote path, the text and the
// version of the file it was read or last saved as.
type editState struct {
	path, name string
	st         *editor.State
	ver        sftpsvc.Version
	saving     bool
	loading    bool
	err        string
	// conflictOpen asks what to do with a file changed on the server
	conflictOpen bool
}

// openAsk confirms opening a large or binary file, in the editor or in
// another app (local), or a program file in another app (exec). data is
// the file already read, for a binary one.
type openAsk struct {
	path   string
	size   int64
	local  bool
	binary bool
	exec   bool
	data   []byte
	ver    sftpsvc.Version
}

// openTimeout is how long opening or saving waits for the connection.
const openTimeout = 30 * time.Second

const editorHardReadLimit int64 = 256 << 20

// openRemote opens a remote file of a files pane in an editor pane, in
// a new tab, after asking when it is large or binary.
func (a *App) openRemote(cn *conn, p string, size int64) { a.startOpen(cn, p, size, false) }

// openLocalProgram opens a remote file in its default app, on a copy
// whose saves upload.
func (a *App) openLocalProgram(cn *conn, p string, size int64) { a.startOpen(cn, p, size, true) }

// startOpen asks first, in another app, about a file of a type opening
// may run (the server names it: it could be a program made to look like
// a document), then goes on with checkSize.
func (a *App) startOpen(cn *conn, p string, size int64, local bool) {
	if local && sftpsvc.Executable(path.Base(p)) {
		cn.fs.open, cn.fs.openOpen = &openAsk{path: p, size: size, local: local, exec: true}, true
		return
	}
	a.checkSize(cn, p, size, local)
}

// checkSize asks about a large file, or reads it.
func (a *App) checkSize(cn *conn, p string, size int64, local bool) {
	// Before the read, and before asking about a large file we will not
	// write. A program still asks first (startOpen); confirming comes
	// back here. A server that cleans "\" would otherwise report the
	// name as missing.
	if local {
		if err := sftpsvc.SafeName(path.Base(p)); err != nil {
			a.openFailed(p, err)
			return
		}
	}
	if limit := int64(a.settings.OpenConfirmMB) << 20; size > limit {
		cn.fs.open, cn.fs.openOpen = &openAsk{path: p, size: size, local: local}, true
		return
	}
	a.fetchOpen(cn, p, local, false)
}

// fetchOpen reads within the approved size, then independently asks about
// binary content before opening it. Binary confirmation uses the cached data.
func (a *App) fetchOpen(cn *conn, p string, local, sizeConfirmed bool) {
	get := cn.fs.transfers.Client
	readLimit := min(int64(a.settings.OpenConfirmMB)<<20, editorHardReadLimit)
	if sizeConfirmed {
		readLimit = editorHardReadLimit
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
		defer cancel()
		var data []byte
		var ver sftpsvc.Version
		c, err := get(ctx)
		if err == nil {
			data, ver, err = c.ReadFileContext(ctx, p, readLimit)
		}
		a.post(func() {
			if err != nil {
				a.openFailed(p, err)
				return
			}
			if !sftpsvc.IsText(data) {
				cn.fs.open, cn.fs.openOpen = &openAsk{path: p, size: ver.Size, local: local, binary: true, data: data, ver: ver}, true
				return
			}
			a.finishOpen(cn, p, data, ver, local)
		})
	}()
}

// Explicitly confirmed files may be large, but every in-memory read has a ceiling.
func editorReadLimit(size int64) int64 {
	if size > sftpsvc.DefaultReadLimit {
		return editorHardReadLimit
	}
	return sftpsvc.DefaultReadLimit
}

func (a *App) openFailed(p string, err error) {
	a.toast(func(m locales.Messages) ui.Toast {
		return ui.Toast{Title: m.OpenFailed(path.Base(p), securityErrorText(m, err)), Type: "error"}
	})
}

func (a *App) finishOpen(cn *conn, p string, data []byte, ver sftpsvc.Version, local bool) {
	if local {
		if err := a.openLocal(cn, p, data, ver); err != nil {
			a.openFailed(p, err)
		}
		return
	}
	a.openFileEditor(cn, p, data, ver)
}

// newEditState is an empty editor for path ("" when the pane is new).
func newEditState(p string) *editState {
	buf := editor.NewBuffer("")
	name := ""
	if p != "" {
		buf.SetLanguage(editor.LanguageFor(path.Base(p)))
		name = path.Base(p)
	}
	return &editState{path: p, name: name, st: &editor.State{Buffer: buf}}
}

// openFileEditor opens an editor pane on the file read, in a new tab, on its
// own (pooled) connection to the host.
func (a *App) openFileEditor(from *conn, p string, data []byte, ver sftpsvc.Version) {
	id, err := a.open(from.host.ID, layout.Editor, openAt{}, from)
	if err != nil {
		a.openFailed(p, err)
		return
	}
	cn := a.conns[id]
	buf := editor.NewBuffer(string(data))
	buf.SetLanguage(editor.LanguageFor(path.Base(p)))
	cn.ed = &editState{path: p, name: path.Base(p), ver: ver, st: &editor.State{Buffer: buf}}
	cn.fs.remote.dir = path.Dir(p)
	a.invalidate()
}

// saveEditor writes the editor's text to its file, unless it changed on
// the server since (then asks), or anyway when force.
func (a *App) saveEditor(cn *conn, force bool) {
	ed := cn.ed
	if ed == nil || ed.saving || ed.loading {
		return
	}
	text := ed.st.Buffer.String()
	was := ed.ver
	if force {
		was = sftpsvc.Version{}
	}
	ed.saving, ed.err = true, ""
	get := cn.fs.transfers.Client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
		defer cancel()
		var ver sftpsvc.Version
		c, err := get(ctx)
		if err == nil {
			ver, err = c.WriteFile(ed.path, []byte(text), was)
		}
		a.post(func() {
			ed.saving = false
			switch {
			case errors.Is(err, sftpsvc.ErrChanged):
				ed.conflictOpen = true
			case err != nil:
				ed.err = err.Error()
			default:
				ed.ver = ver
				if ed.st.Buffer.String() == text {
					ed.st.Buffer.MarkSaved()
				}
				a.toast(func(m locales.Messages) ui.Toast { return ui.Toast{Title: m.EditorSaved(ed.name), Type: "success"} })
			}
		})
	}()
}

// reloadEditor reads the file again, over what was edited.
func (a *App) reloadEditor(cn *conn) {
	ed := cn.ed
	ed.loading, ed.err = true, ""
	get := cn.fs.transfers.Client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
		defer cancel()
		var data []byte
		var ver sftpsvc.Version
		c, err := get(ctx)
		if err == nil {
			data, ver, err = c.ReadFileContext(ctx, ed.path, editorReadLimit(ed.ver.Size))
		}
		a.post(func() {
			ed.loading = false
			if err != nil {
				ed.err = err.Error()
				return
			}
			ed.ver = ver
			ed.st.Buffer.Set(string(data))
			ed.st.Buffer.MarkSaved()
		})
	}()
}

// editorActions are an editor pane's header buttons.
func (a *App) editorActions(c *ui.Context, t *layout.Tab, n *layout.Node, cn *conn, m locales.Messages) {
	ed := cn.ed
	if (IconButton{Icon: icons.Refresh, Label: m.EditorReloadFromServer(), Size: Small, Disabled: ed.loading || ed.saving}).Clicked(c, "reload") {
		t.Focus = n.ID
		a.reloadEditor(cn)
	}
	if (IconButton{Icon: icons.Save, Label: m.EditorSave(), Size: Small, Disabled: ed.saving || !ed.st.Buffer.Modified()}).Clicked(c, "save") {
		t.Focus = n.ID
		a.saveEditor(cn, false)
	}
}

// editorBody is an editor pane below its header: the text, and a status
// line with the path and whether it is edited.
func (a *App) editorBody(c *ui.Context, t *layout.Tab, n *layout.Node, cn *conn, tok theme.Tokens, m locales.Messages) {
	a.ensureSFTP(cn)
	ed := cn.ed
	body := ui.Column(c.Key("editor")).Grow(1).Fill()
	body.Children(func() {
		v := editor.View(c.Key("ed"), ed.st).Grow(1).FillWidth()
		if a.focusReq == cn.id && a.page == pageWorkspace {
			v.Focus()
			a.focusReq = 0
		}
		ui.Row(c).Height(26).Shrink(0).Padding(0, 10).Gap(8).AlignItems(ui.Center).Background(tok.Fill).Children(func() {
			ui.Text(c, ed.path).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(1)
			ui.Spacer(c)
			switch {
			case ed.saving || ed.loading:
				ui.Spinner(c)
			case ed.err != "":
				ui.Text(c, m.ErrGeneric(securityText(m, ed.err))).FontSize(theme.Caption).TextColor(tok.Danger).SingleLine().Shrink(1)
			case ed.st.Buffer.Modified():
				ui.Text(c, m.EditorModified()).FontSize(theme.Caption).TextColor(tok.Accent).SingleLine()
			}
			ui.Text(c, humanSize(int64(ed.st.Buffer.Len()))).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
		})
	})
	if body.FocusWithin() && t.Focus != n.ID {
		t.Focus = n.ID
	}
	switch ui.AlertDialog(c.Key("edconflict"), &ed.conflictOpen, m.EditorConflictTitle(), m.EditorConflictMessage(ed.name), m.Cancel(), m.EditorReload(), m.Overwrite()) {
	case 1:
		a.reloadEditor(cn)
	case 2:
		a.saveEditor(cn, true)
	}
}

// openAskView confirms opening a large or binary file.
func (a *App) openAskView(c *ui.Context, cn *conn, m locales.Messages) {
	fs := cn.fs
	ask := fs.open
	if ask == nil {
		return
	}
	// The name may be the server's: shown as in the skip notice, so that
	// it cannot break the line or reorder what the question says.
	name := shownName(path.Base(ask.path))
	title, msg := m.OpenLargeTitle(), m.OpenLargeMessage(name, humanSize(ask.size))
	if ask.binary {
		title, msg = m.OpenBinaryTitle(), m.OpenBinaryMessage(name)
	}
	if ask.exec {
		title, msg = m.OpenExecTitle(), m.OpenExecMessage(name)
	}
	if ui.AlertDialog(c.Key("openask"), &fs.openOpen, title, msg, m.Cancel(), m.OpenAnyway()) == 1 {
		if ask.exec {
			a.checkSize(cn, ask.path, ask.size, ask.local)
		} else if ask.data != nil {
			a.finishOpen(cn, ask.path, ask.data, ask.ver, ask.local)
		} else {
			a.fetchOpen(cn, ask.path, ask.local, true)
		}
	}
	if !fs.openOpen && fs.open == ask {
		fs.open = nil
	}
}

// localEdit is a remote file opened in another app: a copy in the cache
// folder, watched, whose saves upload.
type localEdit struct {
	remote, local string
	stop          chan struct{}

	mu   sync.Mutex
	sent []byte          // what the server has, as far as we know
	ver  sftpsvc.Version // its version then: an upload checks it
}

// localConflict is an upload of a copy refused because the server's
// file changed meanwhile.
type localConflict struct {
	f    *localEdit
	data []byte
}

// localEdits are the files a files pane opened in other apps.
type localEdits struct {
	mu    sync.Mutex
	dir   string // this pane's folder in the cache, made on first use
	files map[string]*localEdit
	// next numbers the copies' folders in dir; never reused, a failed
	// copy's folder (removed) included.
	next int
}

// writeLocalCopy writes a copy (a variable for tests).
var writeLocalCopy = os.WriteFile

// cacheDir is where files opened in other apps go.
func (a *App) cacheDir() (string, error) {
	if a.p.CacheDir != "" {
		return a.p.CacheDir, nil
	}
	d, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "sshyard", "open"), nil
}

// cleanCache deletes the copies of files opened in other apps that a
// process no longer running left (it quit without closing its panes):
// those of a running one, maybe another window of the app, stay, as
// does anything not named as a pane's folder.
func cleanCache(root string) {
	es, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range es {
		pid, rest, ok := strings.Cut(e.Name(), "-")
		n, err := strconv.Atoi(pid)
		if !e.IsDir() || !ok || err != nil || n <= 0 || len(rest) != 12 {
			// Named before pids were: gone once a day old.
			if fi, err := e.Info(); err == nil && e.IsDir() && len(e.Name()) == 12 && isHex(e.Name()) && time.Since(fi.ModTime()) > 24*time.Hour {
				os.RemoveAll(filepath.Join(root, e.Name()))
			}
			continue
		}
		if !isHex(rest) || n == os.Getpid() || processAlive(n) {
			continue
		}
		os.RemoveAll(filepath.Join(root, e.Name()))
	}
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil
}

// watchEvery is how often a file opened in another app is checked.
var watchEvery = time.Second

// openLocal writes the file to the pane's cache folder (only the user
// can read it), opens it in its default app, and uploads it whenever it
// is saved, until the pane closes, which deletes it.
func (a *App) openLocal(cn *conn, p string, data []byte, ver sftpsvc.Version) error {
	if a.p.OpenPath == nil {
		return errors.New("no app to open files with")
	}
	le := &cn.fs.edits
	le.mu.Lock()
	defer le.mu.Unlock()
	if le.dir == "" {
		root, err := a.cacheDir()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(root, 0o700); err != nil {
			return err
		}
		b := make([]byte, 6)
		rand.Read(b)
		// Named for this process, so that a later start knows when it
		// is gone (cleanCache).
		le.dir = filepath.Join(root, strconv.Itoa(os.Getpid())+"-"+hex.EncodeToString(b))
		if err := os.Mkdir(le.dir, 0o700); err != nil {
			return err
		}
		le.files = map[string]*localEdit{}
	}
	f := le.files[p]
	if f == nil {
		// The name comes from the server: it must be safe as a local
		// name and stay in the copy's folder.
		sub := filepath.Join(le.dir, strconv.Itoa(le.next))
		local, err := sftpsvc.LocalPath(sub, path.Base(p))
		if err != nil {
			return err
		}
		le.next++
		if err := os.Mkdir(sub, 0o700); err != nil {
			return err
		}
		f = &localEdit{remote: p, local: local, stop: make(chan struct{}), sent: data, ver: ver}
		if err := writeLocalCopy(f.local, data, 0o600); err != nil {
			os.RemoveAll(sub)
			return err
		}
		os.Chmod(f.local, 0o600) // whatever the umask
		le.files[p] = f
		go a.watchLocal(cn, f)
	}
	if err := a.p.OpenPath(f.local); err != nil {
		return err
	}
	a.toast(func(m locales.Messages) ui.Toast { return ui.Toast{Title: m.OpenedLocally(path.Base(p))} })
	return nil
}

// watchLocal uploads the copy whenever what it holds changes, once it
// stays the same for a check (an app may write it in parts), unless the
// server's file changed since it was read or uploaded: then it asks, as
// the editor does.
func (a *App) watchLocal(cn *conn, f *localEdit) {
	var last os.FileInfo
	if fi, err := os.Stat(f.local); err == nil {
		last = fi
	}
	// Set from the start: a save can land before this first look, so the
	// mtime seen here is already the new one and a later tick must still
	// compare the bytes.
	pending := last != nil
	tick := time.NewTicker(watchEvery)
	defer tick.Stop()
	for {
		select {
		case <-f.stop:
			return
		case <-tick.C:
		}
		fi, err := os.Stat(f.local)
		if err != nil {
			continue // saved by replacing it: there again soon
		}
		if last != nil && fi.ModTime().Equal(last.ModTime()) && fi.Size() == last.Size() {
			if !pending {
				continue
			}
		} else {
			last, pending = fi, true
			continue // let it settle
		}
		pending = false
		data, err := os.ReadFile(f.local)
		f.mu.Lock()
		same := bytes.Equal(data, f.sent)
		f.mu.Unlock()
		if err != nil || same {
			continue
		}
		a.uploadLocal(cn, f, data, false)
	}
}

// uploadLocal uploads data, the copy f holds, over its remote file; with
// force, even when that changed since.
func (a *App) uploadLocal(cn *conn, f *localEdit, data []byte, force bool) {
	f.mu.Lock()
	was := f.ver
	f.mu.Unlock()
	if force {
		was = sftpsvc.Version{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
	c, err := cn.fs.transfers.Client(ctx)
	var ver sftpsvc.Version
	if err == nil {
		ver, err = c.WriteFile(f.remote, data, was)
	}
	cancel()
	if err == nil {
		f.mu.Lock()
		f.sent, f.ver = data, ver
		f.mu.Unlock()
	}
	name := path.Base(f.remote)
	a.post(func() {
		switch {
		case errors.Is(err, sftpsvc.ErrChanged):
			cn.fs.upConflict, cn.fs.upConflictOpen = &localConflict{f: f, data: data}, true
		case err != nil:
			a.toast(func(m locales.Messages) ui.Toast {
				return ui.Toast{Title: m.OpenFailed(name, securityErrorText(m, err)), Type: "error"}
			})
		default:
			a.toast(func(m locales.Messages) ui.Toast { return ui.Toast{Title: m.AutoUploaded(name), Type: "success"} })
			a.refreshSides(cn)
		}
	})
}

// reloadLocal puts the server's file in f's copy, over what was saved
// there.
func (a *App) reloadLocal(cn *conn, f *localEdit) {
	ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
	defer cancel()
	c, err := cn.fs.transfers.Client(ctx)
	var data []byte
	var ver sftpsvc.Version
	if err == nil {
		data, ver, err = c.ReadFileContext(ctx, f.remote, editorReadLimit(f.ver.Size))
	}
	if err == nil {
		// Known first, so that the watcher does not upload it back.
		f.mu.Lock()
		f.sent, f.ver = data, ver
		f.mu.Unlock()
		err = os.WriteFile(f.local, data, 0o600)
	}
	if err != nil {
		name := path.Base(f.remote)
		a.post(func() {
			a.toast(func(m locales.Messages) ui.Toast {
				return ui.Toast{Title: m.OpenFailed(name, securityErrorText(m, err)), Type: "error"}
			})
		})
	}
}

// localConflictView asks what to do with a copy whose server file
// changed: keep both as they are, take the server's, or replace it.
func (a *App) localConflictView(c *ui.Context, cn *conn, m locales.Messages) {
	fs := cn.fs
	lc := fs.upConflict
	if lc == nil {
		return
	}
	switch ui.AlertDialog(c.Key("upconflict"), &fs.upConflictOpen, m.EditorConflictTitle(), m.EditorConflictMessage(path.Base(lc.f.remote)), m.Cancel(), m.EditorReload(), m.Overwrite()) {
	case 1:
		go a.reloadLocal(cn, lc.f)
	case 2:
		go a.uploadLocal(cn, lc.f, lc.data, true)
	}
	if !fs.upConflictOpen && fs.upConflict == lc {
		fs.upConflict = nil
	}
}

// close stops watching and deletes the copies.
func (le *localEdits) close() {
	le.mu.Lock()
	defer le.mu.Unlock()
	for _, f := range le.files {
		close(f.stop)
	}
	le.files = nil
	if le.dir != "" {
		os.RemoveAll(le.dir)
		le.dir = ""
	}
}

// toast shows a toast at the next frame, from the UI thread.
func (a *App) toast(t func(m locales.Messages) ui.Toast) {
	a.toasts = append(a.toasts, t)
	a.invalidate()
}
