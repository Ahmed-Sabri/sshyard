package app

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/termlog"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/vt"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// The session logs panel lists the terminals: it starts and stops each
// one's log, a text file of what it printed from then on, and saves what
// a terminal holds already. Logs are never started on their own.

var errLogNoTerminal = errors.New("log: not a terminal pane")

// logDir is the folder the session logs go to, a folder of each host's
// in it.
func (a *App) logDir() string {
	if d := a.settings.SessionLog.Dir; d != "" {
		return d
	}
	return filepath.Join(a.localHome(), "SSHYard", "logs")
}

// startLog starts the log of terminal pane c, from what it prints next.
func (a *App) startLog(c *conn) error {
	if c == nil || c.kind != layout.Terminal || c.s == nil {
		return errLogNoTerminal
	}
	if c.log != nil {
		return nil
	}
	h := a.liveHost(c.host)
	r, err := termlog.Start(a.logDir(), termlog.Header{Name: h.Label(), Target: h.Target()}, time.Now(), a.invalidate)
	if err != nil {
		return err
	}
	c.log = r
	c.s.Term.SetLineHook(r.Line)
	return nil
}

// stopLog ends the log of c with the line its terminal is on; the file is
// closed in the background, a failure not reported yet told then.
func (a *App) stopLog(c *conn) {
	r := c.log
	if r == nil {
		return
	}
	c.log = nil
	c.s.Term.SetLineHook(nil)
	tail, failed := c.s.Term.CursorLine(), r.Err() != nil
	go func() {
		if err := r.Close(tail, time.Now()); err != nil && !failed {
			a.post(func() { a.logToast(err) })
		}
	}()
}

// toggleLog starts the log of c, or stops it.
func (a *App) toggleLog(c *conn) {
	if c.log != nil {
		a.stopLog(c)
		return
	}
	if err := a.startLog(c); err != nil {
		a.logToast(err)
	}
}

func (a *App) logToast(err error) {
	a.toast(func(m locales.Messages) ui.Toast { return ui.Toast{Title: m.LogFailed(err.Error()), Type: "error"} })
}

// logCount is how many terminals are being logged, and whether the log of
// any of them failed.
func (a *App) logCount() (n int, failed bool) {
	for _, c := range a.conns {
		if c.log != nil {
			n++
			failed = failed || c.log.Err() != nil
		}
	}
	return n, failed
}

// bufferText is all a terminal holds, its scrollback and its screen, as
// text.
func bufferText(t *vt.Terminal) string {
	text := t.Text(vt.Pos{}, vt.Pos{Line: math.MaxInt32, Col: math.MaxInt32})
	return strings.TrimRight(text, " \n") + "\n"
}

// saveBuffer saves what terminal c holds to a file asked for.
func (a *App) saveBuffer(c *conn, m locales.Messages) {
	if a.p.SaveFile == nil || c.s == nil {
		return
	}
	text := bufferText(c.s.Term)
	dir, title := a.logDir(), m.LogSaveOutput()
	name := termlog.SafeName(a.liveHost(c.host).Label()) + "_" + time.Now().Format("2006-01-02_150405") + ".txt"
	go func() {
		if os.MkdirAll(dir, 0o700) != nil {
			dir = a.localHome()
		}
		path, err := a.p.SaveFile(title, filepath.Join(dir, name))
		if err == nil && path != "" {
			err = os.WriteFile(path, []byte(text), 0o600)
		}
		a.post(func() {
			switch {
			case err != nil:
				a.toast(func(m locales.Messages) ui.Toast { return ui.Toast{Title: m.LogSaveFailed(err.Error()), Type: "error"} })
			case path != "":
				a.toast(func(m locales.Messages) ui.Toast { return ui.Toast{Title: m.LogSaved(path)} })
			}
		})
	}()
}

// openLogDir shows the logs folder, made when it is not there yet.
func (a *App) openLogDir() {
	if a.p.OpenPath == nil {
		return
	}
	dir := a.logDir()
	go func() {
		err := os.MkdirAll(dir, 0o700)
		if err == nil {
			err = a.p.OpenPath(dir)
		}
		if err != nil {
			a.post(func() { a.logToast(err) })
		}
	}()
}

// chooseLogDir asks for the folder of the logs to come.
func (a *App) chooseLogDir(m locales.Messages) {
	if a.p.PickFolder == nil {
		return
	}
	title, start := m.LogChooseFolder(), a.logDir()
	go func() {
		dir, err := a.p.PickFolder(title, start)
		a.post(func() {
			if err != nil {
				a.logToast(err)
				return
			}
			if dir != "" {
				a.setLogDir(dir)
			}
		})
	}()
}

// setLogDir makes dir the folder of the logs to come; those being kept
// stay where they are.
func (a *App) setLogDir(dir string) error {
	st := a.settings
	st.SessionLog.Dir = dir
	return a.ApplySettings(st)
}

// logPanel is the session logs panel: the folder, then the terminals,
// each with its log.
func (a *App) logPanel(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	panes := a.terminalPanes(a.ws.Tabs)
	n, _ := a.logCount()
	ui.Column(c.Key("logs")).Fill().Children(func() {
		a.panelHead(c, m, inspectLog, m.SessionLogs())
		ui.Row(c.Key("log:dir")).FillWidth().Shrink(0).Padding(0, theme.Space(2), theme.Space(2), theme.Space(4)).Gap(theme.Space(1.5)).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Folder.SVG).Size(14, 14).Shrink(0).TextColor(tok.TextMuted)
			ui.Text(c, a.logDir()).FontSize(theme.Footnote).TextColor(tok.TextMuted).SingleLine().Grow(1).Shrink(1).MinWidth(0).Tooltip(a.logDir())
			if (IconButton{Icon: icons.OpenFile, Label: m.LogOpenFolder(), Disabled: a.p.OpenPath == nil, Size: Small}).Clicked(c, "log:open") {
				a.openLogDir()
			}
			if (IconButton{Icon: icons.Edit, Label: m.LogChooseFolder(), Disabled: a.p.PickFolder == nil, Size: Small}).Clicked(c, "log:choose") {
				a.chooseLogDir(m)
			}
		})
		ui.Row(c.Key("log:all")).FillWidth().Shrink(0).Padding(0, theme.Space(2), theme.Space(2), theme.Space(4)).Gap(theme.Space(1)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.LogRecordingCount(n)).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Grow(1).Shrink(1)
			if (IconButton{Icon: icons.Record, Label: m.LogStartAll(), Disabled: n == len(panes), Size: Small}).Clicked(c, "log:startall") {
				for _, cn := range panes {
					if err := a.startLog(cn); err != nil {
						a.logToast(err)
						break
					}
				}
			}
			if (IconButton{Icon: icons.Stop, Label: m.LogStopAll(), Disabled: n == 0, Size: Small}).Clicked(c, "log:stopall") {
				for _, cn := range a.conns {
					a.stopLog(cn)
				}
			}
		})
		ui.Box(c).Height(1).Shrink(0).FillWidth().Background(tok.Line)
		gutterScroll(c, ui.Scroll(c.Key("log:list")).Grow(1).Shrink(1).MinHeight(0).FillWidth()).Children(func() {
			ui.Column(c).FillWidth().Padding(theme.Space(2)).Gap(theme.Space(0.5)).Children(func() {
				if len(panes) == 0 {
					ui.Column(c).FillWidth().Padding(theme.Space(8), theme.Space(4)).AlignItems(ui.Center).Children(func() {
						panelNote(c, tok, m.LogNoTerminal())
					})
					return
				}
				for _, cn := range panes {
					a.logRow(c, m, tok, cn)
				}
			})
		})
		ui.Box(c).Height(1).Shrink(0).FillWidth().Background(tok.Line)
		ui.Column(c).FillWidth().Shrink(0).Padding(theme.Space(3)).Children(func() {
			panelNote(c, tok, m.LogNote())
		})
	})
}

// logRow is a terminal of the logs panel: its log, kept or not, and how
// long and big it is so far.
func (a *App) logRow(c *ui.Context, m locales.Messages, tok theme.Tokens, cn *conn) {
	r := cn.log
	ui.Column(c.Key("log:row:"+strconv.Itoa(cn.id))).FillWidth().Padding(theme.Space(1), theme.Space(2)).Gap(theme.Space(0.5)).Radius(theme.RadiusControl).Children(func() {
		ui.Row(c).FillWidth().MinHeight(24).Gap(theme.Space(2)).AlignItems(ui.Center).Children(func() {
			dot(c, stateColor(tok, cn.s.Status().State))
			ui.Text(c, a.paneName(cn)).FontSize(theme.Footnote).SingleLine().Grow(1).Shrink(1).MinWidth(0)
			if r != nil && r.Err() == nil {
				d := time.Since(r.Started())
				tickDur(c, d)
				ui.Text(c, fmtDur(m, d)+" · "+humanSize(r.Size())).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(0)
			}
			label := m.LogStart()
			if r != nil {
				label = m.LogStop()
			}
			if (IconButton{Icon: icons.Record, On: icons.RecordOn, Label: label, Active: r != nil, Toggle: true, Size: Small}).Clicked(c, "rec") {
				a.toggleLog(cn)
			}
			if (IconButton{Icon: icons.Save, Label: m.LogSaveOutput(), Disabled: a.p.SaveFile == nil, Size: Small}).Clicked(c, "save") {
				a.saveBuffer(cn, m)
			}
			if (IconButton{Icon: icons.FileText, Label: m.LogOpenFile(), Disabled: r == nil || a.p.OpenPath == nil, Size: Small}).Clicked(c, "open") && r != nil {
				path := r.Path()
				go func() {
					if err := a.p.OpenPath(path); err != nil {
						a.post(func() { a.logToast(err) })
					}
				}()
			}
		})
		if r != nil && r.Err() != nil {
			ui.Text(c, m.LogFailed(r.Err().Error())).FontSize(theme.Caption).TextColor(tok.Danger).FillWidth()
		}
	})
}
