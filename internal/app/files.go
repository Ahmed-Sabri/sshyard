package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sftpsvc"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// remoteSide is the remote half of a files pane, on its SFTP client.
type remoteSide struct {
	client   *sftpsvc.Client
	connects int // the connection the client is on
	opening  bool
	err      string
	// listErr says err came from listing this side. A list that works
	// clears only that, so a plan or transfer error survives the refresh
	// a finished transfer triggers in the same frame.
	// shortcut: it also stays across a later folder change, until the next plan, delete, or reconnect.
	listErr bool
	dir     string
	entries []sftpsvc.Entry
	sel     int
	list    ui.ListState
	// unfinished are saves in dir a cut left undone: the file missing,
	// its old version at a backup, to restore or discard.
	unfinished []sftpsvc.Unfinished
	// fixing is a restore or discard under way.
	fixing bool
}

// localSide is the local half of a files pane.
type localSide struct {
	dir     string
	entries []sftpsvc.Entry
	sel     int
	list    ui.ListState
	err     string
	// listErr says err came from listing this side. A list that works
	// clears only that, so a plan or transfer error survives the refresh
	// a finished transfer triggers in the same frame.
	// shortcut: it also stays across a later folder change, until the next plan, delete, or reconnect.
	listErr bool
	loaded  bool
}

// filesState is what a files pane keeps: both sides, its transfers and
// its dialogs. Each files pane has its own.
type filesState struct {
	remote    remoteSide
	local     localSide
	transfers *sftpsvc.Transfers
	// ask is the open name or mode dialog (new folder or file, rename,
	// permissions), del the delete confirmation, conflict the same-name
	// dialog of a transfer being planned.
	ask          *nameAsk
	askOpen      bool
	del          *deleteAsk
	delOpen      bool
	conflict     *conflictAsk
	conflictOpen bool
	// open confirms opening a large or binary file
	open     *openAsk
	openOpen bool
	// upConflict is a copy opened in another app whose upload was
	// refused: the server's file changed.
	upConflict     *localConflict
	upConflictOpen bool
	// edits are the files opened in other apps
	edits localEdits
	// skipped is what the last download planned left out, shown until
	// dismissed or the next download (planned, failing to be, or its
	// question about files already there canceled).
	skipped []sftpsvc.Skip
	// planning is the transfer being planned, until it is ready, fails or
	// is canceled (a newer one cancels it too).
	planning *planRun
	// noFollow is set when the pane stopped following the terminal, and
	// followed is the directory it last went to for it
	noFollow bool
	followed string
	// noReportHidden hides the line saying the terminal followed does not
	// report its directory, snippetOpen is its sheet with the snippet.
	noReportHidden bool
	snippetOpen    bool
	// split is the local side's share of the room, in percent.
	split float32
	// queueOpen is the transfers sheet, queue its list.
	queueOpen bool
	queue     ui.ListState
	// opening is set, per side (local, remote), from a double click on a
	// row's name, which opens on the press, to its release, whose click
	// is not a choice in the folder just opened.
	opening [2]bool
	// done is how many transfers were done when the sides were last
	// listed again for them, at refreshed.
	done      int
	refreshed time.Time
	// pub is the SFTP client for the transfers, which run off the UI
	// thread.
	pub struct {
		sync.Mutex
		client   *sftpsvc.Client
		connects int
	}
}

func newFilesState(onChange func()) *filesState {
	fs := &filesState{transfers: &sftpsvc.Transfers{OnChange: onChange}}
	fs.remote.sel, fs.local.sel = -1, -1
	fs.remote.list.Selected = &fs.remote.sel
	fs.local.list.Selected = &fs.local.sel
	return fs
}

func (fs *filesState) close() {
	fs.transfers.CancelAll()
	fs.edits.close()
	fs.pub.Lock()
	fs.pub.client = nil
	fs.pub.Unlock()
	if fs.remote.client != nil {
		fs.remote.client.Close()
	}
}

// publish makes c the client transfers use.
func (fs *filesState) publish(c *sftpsvc.Client, connects int) {
	fs.pub.Lock()
	fs.pub.client, fs.pub.connects = c, connects
	fs.pub.Unlock()
}

// transferClient is a files pane's Transfers.Client: the pane's SFTP
// client once it is open on the connection there is now, waiting while
// the pane connects or reconnects.
func (a *App) transferClient(cn *conn) func(context.Context) (*sftpsvc.Client, error) {
	return func(ctx context.Context) (*sftpsvc.Client, error) {
		for {
			st := cn.s.Status()
			switch st.State {
			case session.Failed, session.Disconnected, session.Closed, session.Exited:
				if st.Err != nil {
					return nil, st.Err
				}
				return nil, errors.New("sftp: " + st.State.String())
			}
			cn.fs.pub.Lock()
			c, n := cn.fs.pub.client, cn.fs.pub.connects
			cn.fs.pub.Unlock()
			if c != nil && st.State == session.Connected && n == st.Connects {
				return c, nil
			}
			if st.State == session.Connected {
				// the pane may not be showing, to open SFTP itself
				a.post(func() { a.ensureSFTP(cn) })
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
}

// ensureSFTP opens SFTP on the pane's connection, again after it
// reconnected.
func (a *App) ensureSFTP(cn *conn) {
	s := &cn.fs.remote
	st := cn.s.Status()
	if st.State != session.Connected || s.opening || (s.client != nil && s.connects == st.Connects) {
		return
	}
	if s.client != nil {
		cn.fs.publish(nil, 0)
		s.client.Close()
		s.client = nil
	}
	client := cn.s.Client()
	if client == nil {
		return
	}
	s.opening, s.err, s.listErr = true, "", false
	connects := st.Connects
	dir := s.dir
	go func() {
		c, err := sftpsvc.New(client)
		var home string
		var es []sftpsvc.Entry
		var un []sftpsvc.Unfinished
		if err == nil {
			home, err = c.Home()
		}
		if err == nil {
			if dir == "" {
				dir = home
			}
			es, un, err = c.ListUnfinished(dir)
		}
		a.post(func() {
			s.opening = false
			if err != nil {
				s.err, s.listErr = err.Error(), true
				if c != nil {
					c.Close()
				}
				s.connects = connects
				return
			}
			s.client, s.connects, s.dir, s.entries, s.sel, s.unfinished = c, connects, dir, es, -1, un
			cn.fs.publish(c, connects)
		})
	}()
}

func (a *App) listRemote(s *remoteSide, dir string) {
	c := s.client
	if c == nil {
		return
	}
	go func() {
		es, un, err := c.ListUnfinished(dir)
		a.post(func() {
			if err != nil {
				s.err, s.listErr = err.Error(), true
				return
			}
			if s.listErr {
				s.err, s.listErr = "", false
			}
			s.dir, s.entries, s.sel, s.unfinished = dir, es, -1, un
		})
	}()
}

func (a *App) listLocal(l *localSide, dir string) {
	if dir == "" {
		dir = a.p.HomeDir
		if dir == "" {
			dir, _ = os.UserHomeDir()
		}
	}
	es, err := sftpsvc.ListLocal(dir)
	l.loaded = true
	if err != nil {
		l.err, l.listErr = err.Error(), true
		return
	}
	if l.listErr {
		l.err, l.listErr = "", false
	}
	l.dir, l.entries, l.sel = dir, es, -1
}

// Upload copies local files and folders into the remote folder dir,
// asking about those already there.
func (a *App) Upload(cn *conn, paths []string, dir string) {
	a.plan(cn, sftpsvc.Upload, paths, dir)
}

// Download copies remote files and folders into the local folder dir,
// asking about those already there.
func (a *App) Download(cn *conn, paths []string, dir string) {
	a.plan(cn, sftpsvc.Download, paths, dir)
}

func (a *App) plan(cn *conn, d sftpsvc.Direction, paths []string, dir string) {
	c := cn.fs.remote.client
	if c == nil || len(paths) == 0 || dir == "" {
		return
	}
	if cn.fs.planning != nil {
		cn.fs.planning.cancel()
	}
	// The previous failure is done with; this plan's own failure, if it
	// has one, is set when it finishes and must survive the refresh.
	a.filesErr(cn, d == sftpsvc.Upload, nil)
	ctx, cancel := context.WithCancel(context.Background())
	run := &planRun{dir: d, cancel: cancel}
	cn.fs.planning = run
	go func() {
		defer cancel()
		var items []sftpsvc.Item
		var skips []sftpsvc.Skip
		var err error
		if d == sftpsvc.Upload {
			items, err = c.PlanUpload(ctx, paths, dir)
		} else {
			items, skips, err = c.PlanDownload(ctx, paths, dir)
		}
		a.post(func() {
			if cn.fs.planning != run {
				return // canceled, or a newer plan took its place
			}
			cn.fs.planning = nil
			if err != nil {
				if d == sftpsvc.Download {
					cn.fs.skipped = nil // the last download's, not this one's
				}
				a.filesErr(cn, d == sftpsvc.Upload, err)
				return
			}
			if d == sftpsvc.Download {
				cn.fs.skipped = skips
			}
			ask := &conflictAsk{dir: d, items: items, choice: make([]resolution, len(items))}
			a.resolveConflicts(cn, ask)
		})
	}()
}

// planRun is a transfer being planned.
type planRun struct {
	dir    sftpsvc.Direction
	cancel context.CancelFunc
}

// filesErr shows err on the side a step was on. A nil err clears that
// side. The error is not a listing error, so a refresh leaves it.
func (a *App) filesErr(cn *conn, remote bool, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if remote {
		cn.fs.remote.err, cn.fs.remote.listErr = msg, false
	} else {
		cn.fs.local.err, cn.fs.local.listErr = msg, false
	}
	a.invalidate()
}

// resolveConflicts goes on through ask's items from ask.at: it asks about
// the next one already there, or, with none left, queues them.
func (a *App) resolveConflicts(cn *conn, ask *conflictAsk) {
	for ; ask.at < len(ask.items); ask.at++ {
		it := ask.items[ask.at]
		if it.IsDir || !it.Exists {
			continue
		}
		if ask.all {
			ask.choice[ask.at] = ask.last
			continue
		}
		cn.fs.conflict, cn.fs.conflictOpen = ask, true
		return
	}
	cn.fs.conflict, cn.fs.conflictOpen = nil, false
	a.enqueue(cn, ask)
}

// enqueue makes the folders of a plan resolved and queues its files.
func (a *App) enqueue(cn *conn, ask *conflictAsk) {
	ts := cn.fs.transfers
	get := ts.Client
	up := ask.dir == sftpsvc.Upload
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		c, err := get(ctx)
		if err != nil {
			a.post(func() { a.filesErr(cn, up, err) })
			return
		}
		chosen := map[string]bool{}
		taken := func(p string) bool {
			if chosen[p] {
				return true
			}
			if up {
				return c.Exists(p)
			}
			return sftpsvc.LocalExists(p)
		}
		for i, it := range ask.items {
			switch {
			case it.IsDir && up:
				err = c.MkdirAll(it.Dst)
			case it.IsDir:
				err = os.MkdirAll(it.Dst, 0o755)
			case ask.choice[i] == resSkip:
			default:
				dst := it.Dst
				if ask.choice[i] == resRename {
					dst = sftpsvc.UniqueName(dst, up, taken)
				}
				chosen[dst] = true
				ts.Add(ask.dir, it.Src, dst, it.Rel)
			}
			if err != nil {
				a.post(func() { a.filesErr(cn, up, err) })
				return
			}
		}
		a.post(func() { a.refreshSides(cn) })
	}()
}

// refreshSides lists both sides of a files pane again.
func (a *App) refreshSides(cn *conn) {
	fs := cn.fs
	a.listLocal(&fs.local, fs.local.dir)
	if fs.remote.client != nil {
		a.listRemote(&fs.remote, fs.remote.dir)
	}
}

// Mkdir makes a folder on one side of a files pane and lists it again.
func (a *App) Mkdir(cn *conn, remote bool, name string) error {
	fs := cn.fs
	if !remote {
		if _, err := sftpsvc.MkdirLocal(fs.local.dir, name); err != nil {
			return err
		}
		a.listLocal(&fs.local, fs.local.dir)
		return nil
	}
	s := &fs.remote
	if s.client == nil {
		return session.ErrNoCredentials
	}
	if _, err := s.client.Mkdir(s.dir, name); err != nil {
		return err
	}
	a.listRemote(s, s.dir)
	return nil
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	f, i := float64(n), 0
	for f >= unit && i < 4 {
		f /= unit
		i++
	}
	return strconv.FormatFloat(f, 'f', 1, 64) + " " + "BKMGT"[i:i+1] + "B"
}

// filesDrag is rows dragged from one side of a files pane to the other
// side, of the same files pane or another one.
type filesDrag struct {
	pane   int
	remote bool
	paths  []string
}

// sidePath is the path of the entry in the folder dir of a side.
func sidePath(remote bool, dir, name string) string {
	if remote {
		return sftpsvc.RemoteJoin(dir, name)
	}
	return filepath.Join(dir, name)
}

// fileTable is one side's entries. Each row drags to the other side and
// has a menu of what can be done to it (rowMenu). opened reports a double
// click on a row's name, which opens the chosen row as Submitted does.
func (a *App) fileTable(c *ui.Context, cn *conn, remote bool, m locales.Messages, list *ui.ListState, dir string, es []sftpsvc.Entry, tok theme.Tokens, narrow bool) (tb ui.Element, opened bool) {
	cols := []ui.TableColumn{{Title: m.ColName()}, {Title: m.ColSize(), Width: 80, Align: ui.End}}
	if !narrow {
		cols = append(cols, ui.TableColumn{Title: m.ColModified(), Width: 130})
	}
	key := "ltable"
	if remote {
		key = "rtable"
	}
	tb = ui.Table(c.Key(key), list, cols, len(es), func(row, col int) {
		e := es[row]
		muted := tok.TextMuted
		if list.Selected != nil && *list.Selected == row {
			muted = tok.AccentText // readable on the selection
		}
		var cell ui.Element
		switch col {
		case 0:
			cell = ui.Row(c).Gap(6).AlignItems(ui.Center).Grow(1).Children(func() {
				ic := icons.File
				if e.IsDir {
					ic = icons.Folder
				}
				col := muted
				if e.IsDir && muted == tok.TextMuted {
					col = tok.Accent
				}
				ui.Icon(c, ic.SVG).Width(15).Height(15).Shrink(0).TextColor(col)
				ui.Text(c, e.Name).SingleLine()
			})
			cell.Drag(filesDrag{pane: cn.id, remote: remote, paths: []string{sidePath(remote, dir, e.Name)}})
			// The name takes the press to drag the row, so it chooses
			// and opens the row itself, as the rest of the row does.
			side := 0
			if remote {
				side = 1
			}
			dbl, clk := cell.DoubleClicked(), cell.Clicked()
			if dbl && list.Selected != nil {
				*list.Selected = row
				opened, cn.fs.opening[side] = true, !clk // released yet?
			} else if clk && list.Selected != nil {
				if cn.fs.opening[side] {
					cn.fs.opening[side] = false
				} else {
					*list.Selected = row
					list.Focus(c)
				}
			}
		case 1:
			cell = ui.Row(c).Grow(1).Justify(ui.End).Children(func() {
				if !e.IsDir {
					ui.Text(c, humanSize(e.Size)).TextColor(muted).SingleLine()
				}
			})
		case 2:
			cell = ui.Text(c, e.ModTime.Format("2006-01-02 15:04")).TextColor(muted).SingleLine().Grow(1)
		}
		cell.ContextMenu(func(mn *ui.Menu) { a.rowMenu(c, mn, cn, remote, row, e) })
	}).Grow(1).FillWidth()
	tb.ContextMenu(func(mn *ui.Menu) { a.folderMenu(c, mn, cn, remote) })
	return tb, opened
}

// sideHeader is the title row of one side of a files pane: an icon, the
// side's name, its folder and its IconButtons.
func sideHeader(c *ui.Context, tok theme.Tokens, ic *icons.Icon, title, dir string, actions func()) {
	ui.Row(c).Gap(8).Height(32).Padding(0, 2, 0, 4).AlignItems(ui.Center).Children(func() {
		softTile(c, ic, tok.Accent, 22)
		ui.Text(c, title).FontWeight(600).FontSize(theme.Footnote).SingleLine()
		ui.Text(c, dir).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(1).Grow(1)
		actions()
	})
}

// selected is the entry chosen on a side, if any.
func selected(sel int, es []sftpsvc.Entry) (sftpsvc.Entry, bool) {
	if sel < 0 || sel >= len(es) {
		return sftpsvc.Entry{}, false
	}
	return es[sel], true
}

// filesBody is a files pane below its header: local and remote side by
// side (stacked when the pane is narrow), then the pane's transfers.
func (a *App) filesBody(c *ui.Context, t *layout.Tab, n *layout.Node, cn *conn, width float32, tok theme.Tokens, m locales.Messages) {
	fs := cn.fs
	if !fs.local.loaded {
		a.listLocal(&fs.local, fs.local.dir)
	}
	a.ensureSFTP(cn)
	a.refreshForTransfers(c, cn)
	l, s := &fs.local, &fs.remote
	stacked := width > 0 && width < 620
	narrow := width > 0 && width < 900
	body := ui.Column(c.Key("files")).Grow(1).FillWidth().Padding(10).Gap(10)
	body.Children(func() {
		a.unfinishedView(c, cn, tok, m)
		a.noReportView(c, t, cn, tok, m)
		a.planningView(c, cn, tok, m)
		a.skippedView(c, cn, tok, m)
		if fs.split <= 0 || fs.split >= 100 {
			fs.split = 50
		}
		// Each side keeps clear of the line the sides are resized by.
		gap := func(first bool) (top, right, bottom, left float32) {
			switch {
			case stacked && first:
				return 0, 0, 5, 0
			case stacked:
				return 5, 0, 0, 0
			case first:
				return 0, 5, 0, 0
			}
			return 0, 0, 0, 5
		}
		localPane := func() {
			ui.Box(c.Key("lpad")).Fill().Padding(gap(true)).Children(func() {
				// local
				local := ui.Column(c.Key("local")).Fill().Gap(4).Padding(6).Radius(theme.RadiusCard-2).Border(1, tok.Line)
				local.Children(func() {
					le, lok := selected(l.sel, l.entries)
					sideHeader(c, tok, icons.LocalDisk, m.Local(), l.dir, func() {
						if iconButton(c, "lup", icons.GoUp, m.LocalUp()) {
							a.listLocal(l, filepath.Dir(l.dir))
						}
						if iconButton(c, "lrefresh", icons.Refresh, m.LocalRefresh()) {
							a.listLocal(l, l.dir)
						}
						if iconButton(c, "lmkdir", icons.NewFolder, m.LocalNewFolder()) {
							fs.ask, fs.askOpen = &nameAsk{kind: askMkdir}, true
						}
						if (IconButton{Icon: icons.Upload, Label: m.UploadSelected(), Disabled: s.client == nil || !lok}).Clicked(c, "upload") {
							a.Upload(cn, []string{filepath.Join(l.dir, le.Name)}, s.dir)
						}
					})
					tb, opened := a.fileTable(c, cn, false, m, &l.list, l.dir, l.entries, tok, narrow)
					if e, ok := selected(l.sel, l.entries); (tb.Submitted() || opened) && ok {
						if e.IsDir {
							a.listLocal(l, filepath.Join(l.dir, e.Name))
						} else if s.client != nil {
							a.Upload(cn, []string{filepath.Join(l.dir, e.Name)}, s.dir)
						}
					}
					if l.err != "" {
						ui.Text(c, l.err).TextColor(tok.Danger).FontSize(theme.Footnote)
					}
				})
				// remote rows download here, from this pane or another files
				// pane (on its own connection, in its own queue)
				if d, ok := ui.Drop[filesDrag](local); ok && d.remote {
					if src := a.conns[d.pane]; src != nil && src.fs != nil {
						a.Download(src, d.paths, l.dir)
					}
				}
				if d, ok := ui.DragOver[filesDrag](local); ok && d.remote {
					local.Background(tok.Accent.Alpha(0.08))
				}
			})
		}
		remotePane := func() {
			ui.Box(c.Key("rpad")).Fill().Padding(gap(false)).Children(func() {
				remote := ui.Column(c.Key("remote")).Fill().Gap(4).Padding(6).Radius(theme.RadiusCard-2).Border(1, tok.Line)
				remote.Children(func() {
					re, rok := selected(s.sel, s.entries)
					sideHeader(c, tok, icons.Host, m.Remote(), s.dir, func() {
						if s.opening {
							ui.Spinner(c)
						}
						off := s.client == nil
						if (IconButton{Icon: icons.GoUp, Label: m.RemoteUp(), Disabled: off}).Clicked(c, "rup") && s.dir != "" {
							a.listRemote(s, sftpsvc.RemoteParent(s.dir))
						}
						if (IconButton{Icon: icons.Refresh, Label: m.RemoteRefresh(), Disabled: off}).Clicked(c, "rrefresh") {
							a.listRemote(s, s.dir)
						}
						if (IconButton{Icon: icons.NewFolder, Label: m.RemoteNewFolder(), Disabled: off}).Clicked(c, "rmkdir") {
							fs.ask, fs.askOpen = &nameAsk{kind: askMkdir, remote: true}, true
						}
						if (IconButton{Icon: icons.Download, Label: m.DownloadSelected(), Disabled: off || !rok}).Clicked(c, "download") {
							a.Download(cn, []string{sftpsvc.RemoteJoin(s.dir, re.Name)}, l.dir)
						}
					})
					if s.client == nil {
						ui.Column(c).Grow(1).Center().Children(func() {
							if s.opening || cn.s.Status().State != session.Connected {
								ui.Text(c, a.pv(a.statusText(m, cn.host, cn.s.Status(), c.Now()))).TextColor(tok.TextMuted)
							}
							if s.err != "" {
								ui.Text(c, a.pv(m.ErrGeneric(s.err))).TextColor(tok.Danger).FontSize(theme.Footnote)
							}
						})
						return
					}
					tb, opened := a.fileTable(c, cn, true, m, &s.list, s.dir, s.entries, tok, narrow)
					if a.focusReq == cn.id && a.page == pageWorkspace {
						tb.Focus()
						a.focusReq = 0
					}
					if e, ok := selected(s.sel, s.entries); (tb.Submitted() || opened) && ok {
						if e.IsDir {
							a.listRemote(s, sftpsvc.RemoteJoin(s.dir, e.Name))
						} else {
							a.openRemote(cn, sftpsvc.RemoteJoin(s.dir, e.Name), e.Size)
						}
					}
					if len(s.entries) == 0 {
						ui.Text(c, m.Empty()).TextColor(tok.TextMuted).FontSize(theme.Footnote)
					}
					if s.err != "" {
						ui.Text(c, m.ErrGeneric(s.err)).TextColor(tok.Danger).FontSize(theme.Footnote)
					}
				})
				if files := remote.DroppedFiles(); len(files) > 0 {
					a.Upload(cn, files, s.dir)
				}
				// local rows upload here, from this pane or another one: the
				// local disk is the same for all. Remote rows of another pane
				// are not taken (no copying between remotes).
				if d, ok := ui.Drop[filesDrag](remote); ok && !d.remote {
					a.Upload(cn, d.paths, s.dir)
				}
				if d, ok := ui.DragOver[filesDrag](remote); remote.FileDragOver() || ok && !d.remote {
					remote.Background(tok.Accent.Alpha(0.08))
				}
			})
		}
		if stacked {
			ui.SplitVerticalPercent(c.Key("stacked"), &fs.split, localPane, remotePane).Grow(1).FillWidth()
		} else {
			ui.SplitPercent(c.Key("sides"), &fs.split, localPane, remotePane).Grow(1).FillWidth()
		}
	})
	if body.FocusWithin() && t.Focus != n.ID {
		t.Focus = n.ID
	}
	a.askView(c, cn, m)
	a.deleteView(c, cn, m)
	a.conflictView(c, cn, m)
	a.openAskView(c, cn, m)
	a.localConflictView(c, cn, m)
	a.snippetView(c, cn, tok, m)
	a.transfersSheet(c, cn, tok, m)
}

// refreshForTransfers lists the sides again as transfers finish: once
// all are, else at most every second.
func (a *App) refreshForTransfers(c *ui.Context, cn *conn) {
	fs := cn.fs
	done, active := 0, 0
	for _, t := range fs.transfers.List() {
		switch {
		case t.State == sftpsvc.Done:
			done++
		case t.Active():
			active++
		}
	}
	if done < fs.done {
		fs.done = done // cleared
	}
	if done == fs.done {
		return
	}
	if active == 0 || c.Now().Sub(fs.refreshed) >= time.Second {
		fs.done, fs.refreshed = done, c.Now()
		a.refreshSides(cn)
		return
	}
	c.After(time.Second)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
