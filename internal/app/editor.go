package app

import (
	"context"
	"errors"
	"path"
	"sync/atomic"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sftpsvc"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// editState is a file open in the editor, in a window of its own: where
// the file is (a remote path, on the conn's own pooled connection to the
// host, or one of this computer), the text and the version of the file it
// was read or last saved as.
type editState struct {
	path, name string
	local      bool
	st         *editor.State
	ver        sftpsvc.Version
	saving     bool
	loading    bool
	err        string
	// lang is the name of the syntax the text is colored as, "" for
	// plain text.
	lang string
	// conflictOpen asks what to do with a file changed since it was read,
	// closeOpen what to do with unsaved changes as the window closes;
	// closeSaved closes the window once the save asked for there is done.
	conflictOpen bool
	closeOpen    bool
	closeSaved   bool
	// win is the window once it is open, title and edited what it was
	// last told.
	win    Window
	title  string
	edited bool
	// dirty is whether the text has unsaved changes, for the window's
	// close, which asks on no frame.
	dirty atomic.Bool
	// focused is set once the text took the focus.
	focused bool
	// toasts show in the window at its next frame.
	toasts []func(m locales.Messages) ui.Toast
}

// editorReadLimit is how much reading a file again for the editor
// reads: explicitly confirmed files may be large, but every in-memory
// read has a ceiling.
func editorReadLimit(size int64) int64 {
	if size > sftpsvc.DefaultReadLimit {
		return editorHardReadLimit
	}
	return sftpsvc.DefaultReadLimit
}

// editorOn is the editor already open on the file p, of the host's
// server (remote) or of this computer.
func (a *App) editorOn(hostID string, remote bool, p string) *editState {
	for _, c := range a.conns {
		if ed := c.ed; ed != nil && ed.path == p && ed.local == !remote && (!remote || c.host.ID == hostID) {
			return ed
		}
	}
	return nil
}

// focusEditor brings the editor's window to the front.
func (a *App) focusEditor(ed *editState) {
	if w := ed.win; w != nil {
		go w.Focus()
	}
}

// openFileEditor opens the file read, data, in the editor, in a window of
// its own: a remote file on its own (pooled) connection to the host of
// the files pane it was opened from, a local one on none.
func (a *App) openFileEditor(from *conn, remote bool, p string, data []byte, ver sftpsvc.Version) {
	name := sideBase(remote, p)
	if ed := a.editorOn(from.host.ID, remote, p); ed != nil {
		a.focusEditor(ed)
		return
	}
	if a.p.OpenWindow == nil {
		a.openFailed(name, errors.New("no window for the editor"))
		return
	}
	var cn *conn
	if remote {
		c, err := a.makeConn(from.host.ID, layout.Editor, 0)
		if err != nil {
			a.openFailed(name, err)
			return
		}
		cn = c
		cn.fs.remote.dir = path.Dir(p)
	} else {
		a.nextConn++
		cn = &conn{id: a.nextConn, kind: layout.Editor, host: from.host}
		a.conns[cn.id] = cn
	}
	buf := editor.NewBuffer(string(data))
	lang := editor.LanguageFor(name)
	buf.SetLanguage(lang)
	cn.ed = &editState{path: p, name: name, local: !remote, ver: ver, st: &editor.State{Buffer: buf}, title: name}
	if lang != nil {
		cn.ed.lang = lang.Name
	}
	if remote {
		a.startConn(cn)
	}
	a.openEditorWindow(cn)
}

// openEditorWindow opens the window of an editor, off the frame.
func (a *App) openEditorWindow(cn *conn) {
	ed := cn.ed
	spec := WindowSpec{
		Title:    ed.title,
		Width:    900,
		Height:   640,
		StateKey: "editor",
		View:     func(c *ui.Context) { a.editorWindowView(c, cn) },
		Closing:  func() bool { return a.editorClosing(cn) },
		Closed:   func() { a.post(func() { a.editorClosed(cn) }) },
	}
	go func() {
		w := a.p.OpenWindow(spec)
		a.post(func() {
			if a.conns[cn.id] != cn {
				go w.Close() // closed while it opened
				return
			}
			ed.win = w
		})
	}()
}

// editorClosing says whether the editor's window may close, as the user
// closes it: with unsaved changes it stays and asks what to do with them.
func (a *App) editorClosing(cn *conn) bool {
	if !cn.ed.dirty.Load() {
		return true
	}
	a.post(func() { cn.ed.closeOpen = true })
	return false
}

// editorClosed closes the editor whose window is gone.
func (a *App) editorClosed(cn *conn) {
	if cn.ed != nil {
		cn.ed.win = nil
	}
	a.CloseSession(cn.id)
}

// closeEditorWindow closes the window of an editor being closed, and
// forgets it.
func (a *App) closeEditorWindow(cn *conn) {
	a.mu.Lock()
	delete(a.winSvc, cn.id)
	a.mu.Unlock()
	if w := cn.ed.win; w != nil {
		cn.ed.win = nil
		go w.Close()
	}
}

// editorToast shows a toast in the editor's window at its next frame.
func (a *App) editorToast(cn *conn, t func(m locales.Messages) ui.Toast) {
	cn.ed.toasts = append(cn.ed.toasts, t)
	a.invalidate()
}

// saveEditor writes the editor's text to its file, unless it changed
// since (then asks), or anyway when force.
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
	var get func(context.Context) (*sftpsvc.Client, error)
	if !ed.local {
		get = cn.fs.transfers.Client
	}
	go func() {
		var ver sftpsvc.Version
		var err error
		if ed.local {
			ver, err = sftpsvc.WriteLocalFile(ed.path, []byte(text), was)
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
			defer cancel()
			var c *sftpsvc.Client
			if c, err = get(ctx); err == nil {
				ver, err = c.WriteFile(ed.path, []byte(text), was)
			}
		}
		a.post(func() {
			ed.saving = false
			switch {
			case errors.Is(err, sftpsvc.ErrChanged):
				ed.conflictOpen, ed.closeSaved = true, false
			case err != nil:
				ed.err, ed.closeSaved = err.Error(), false
			default:
				ed.ver = ver
				if ed.st.Buffer.String() == text {
					ed.st.Buffer.MarkSaved()
					ed.dirty.Store(false)
				}
				if ed.closeSaved && !ed.st.Buffer.Modified() {
					a.CloseSession(cn.id)
					return
				}
				ed.closeSaved = false
				a.editorToast(cn, func(m locales.Messages) ui.Toast {
					return ui.Toast{Title: m.EditorSaved(shownName(ed.name)), Type: "success"}
				})
			}
		})
	}()
}

// reloadEditor reads the file again, over what was edited.
func (a *App) reloadEditor(cn *conn) {
	ed := cn.ed
	if ed.saving || ed.loading {
		return
	}
	ed.loading, ed.err = true, ""
	var get func(context.Context) (*sftpsvc.Client, error)
	if !ed.local {
		get = cn.fs.transfers.Client
	}
	limit := editorReadLimit(ed.ver.Size)
	go func() {
		var data []byte
		var ver sftpsvc.Version
		var err error
		if ed.local {
			data, ver, err = sftpsvc.ReadLocalFile(ed.path, limit)
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
			defer cancel()
			var c *sftpsvc.Client
			if c, err = get(ctx); err == nil {
				data, ver, err = c.ReadFileContext(ctx, ed.path, limit)
			}
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
			ed.dirty.Store(false)
		})
	}()
}

// editorWindowView is the editor's window: a toolbar with the file and
// where it is, the text, and a status line with its syntax, whether it is
// edited and its size.
func (a *App) editorWindowView(c *ui.Context, cn *conn) {
	if a.conns[cn.id] != cn || cn.ed == nil {
		return // closed: the window goes too
	}
	a.mu.Lock()
	if a.winSvc == nil {
		a.winSvc = map[int]ui.Services{}
	}
	a.winSvc[cn.id] = c.Services()
	a.mu.Unlock()
	a.drain()
	if a.conns[cn.id] != cn {
		return
	}
	ed := cn.ed
	if !ed.local {
		a.ensureSFTP(cn)
	}
	base := *c.Theme()
	base.Font = a.fonts.ui
	tok := theme.For(a.settings.Theme, base.Dark)
	c.SetTheme(tok.Apply(base))
	c.Root().Background(tok.Canvas)
	m := locales.In(c.Locale())
	for _, t := range ed.toasts {
		c.AddToast(t(m))
	}
	ed.toasts = nil

	place := m.Local()
	if !ed.local {
		place = a.pv(cn.host.Label())
	}
	modified := ed.st.Buffer.Modified()
	ed.dirty.Store(modified)
	if title := m.EditorWindowTitle(shownName(ed.name), place); ed.win != nil && (title != ed.title || modified != ed.edited) {
		w := ed.win
		ed.title, ed.edited = title, modified
		go func() {
			w.SetTitle(title)
			w.SetEdited(modified)
		}()
	}
	if c.Shortcut(saveMods, ui.KeyS) {
		a.saveEditor(cn, false)
	}
	busy := ed.saving || ed.loading
	ui.Column(c.Key("editorwin")).Fill().TextColor(tok.Text).Children(func() {
		ui.Row(c.Key("edbar")).Height(40).Shrink(0).FillWidth().Padding(0, theme.Space(2), 0, theme.Space(3)).Gap(theme.Space(2)).AlignItems(ui.Center).Children(func() {
			fileImage(c, entryIcon(sftpsvc.Entry{Name: ed.name}), 18)
			ui.Text(c, shownName(ed.name)).FontWeight(600).FontSize(theme.Body).SingleLine().Shrink(0)
			where := ed.path
			if !ed.local {
				where = a.pv(cn.host.Target()) + ":" + ed.path
			}
			ui.Text(c, shownName(where)).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(1).MinWidth(0)
			ui.Spacer(c)
			if !ed.local {
				st := cn.s.Status()
				dot(c, stateColor(tok, st.State))
				if st.State != session.Connected {
					ui.Text(c, a.pv(a.statusText(m, cn.host, st, c.Now()))).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(1)
				}
			}
			reload := m.EditorReloadFromServer()
			if ed.local {
				reload = m.EditorReloadFromDisk()
			}
			if (IconButton{Icon: icons.Refresh, Label: reload, Size: Small, Disabled: busy}).Clicked(c, "reload") {
				a.reloadEditor(cn)
			}
			if (IconButton{Icon: icons.Save, Label: m.EditorSave(), Size: Small, Disabled: busy || !modified}).Clicked(c, "save") {
				a.saveEditor(cn, false)
			}
		})
		ui.Box(c).Height(1).Shrink(0).FillWidth().Background(tok.Line)
		v := editor.View(c.Key("ed"), ed.st).Grow(1).FillWidth()
		if !ed.focused {
			v.Focus()
			ed.focused = true
		}
		ui.Box(c).Height(1).Shrink(0).FillWidth().Background(tok.Line)
		ui.Row(c.Key("edstatus")).Height(28).Shrink(0).FillWidth().Padding(0, theme.Space(3)).Gap(theme.Space(2)).AlignItems(ui.Center).Background(tok.Fill).Children(func() {
			a.syntaxSelect(c, ed, m)
			ui.Spacer(c)
			switch {
			case busy:
				ui.Spinner(c)
			case ed.err != "":
				ui.Text(c, m.ErrGeneric(securityText(m, ed.err))).FontSize(theme.Caption).TextColor(tok.Danger).SingleLine().Shrink(1)
			case modified:
				ui.Text(c, m.EditorModified()).FontSize(theme.Caption).TextColor(tok.Accent).SingleLine()
			}
			ui.Text(c, humanSize(int64(ed.st.Buffer.Len()))).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
		})
	})
	title, msg := m.EditorConflictTitle(), m.EditorConflictMessage(shownName(ed.name))
	if ed.local {
		title, msg = m.EditorDiskConflictTitle(), m.EditorDiskConflictMessage(shownName(ed.name))
	}
	switch ui.AlertDialog(c.Key("edconflict"), &ed.conflictOpen, title, msg, m.Cancel(), m.EditorReload(), m.Overwrite()) {
	case 1:
		a.reloadEditor(cn)
	case 2:
		a.saveEditor(cn, true)
	}
	switch ui.AlertDialog(c.Key("edclose"), &ed.closeOpen, m.EditorUnsavedTitle(shownName(ed.name)), m.EditorUnsavedMessage(), m.Cancel(), m.DontSave(), m.EditorSave()) {
	case 1:
		a.CloseSession(cn.id)
	case 2:
		ed.closeSaved = true
		a.saveEditor(cn, false)
	}
}

// syntaxSelect chooses the syntax the editor colors its text as.
func (a *App) syntaxSelect(c *ui.Context, ed *editState, m locales.Messages) {
	plain := m.PlainText()
	opts := []string{plain}
	for _, l := range editor.Languages() {
		opts = append(opts, l.Name)
	}
	choice := ed.lang
	if choice == "" {
		choice = plain
	}
	ui.Select(c.Key("syntax"), &choice, opts).Label(m.EditorSyntax()).Tooltip(m.EditorSyntax()).FontSize(theme.Caption)
	if choice == plain {
		choice = ""
	}
	if choice != ed.lang {
		ed.lang = choice
		ed.st.Buffer.SetLanguage(editor.LanguageNamed(choice))
	}
}

// toast shows a toast at the next frame, from the UI thread.
func (a *App) toast(t func(m locales.Messages) ui.Toast) {
	a.toasts = append(a.toasts, t)
	a.invalidate()
}
