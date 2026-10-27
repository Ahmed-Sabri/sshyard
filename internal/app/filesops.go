package app

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/sftpsvc"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

var errNotConnected = errors.New("not connected")

// askKind is what a nameAsk asks for.
type askKind int

const (
	askMkdir askKind = iota
	askNewFile
	askRename
	askChmod
)

// nameAsk is the dialog of a files pane asking a name (new folder or
// file, rename) or permissions, on one side.
type nameAsk struct {
	kind   askKind
	remote bool
	// path is the entry renamed or whose permissions change
	path  string
	value string
	err   string
}

// deleteAsk confirms deleting an entry of one side.
type deleteAsk struct {
	remote bool
	path   string
	isDir  bool
}

// resolution is what to do with a file a transfer would copy over.
type resolution int

const (
	resOverwrite resolution = iota
	resSkip
	resRename
)

// conflictAsk is a transfer planned, going through the files already at
// their destination, asking about each (or once, for all).
type conflictAsk struct {
	dir    sftpsvc.Direction
	items  []sftpsvc.Item
	choice []resolution
	at     int // the item asked about
	all    bool
	last   resolution
}

// left is how many files already there are not yet answered for.
func (k *conflictAsk) left() int {
	n := 0
	for _, it := range k.items[k.at:] {
		if !it.IsDir && it.Exists {
			n++
		}
	}
	return n
}

// rowMenu is the context menu of an entry of one side.
func (a *App) rowMenu(c *ui.Context, mn *ui.Menu, cn *conn, remote bool, row int, e sftpsvc.Entry) {
	m := locales.In(c.Locale())
	fs := cn.fs
	side := &fs.local.sel
	dir := fs.local.dir
	if remote {
		side, dir = &fs.remote.sel, fs.remote.dir
	}
	*side = row
	p := sidePath(remote, dir, e.Name)
	off := fs.remote.client == nil
	if e.IsDir && mn.Item(m.OpenFolder()).Chosen() {
		if remote {
			a.listRemote(&fs.remote, p)
		} else {
			a.listLocal(&fs.local, p)
		}
	}
	if remote {
		if !e.IsDir {
			if mn.Item(m.EditInEditor()).Disabled(off).Chosen() {
				a.openRemote(cn, p, e.Size)
			}
			if mn.Item(m.OpenWithDefault()).Disabled(off).Chosen() {
				a.openLocalProgram(cn, p, e.Size)
			}
		}
		if mn.Item(m.Download()).Disabled(off).Chosen() {
			a.Download(cn, []string{p}, fs.local.dir)
		}
	} else if mn.Item(m.Upload()).Disabled(off).Chosen() {
		a.Upload(cn, []string{p}, fs.remote.dir)
	}
	mn.Separator()
	if mn.Item(m.RenameItem()).Chosen() {
		fs.ask, fs.askOpen = &nameAsk{kind: askRename, remote: remote, path: p, value: e.Name}, true
	}
	if remote && mn.Item(m.Permissions()).Chosen() {
		fs.ask, fs.askOpen = &nameAsk{kind: askChmod, remote: true, path: p, value: strconv.FormatUint(uint64(e.Mode.Perm()), 8)}, true
	}
	if mn.Item(m.CopyPath()).Chosen() {
		c.WriteClipboard(p)
	}
	mn.Separator()
	if mn.Item(m.DeleteItem()).Chosen() {
		fs.del, fs.delOpen = &deleteAsk{remote: remote, path: p, isDir: e.IsDir}, true
	}
	mn.Separator()
	a.folderMenu(c, mn, cn, remote)
}

// folderMenu is the context menu of a side's folder: what makes
// something in it.
func (a *App) folderMenu(c *ui.Context, mn *ui.Menu, cn *conn, remote bool) {
	m := locales.In(c.Locale())
	fs := cn.fs
	off := remote && fs.remote.client == nil
	if mn.Item(m.NewFolderItem()).Disabled(off).Chosen() {
		fs.ask, fs.askOpen = &nameAsk{kind: askMkdir, remote: remote}, true
	}
	if mn.Item(m.NewFileItem()).Disabled(off).Chosen() {
		fs.ask, fs.askOpen = &nameAsk{kind: askNewFile, remote: remote}, true
	}
	if mn.Item(m.Refresh()).Disabled(off).Chosen() {
		if remote {
			a.listRemote(&fs.remote, fs.remote.dir)
		} else {
			a.listLocal(&fs.local, fs.local.dir)
		}
	}
}

// doAsk does what the name dialog asks, on its side.
func (a *App) doAsk(cn *conn, ask *nameAsk) error {
	fs := cn.fs
	if ask.kind == askMkdir {
		return a.Mkdir(cn, ask.remote, ask.value)
	}
	c := fs.remote.client
	if ask.remote && c == nil {
		return errNotConnected
	}
	var err error
	switch ask.kind {
	case askNewFile:
		if ask.remote {
			_, err = c.CreateFile(fs.remote.dir, ask.value)
		} else {
			_, err = sftpsvc.CreateLocalFile(fs.local.dir, ask.value)
		}
	case askRename:
		if ask.remote {
			_, err = c.Rename(ask.path, ask.value)
		} else {
			_, err = sftpsvc.RenameLocal(ask.path, ask.value)
		}
	case askChmod:
		mode, perr := sftpsvc.ParseMode(ask.value)
		if err = perr; err == nil {
			err = c.Chmod(ask.path, mode)
		}
	}
	if err != nil {
		return err
	}
	if ask.remote {
		a.listRemote(&fs.remote, fs.remote.dir)
	} else {
		a.listLocal(&fs.local, fs.local.dir)
	}
	return nil
}

func (a *App) askView(c *ui.Context, cn *conn, m locales.Messages) {
	fs := cn.fs
	if fs.ask == nil {
		return
	}
	ask := fs.ask
	var title, label, button string
	ic := icons.NewFolder
	switch ask.kind {
	case askMkdir:
		title, label, button = m.LocalNewFolder(), m.FolderName(), m.Create()
		if ask.remote {
			title = m.RemoteNewFolder()
		}
	case askNewFile:
		title, label, button, ic = m.NewFileTitle(), m.FileName(), m.Create(), icons.NewFile
	case askRename:
		title, label, button, ic = m.RenameTitle(path.Base(filepath.ToSlash(ask.path))), m.NewName(), m.RenameItem(), icons.Rename
	case askChmod:
		title, label, button, ic = m.PermissionsTitle(path.Base(ask.path)), m.ModeOctal(), m.Save(), icons.Chmod
	}
	tok := currentTokens(c)
	sheet(ui.Modal(c.Key("ask"), &fs.askOpen, func() {
		ui.Column(c).Gap(14).Width(360).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				softTile(c, ic, tok.Accent, 30)
				ui.Text(c, title).FontSize(theme.Title3).FontWeight(600).Shrink(1)
			})
			in := ui.TextInput(c.Key("name"), &ask.value).AutoFocus().Label(label).Placeholder(label).FillWidth()
			a.mkdirHold.apply(c, ask, in)
			if ask.kind == askChmod {
				ui.Text(c, m.ModeHint()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			}
			if ask.err != "" {
				ui.Text(c, m.ErrGeneric(securityText(m, ask.err))).TextColor(tok.Danger).FontSize(theme.Footnote)
			}
			submit := in.Submitted()
			ui.Row(c).Gap(8).Margin(4, 0, 0, 0).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					fs.askOpen = false
				}
				if pill(c, button, true, false, false) {
					submit = true
				}
			})
			if submit {
				if err := a.doAsk(cn, ask); err != nil {
					ask.err = err.Error()
				} else {
					fs.askOpen = false
				}
			}
		})
	}), tok)
	if !fs.askOpen {
		fs.ask = nil
	}
}

func (a *App) deleteView(c *ui.Context, cn *conn, m locales.Messages) {
	fs := cn.fs
	if fs.del == nil {
		return
	}
	del := fs.del
	name := filepath.Base(del.path)
	if del.remote {
		name = path.Base(del.path)
	}
	msg := m.DeleteFileConfirm(name)
	if del.isDir {
		msg = m.DeleteFolderConfirm(name)
	}
	if ui.AlertDialog(c.Key("del"), &fs.delOpen, m.DeleteItem(), msg, m.Cancel(), m.Delete()) == 1 {
		client := fs.remote.client
		go func() {
			var err error
			if del.remote {
				if client == nil {
					err = errNotConnected
				} else {
					err = client.Remove(del.path)
				}
			} else {
				err = sftpsvc.RemoveLocal(del.path)
			}
			a.post(func() {
				// Success clears a previous failure; a failure stays
				// through the refresh below.
				a.filesErr(cn, del.remote, err)
				a.refreshSides(cn)
			})
		}()
	}
	if !fs.delOpen {
		fs.del = nil
	}
}

func (a *App) conflictView(c *ui.Context, cn *conn, m locales.Messages) {
	fs := cn.fs
	ask := fs.conflict
	if ask == nil {
		return
	}
	if ask.at >= len(ask.items) {
		fs.conflict = nil
		return
	}
	it := ask.items[ask.at]
	tok := currentTokens(c)
	answer := func(r resolution) {
		ask.choice[ask.at], ask.last = r, r
		ask.at++
		a.resolveConflicts(cn, ask)
	}
	sheet(ui.Modal(c.Key("conflict"), &fs.conflictOpen, func() {
		ui.Column(c).Gap(14).Width(420).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				softTile(c, icons.Warning, tok.Warning, 30)
				ui.Text(c, m.ConflictTitle()).FontSize(theme.Title3).FontWeight(600)
			})
			where := path.Dir(it.Dst)
			if ask.dir == sftpsvc.Download {
				where = filepath.Dir(it.Dst)
			}
			ui.Text(c, m.ConflictMessage(it.Rel, where)).Wrap()
			if n := ask.left(); n > 1 {
				ui.Checkbox(c.Key("all"), &ask.all, m.ApplyToAll(strconv.Itoa(n-1)))
			}
			ui.Row(c).Gap(8).Margin(4, 0, 0, 0).Children(func() {
				if pill(c, m.Cancel(), false, false, false) {
					fs.conflictOpen = false
				}
				ui.Spacer(c)
				if pill(c, m.Skip(), false, false, false) {
					answer(resSkip)
				}
				if pill(c, m.KeepBoth(), false, false, false) {
					answer(resRename)
				}
				if pill(c, m.Overwrite(), true, false, false) {
					answer(resOverwrite)
				}
			})
		})
	}), tok)
	if !fs.conflictOpen {
		fs.conflict = nil // canceled: nothing of the plan is queued
		if ask.dir == sftpsvc.Download {
			fs.skipped = nil // nor is what it left out of interest
		}
	}
}

// transferLabel is what a transfer's row says of its state.
func transferLabel(m locales.Messages, t sftpsvc.Transfer) string {
	switch t.State {
	case sftpsvc.Queued:
		if t.Attempt > 0 {
			return m.TransferRetrying(strconv.Itoa(t.Attempt), strconv.Itoa(sftpsvc.MaxRetries))
		}
		return m.TransferQueued()
	case sftpsvc.Running:
		if t.Attempt > 0 && t.Err != nil {
			return m.TransferRetrying(strconv.Itoa(t.Attempt), strconv.Itoa(sftpsvc.MaxRetries))
		}
		s := strconv.Itoa(t.Percent()) + "%"
		if t.Speed > 0 {
			s += " · " + humanSize(int64(t.Speed)) + "/s"
		}
		if eta := t.ETA(); eta > 0 {
			s += " · " + m.TransferETA(shortDuration(eta))
		}
		return s
	case sftpsvc.Paused:
		return m.TransferPaused(strconv.Itoa(t.Percent()))
	case sftpsvc.Done:
		return m.TransferDoneShort()
	case sftpsvc.Failed:
		return m.TransferFailed(securityErrorText(m, t.Err))
	case sftpsvc.Canceled:
		return m.TransferCanceled()
	}
	return ""
}

func shortDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Second {
		d = time.Second
	}
	h, mn, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return strconv.Itoa(h) + ":" + pad2(mn) + ":" + pad2(s)
	}
	return strconv.Itoa(mn) + ":" + pad2(s)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// transferCounts is how many of ts are in each state, and how fast the
// running ones go together.
type transferCounts struct {
	active, running, paused, failed, done int
	speed                                 float64
}

func countTransfers(ts []sftpsvc.Transfer) transferCounts {
	var n transferCounts
	for _, t := range ts {
		switch t.State {
		case sftpsvc.Running:
			n.running++
			n.speed += t.Speed
		case sftpsvc.Paused:
			n.paused++
		case sftpsvc.Failed, sftpsvc.Canceled:
			n.failed++
		case sftpsvc.Done:
			n.done++
		}
		if t.Active() {
			n.active++
		}
	}
	return n
}

// transfersButton is the Files pane header's button for its transfer
// queue, with how many are still to go on it.
func (a *App) transfersButton(c *ui.Context, cn *conn, m locales.Messages) {
	n := countTransfers(cn.fs.transfers.List())
	b := IconButton{Icon: icons.Transfers, Label: m.Transfers(), Active: cn.fs.queueOpen, Size: Tiny}
	if n.active > 0 {
		b.Badge = strconv.Itoa(n.active)
	} else if n.failed > 0 {
		b.Badge, b.BadgeDanger = strconv.Itoa(n.failed), true
	}
	if b.Clicked(c, "transfers") {
		cn.fs.queueOpen = true
	}
	if n.running > 0 {
		c.After(250 * time.Millisecond)
	}
}

// transfersSheet is the transfer queue of a files pane: what is going
// on, buttons for all, and a row for each transfer.
func (a *App) transfersSheet(c *ui.Context, cn *conn, tok theme.Tokens, m locales.Messages) {
	fs := cn.fs
	if !fs.queueOpen {
		return
	}
	trs := fs.transfers
	ts := trs.List()
	n := countTransfers(ts)
	active, paused, failed := n.active, n.paused, n.failed
	sheet(ui.Modal(c, &fs.queueOpen, func() {
		ui.Column(c.Key("transfers")).Gap(14).Width(720).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).FillWidth().Children(func() {
				tile(c, icons.Transfers, theme.Brand, 36)
				ui.Column(c).Gap(2).Grow(1).Shrink(1).Children(func() {
					sheetTitle(c, m.Transfers())
					summary := m.TransferSummary(strconv.Itoa(active), strconv.Itoa(n.done), strconv.Itoa(len(ts)))
					if n.speed > 0 {
						summary += " · " + humanSize(int64(n.speed)) + "/s"
					}
					if len(ts) > 0 {
						ui.Text(c, summary).FontSize(theme.Footnote).TextColor(tok.TextMuted).SingleLine()
					}
				})
				if (IconButton{Icon: icons.Pause, Label: m.PauseAll(), Disabled: active == paused, Size: Small}).Clicked(c, "pauseall") {
					trs.PauseAll()
				}
				if (IconButton{Icon: icons.Resume, Label: m.ResumeAll(), Disabled: paused == 0, Size: Small}).Clicked(c, "resumeall") {
					trs.ResumeAll()
				}
				if (IconButton{Icon: icons.Reconnect, Label: m.RetryAll(), Disabled: failed == 0, Size: Small}).Clicked(c, "retryall") {
					for _, t := range ts {
						if t.State == sftpsvc.Failed || t.State == sftpsvc.Canceled {
							trs.Retry(t.ID)
						}
					}
				}
				if (IconButton{Icon: icons.Close, Label: m.CancelAll(), Disabled: active == 0, Size: Small}).Clicked(c, "cancelall") {
					trs.CancelAll()
				}
				if (IconButton{Icon: icons.Clear, Label: m.ClearFinished(), Disabled: len(ts) == active, Size: Small}).Clicked(c, "clear") {
					trs.ClearFinished()
				}
			})
			box := ui.Column(c).Height(320).FillWidth().Padding(6).Radius(theme.RadiusCard - 2).Background(tok.Fill)
			box.Children(func() {
				if len(ts) == 0 {
					ui.Column(c).Grow(1).Center().Children(func() {
						ui.Text(c, m.NoTransfers()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
					})
					return
				}
				ui.List(c.Key("queue"), &fs.queue, len(ts), func(i int) {
					t := ts[i]
					ui.Row(c.Key("tr:"+strconv.Itoa(t.ID))).Gap(8).AlignItems(ui.Center).Height(26).Padding(0, 2).FillWidth().Children(func() {
						ic := icons.Upload
						if t.Dir == sftpsvc.Download {
							ic = icons.Download
						}
						ui.Icon(c, ic.SVG).Width(14).Height(14).Shrink(0).TextColor(tok.TextMuted)
						name := ui.Text(c, t.Name).FontSize(theme.Footnote).SingleLine().Width(200)
						if t.Destination != "" {
							name.Tooltip(t.Destination)
						}
						ui.Progress(c, float64(t.Percent())/100).Grow(1)
						col := tok.TextMuted
						if t.State == sftpsvc.Failed {
							col = tok.Danger
						}
						lbl := ui.Text(c, transferLabel(m, t)).FontSize(theme.Caption).TextColor(col).SingleLine().Width(190)
						if t.Err != nil {
							lbl.Tooltip(securityErrorText(m, t.Err))
						}
						id := t.ID
						ui.Row(c).Gap(2).Width(52).Justify(ui.End).Children(func() {
							switch t.State {
							case sftpsvc.Queued, sftpsvc.Running:
								if (IconButton{Icon: icons.Pause, Label: m.PauseTransfer(t.Name), Size: Small}).Clicked(c, "pause") {
									trs.Pause(id)
								}
							case sftpsvc.Paused:
								if (IconButton{Icon: icons.Resume, Label: m.ResumeTransfer(t.Name), Size: Small}).Clicked(c, "resume") {
									trs.Resume(id)
								}
							case sftpsvc.Failed, sftpsvc.Canceled:
								if (IconButton{Icon: icons.Reconnect, Label: m.RetryTransfer(t.Name), Size: Small}).Clicked(c, "retry") {
									trs.Retry(id)
								}
							}
							if t.Active() {
								if (IconButton{Icon: icons.Close, Label: m.CancelTransfer(t.Name), Size: Small}).Clicked(c, "cancel") {
									trs.Cancel(id)
								}
							}
						})
					})
				}).Grow(1).FillWidth()
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Close(), true, false, false) {
					fs.queueOpen = false
				}
			})
		})
	}), tok)
}

// skippedShown is how many skipped items the notice names.
const skippedShown = 8

// planningView shows that a transfer is being planned (folders listed
// to the bottom, which a slow or silent server can make long), with a
// way to cancel it.
func (a *App) planningView(c *ui.Context, cn *conn, tok theme.Tokens, m locales.Messages) {
	run := cn.fs.planning
	if run == nil {
		return
	}
	col, icon, text := tok.Accent, icons.Download, m.PlanningDownload()
	if run.dir == sftpsvc.Upload {
		icon, text = icons.Upload, m.PlanningUpload()
	}
	ui.Row(c.Key("planning")).FillWidth().Padding(6, 8, 6, 12).Gap(8).AlignItems(ui.Center).
		Radius(theme.RadiusControl).Background(col.Alpha(0.12)).Border(1, col.Alpha(0.22)).Children(func() {
		ui.Icon(c, icon.SVG).Size(14, 14).Shrink(0).TextColor(col)
		ui.Text(c, text).FontSize(theme.Footnote).Grow(1).Shrink(1)
		if pill(c, m.CancelPlanning(), false, false, false) {
			run.cancel()
			cn.fs.planning = nil
			if run.dir == sftpsvc.Download {
				cn.fs.skipped = nil // as when its question is canceled
			}
		}
	})
}

// unfinishedView is the notice of a save in the remote folder that a cut
// left undone: the file is missing and its last version at a backup. It
// offers the first such file to restore or discard (the next shows once
// that is done); nothing is put back unless asked.
func (a *App) unfinishedView(c *ui.Context, cn *conn, tok theme.Tokens, m locales.Messages) {
	s := &cn.fs.remote
	if len(s.unfinished) == 0 || s.client == nil {
		return
	}
	u := s.unfinished[0]
	col := tok.Warning
	ui.Row(c.Key("unfinished")).FillWidth().Padding(6, 8, 6, 12).Gap(8).AlignItems(ui.Center).
		Radius(theme.RadiusControl).Background(col.Alpha(0.12)).Border(1, col.Alpha(0.22)).Children(func() {
		ui.Icon(c, icons.Warning.SVG).Size(14, 14).Shrink(0).TextColor(col)
		ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
			ui.Text(c, m.UnfinishedSave(shownName(u.Name))).FontSize(theme.Footnote).Selectable()
			if n := len(s.unfinished) - 1; n > 0 {
				ui.Text(c, m.SkippedMore(strconv.Itoa(n))).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			}
		})
		restore := pill(c, m.RestoreUnfinished(), true, false, s.fixing)
		discard := pill(c, m.DiscardUnfinished(), false, false, s.fixing)
		if (restore || discard) && !s.fixing {
			a.fixUnfinished(cn, u, restore)
		}
	})
}

// fixUnfinished restores u, or discards its backup, then lists the
// folder again.
func (a *App) fixUnfinished(cn *conn, u sftpsvc.Unfinished, restore bool) {
	s := &cn.fs.remote
	c, dir := s.client, s.dir
	s.fixing = true
	go func() {
		var err error
		if restore {
			err = c.Restore(dir, u)
		} else {
			err = c.Discard(dir, u)
		}
		a.post(func() {
			s.fixing = false
			if s.client != c || s.dir != dir {
				return
			}
			if err != nil {
				a.filesErr(cn, true, err)
				return
			}
			a.listRemote(s, dir)
		})
	}()
}

// skippedView is the notice of what the last download left out: each
// with why, the first skippedShown of them, and how many more.
func (a *App) skippedView(c *ui.Context, cn *conn, tok theme.Tokens, m locales.Messages) {
	sk := cn.fs.skipped
	if len(sk) == 0 {
		return
	}
	col := tok.Warning
	ui.Row(c.Key("skipped")).FillWidth().Padding(6, 8, 6, 12).Gap(8).AlignItems(ui.Start).
		Radius(theme.RadiusControl).Background(col.Alpha(0.12)).Border(1, col.Alpha(0.22)).Children(func() {
		ui.Icon(c, icons.Warning.SVG).Size(14, 14).Shrink(0).TextColor(col).Margin(3, 0, 0, 0)
		ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
			ui.Text(c, m.DownloadSkipped(strconv.Itoa(len(sk)))).FontSize(theme.Footnote)
			for i, s := range sk {
				if i == skippedShown {
					ui.Text(c, m.SkippedMore(strconv.Itoa(len(sk)-i))).FontSize(theme.Footnote).TextColor(tok.TextMuted)
					break
				}
				ui.Text(c, skipText(m, s)).FontSize(theme.Footnote).TextColor(tok.TextMuted).Selectable()
			}
		})
		if (IconButton{Icon: icons.Close, Label: m.DismissSkipped(), Size: Small}).Clicked(c, "dismissskipped") {
			cn.fs.skipped = nil
		}
	})
}

// skipText says what was skipped and why, the name shown as shownName.
func skipText(m locales.Messages, s sftpsvc.Skip) string {
	name := shownName(s.Rel)
	switch s.Reason {
	case sftpsvc.SkipLinkedFolder:
		return m.SkipLinkedFolder(name)
	case sftpsvc.SkipBrokenLink:
		return m.SkipBrokenLink(name)
	}
	return m.SkipName(name)
}

// shownName is a name from the server as text that shows all of it on
// one line, and nothing but it: newlines, other control characters and
// those that reorder text (bidi controls) are written as escapes (\n,
// \u202e), as are bytes that are not UTF-8 (\xff).
func shownName(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case r == utf8.RuneError:
			if _, n := utf8.DecodeRuneInString(s[i:]); n == 1 {
				fmt.Fprintf(&b, `\x%02x`, s[i])
			} else {
				b.WriteRune(r)
			}
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r <= 0xff && unicode.IsControl(r):
			fmt.Fprintf(&b, `\x%02x`, r)
		case unicode.Is(unicode.Bidi_Control, r) || r == '\u2028' || r == '\u2029':
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
