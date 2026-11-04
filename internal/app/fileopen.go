package app

import (
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
	"github.com/Licoy/sshyard/internal/sftpsvc"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// appFiles are the extensions of files made for other apps (documents,
// media, archives, programs, fonts, databases): a double click asks how to
// open one instead of reading it into the editor.
var appFiles = map[string]bool{}

func init() {
	for _, ext := range strings.Fields(`
		pdf doc docx xls xlsx ppt pptx odt ods odp rtf pages numbers key epub
		mp3 wav flac aac ogg oga m4a wma aif aiff opus mid midi
		mp4 m4v mov avi mkv webm wmv flv mpg mpeg 3gp
		zip tar gz tgz bz2 tbz2 xz txz zst 7z rar lz lzma cab iso dmg img
		exe msi dll so dylib bin o a lib class jar war apk ipa deb rpm pkg app
		ttf otf woff woff2 eot
		db sqlite sqlite3 mdb accdb
		ico icns tif tiff psd ai sketch fig heic heif avif raw cr2 nef
		pyc wasm`) {
		appFiles[ext] = true
	}
}

// appFile says whether the file name is one made for another app.
func appFile(name string) bool {
	return appFiles[strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))]
}

// sideBase is the name of the entry at p, a path of a side.
func sideBase(remote bool, p string) string {
	if remote {
		return path.Base(p)
	}
	return filepath.Base(p)
}

// openEntry opens an entry of a side, as a double click or Enter does: a
// folder lists, an image shows, a file made for another app asks how to
// open it, and another file opens in a tab of the editor window of its host.
func (a *App) openEntry(cn *conn, remote bool, e sftpsvc.Entry) {
	sd := cn.fs.side(remote)
	p := sidePath(remote, sd.dir, e.Name)
	switch {
	case e.IsDir:
		a.goTo(cn, remote, p)
	case isImage(e):
		a.openPreview(cn, remote, e.Name)
	case appFile(e.Name):
		a.askOpenWith(cn, &withAsk{remote: remote, path: p, size: e.Size})
	default:
		a.editFile(cn, remote, p, e.Size, false)
	}
}

// openAsk confirms opening a large file in the editor.
type openAsk struct {
	remote bool
	path   string
	size   int64
	asText bool
}

// openTimeout is how long opening or saving waits for the connection.
const openTimeout = 30 * time.Second

// editorHardReadLimit is the most the editor reads, confirmed or not.
const editorHardReadLimit int64 = 256 << 20

// editFile opens a file of a side in the editor: the tab already showing
// it, else after asking about a large one. A file that is not
// text asks how to open it instead, unless asText.
func (a *App) editFile(cn *conn, remote bool, p string, size int64, asText bool) {
	if ecn, ed := a.editorTab(cn.host.ID, remote, p); ed != nil {
		a.showTab(ecn, ed)
		return
	}
	if remote && cn.fs.remote.client == nil {
		return
	}
	if size > int64(a.settings.OpenConfirmMB)<<20 {
		cn.fs.open, cn.fs.openOpen = &openAsk{remote: remote, path: p, size: size, asText: asText}, true
		return
	}
	a.readForEditor(cn, remote, p, false, asText)
}

// readForEditor reads a file for the editor, within the size opening
// asks from (or the editor's ceiling once a larger one was confirmed).
func (a *App) readForEditor(cn *conn, remote bool, p string, sizeConfirmed, asText bool) {
	limit := min(int64(a.settings.OpenConfirmMB)<<20, editorHardReadLimit)
	if sizeConfirmed {
		limit = editorHardReadLimit
	}
	get := cn.fs.transfers.Client
	go func() {
		var data []byte
		var ver sftpsvc.Version
		var err error
		if remote {
			ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
			defer cancel()
			var c *sftpsvc.Client
			if c, err = get(ctx); err == nil {
				data, ver, err = c.ReadFileContext(ctx, p, limit)
			}
		} else {
			data, ver, err = sftpsvc.ReadLocalFile(p, limit)
		}
		a.post(func() {
			switch {
			case err != nil:
				a.openFailed(cn, sideBase(remote, p), err)
			case !asText && !sftpsvc.IsText(data):
				a.askOpenWith(cn, &withAsk{remote: remote, path: p, size: ver.Size, data: data, ver: ver})
			default:
				a.openFileEditor(cn, remote, p, data, ver)
			}
		})
	}()
}

// openFailed says, in the window of cn, that the file name did not open.
func (a *App) openFailed(cn *conn, name string, err error) {
	a.toastIn(cn, func(m locales.Messages) ui.Toast {
		return ui.Toast{Title: m.OpenFailed(shownName(name), securityErrorText(m, err)), Type: "error"}
	})
}

// openAskView confirms opening a large file in the editor.
func (a *App) openAskView(c *ui.Context, cn *conn, m locales.Messages) {
	fs := cn.fs
	ask := fs.open
	if ask == nil {
		return
	}
	// The name may be the server's: shown as in the skip notice, so that
	// it cannot break the line or reorder what the question says.
	name := shownName(sideBase(ask.remote, ask.path))
	msg := m.OpenLargeMessage(name, humanSize(ask.size))
	if !ask.remote {
		msg = m.OpenLargeLocalMessage(name, humanSize(ask.size))
	}
	if ui.AlertDialog(c.Key("openask"), &fs.openOpen, m.OpenLargeTitle(), msg, m.Cancel(), m.OpenAnyway()) == 1 {
		a.readForEditor(cn, ask.remote, ask.path, true, ask.asText)
	}
	if !fs.openOpen && fs.open == ask {
		fs.open = nil
	}
}

// withAsk asks how to open a file the app does not show: in its default
// app, in an app chosen, as text, or downloaded (a remote one). data is
// the file, when it was read for the editor and is not text.
type withAsk struct {
	remote bool
	path   string
	size   int64
	data   []byte
	ver    sftpsvc.Version
}

func (a *App) askOpenWith(cn *conn, w *withAsk) {
	cn.fs.with, cn.fs.withOpen = w, true
}

// openWith opens a file of a side in another app, its default one or
// one chosen; a remote file a program could be made to look like a
// document asks first.
func (a *App) openWith(cn *conn, remote bool, p string, size int64, choose bool, m locales.Messages) {
	w := &withAsk{remote: remote, path: p, size: size}
	if remote && sftpsvc.Executable(path.Base(p)) {
		a.askOpenWith(cn, w)
		return
	}
	a.openOutside(cn, w, choose, m)
}

// withView is the sheet asking how to open a file the app does not show.
func (a *App) withView(c *ui.Context, cn *conn, tok theme.Tokens, m locales.Messages) {
	fs := cn.fs
	w := fs.with
	if w == nil {
		return
	}
	name := sideBase(w.remote, w.path)
	sheet(ui.Modal(c.Key("openwith"), &fs.withOpen, func() {
		ui.Column(c).Gap(14).Width(380).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				fileImage(c, entryIcon(sftpsvc.Entry{Name: name}), 30)
				ui.Text(c, m.OpenWithTitle(shownName(name))).FontSize(theme.Title3).FontWeight(600).Shrink(1).MinWidth(0).SingleLine()
			})
			ui.Text(c, m.OpenWithMessage()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			if w.remote {
				ui.Text(c, m.OpenWithCopyNote()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
				if sftpsvc.Executable(name) {
					col := tok.Warning
					ui.Row(c).FillWidth().Padding(6, 10).Gap(theme.Space(2)).AlignItems(ui.Start).
						Radius(theme.RadiusControl).Background(col.Alpha(0.12)).Border(1, col.Alpha(0.22)).Children(func() {
						ui.Icon(c, icons.Warning.SVG).Size(14, 14).Shrink(0).TextColor(col).Margin(2, 0, 0, 0)
						ui.Text(c, m.OpenExecMessage(shownName(name))).FontSize(theme.Footnote).Grow(1).Shrink(1)
					})
				}
			}
			ui.Column(c).Gap(8).FillWidth().Margin(4, 0, 0, 0).Children(func() {
				choice := func(label string, primary, disabled bool) bool {
					el, ok := pillEl(c, label, primary, false, disabled)
					el.FillWidth()
					return ok
				}
				if choice(m.OpenWithDefault(), true, a.p.OpenPath == nil) {
					fs.withOpen = false
					a.openOutside(cn, w, false, m)
				}
				if choice(m.ChooseApp(), false, a.p.OpenWith == nil) {
					fs.withOpen = false
					a.openOutside(cn, w, true, m)
				}
				if choice(m.OpenAsText(), false, false) {
					fs.withOpen = false
					if w.data != nil {
						a.openFileEditor(cn, w.remote, w.path, w.data, w.ver)
					} else {
						a.editFile(cn, w.remote, w.path, w.size, true)
					}
				}
				if w.remote && choice(m.DownloadTo(), false, a.p.PickFolder == nil) {
					fs.withOpen = false
					a.downloadTo(cn, []string{w.path}, m)
				}
				if choice(m.Cancel(), false, false) {
					fs.withOpen = false
				}
			})
		})
	}), tok)
	if !fs.withOpen && fs.with == w {
		fs.with = nil
	}
}

// openOutside opens a file of a side in another app: its default one, or
// one the user chooses. A remote file is copied to the pane's folder of
// the cache first, which only the user can read, and opens from there:
// what is changed in the copy is not uploaded.
func (a *App) openOutside(cn *conn, w *withAsk, choose bool, m locales.Messages) {
	name := sideBase(w.remote, w.path)
	title := m.ChooseAppTitle(shownName(name))
	if !w.remote {
		a.openLocalApp(cn, w.path, choose, title, "")
		return
	}
	// Before anything is written: the name comes from the server, and
	// must be safe as a local name and stay in the copy's folder.
	if err := sftpsvc.SafeName(name); err != nil {
		a.openFailed(cn, name, err)
		return
	}
	local, err := a.copyPath(&cn.fs.copies, name)
	if err != nil {
		a.openFailed(cn, name, err)
		return
	}
	if w.data != nil {
		if err := writeLocalCopy(local, w.data, 0o600); err != nil {
			os.RemoveAll(filepath.Dir(local))
			a.openFailed(cn, name, err)
			return
		}
		os.Chmod(local, 0o600) // whatever the umask
		a.openLocalApp(cn, local, choose, title, name)
		return
	}
	id := cn.fs.transfers.Add(sftpsvc.Download, w.path, local, name)
	cn.fs.opens = append(cn.fs.opens, pendingOpen{id: id, local: local, name: name, choose: choose, title: title})
}

// pendingOpen is a remote file downloading into a copy, to open in
// another app once it is there.
type pendingOpen struct {
	id          int // the transfer
	local, name string
	choose      bool
	title       string
}

// pendingOpens opens the copies downloaded since the last frame, and
// forgets those that failed or were canceled.
func (a *App) pendingOpens(cn *conn) {
	fs := cn.fs
	if len(fs.opens) == 0 {
		return
	}
	byID := map[int]sftpsvc.Transfer{}
	for _, t := range fs.transfers.List() {
		byID[t.ID] = t
	}
	left := fs.opens[:0]
	for _, o := range fs.opens {
		t, ok := byID[o.id]
		switch {
		case !ok || t.State == sftpsvc.Canceled:
			os.RemoveAll(filepath.Dir(o.local))
		case t.State == sftpsvc.Failed:
			os.RemoveAll(filepath.Dir(o.local))
			a.openFailed(cn, o.name, t.Err)
		case t.State == sftpsvc.Done:
			local := o.local
			if t.Destination != "" {
				local = t.Destination
			}
			os.Chmod(local, 0o600)
			a.openLocalApp(cn, local, o.choose, o.title, o.name)
		default:
			left = append(left, o)
		}
	}
	fs.opens = left
}

// openLocalApp opens the local file p in its default app, or in one the user
// chooses (title asks for it), off the UI thread. copied, when set, is
// the name of the remote file p is a copy of, which a toast in the window
// of cn says.
func (a *App) openLocalApp(cn *conn, p string, choose bool, title, copied string) {
	open := a.p.OpenPath
	if choose && a.p.OpenWith != nil {
		open = func(p string) error { return a.p.OpenWith(p, title) }
	} else if choose {
		open = nil
	}
	if open == nil {
		a.openFailed(cn, filepath.Base(p), errors.New("no app to open files with"))
		return
	}
	go func() {
		err := open(p)
		a.post(func() {
			if err != nil {
				a.openFailed(cn, filepath.Base(p), err)
				return
			}
			if copied != "" {
				a.toastIn(cn, func(m locales.Messages) ui.Toast { return ui.Toast{Title: m.OpenedLocally(shownName(copied))} })
			}
		})
	}()
}

// localCopies are the copies of remote files a files pane opened in
// other apps, in the pane's folder of the cache, made on first use and
// deleted with the pane.
type localCopies struct {
	mu  sync.Mutex
	dir string
	// next numbers the copies' folders in dir; never reused, a failed
	// copy's folder (removed) included.
	next int
}

// writeLocalCopy writes a copy (a variable for tests).
var writeLocalCopy = os.WriteFile

// copyPath makes a new folder in the pane's folder of the cache for a
// copy of the remote file name, and is the copy's path there.
func (a *App) copyPath(lc *localCopies, name string) (string, error) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if lc.dir == "" {
		root, err := a.cacheDir()
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(root, 0o700); err != nil {
			return "", err
		}
		b := make([]byte, 6)
		rand.Read(b)
		// Named for this process, so that a later start knows when it is
		// gone (cleanCache).
		dir := filepath.Join(root, strconv.Itoa(os.Getpid())+"-"+hex.EncodeToString(b))
		if err := os.Mkdir(dir, 0o700); err != nil {
			return "", err
		}
		lc.dir = dir
	}
	sub := filepath.Join(lc.dir, strconv.Itoa(lc.next))
	local, err := sftpsvc.LocalPath(sub, name)
	if err != nil {
		return "", err
	}
	lc.next++
	if err := os.Mkdir(sub, 0o700); err != nil {
		return "", err
	}
	return local, nil
}

// close deletes the copies.
func (lc *localCopies) close() {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if lc.dir != "" {
		os.RemoveAll(lc.dir)
		lc.dir = ""
	}
}

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
