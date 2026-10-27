package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// The usage panel: the processes of the focused session's server and what
// each takes of the CPU, the memory and the disk, read again every
// usageEvery while the panel shows, in a table sorted by any column.

// usageEvery is how often the processes are read; the second reading
// comes sooner, as the CPU and the disk take two.
const (
	usageEvery = 2 * time.Second
	usageFirst = time.Second
)

// usageState is what the usage panel read, of which session.
type usageState struct {
	// conn and connects are the session read, and its connection; gen
	// counts the readings started, so that a late one for another
	// session is dropped.
	conn, connects, gen int
	running             bool
	at                  time.Time
	prev                session.ProcSample
	procs               []session.Proc
	read                bool
	err                 error
	list                ui.ListState
	sort                ui.SortOrder
	filter              string
}

// sshConn is the session of the focused pane's host that holds an SSH
// connection: the pane's own, else one of its tab on that host.
func (a *App) sshConn(cn *conn) *conn {
	if cn == nil || isLocal(cn.host.ID) {
		return nil
	}
	if cn.s != nil {
		return cn
	}
	if t := a.ws.ActiveTab(); t != nil {
		for _, p := range t.Panes() {
			if o := a.conns[p.Item.ID]; o != nil && o.s != nil && o.host.ID == cn.host.ID {
				return o
			}
		}
	}
	return nil
}

// usageNote says why the panel cannot read the server, "" when it can:
// tc is the session it would read.
func (a *App) usageNote(m locales.Messages, cn, tc *conn) string {
	switch {
	case cn == nil:
		return m.InspectorEmpty()
	case isLocal(cn.host.ID):
		return m.MonitorLocal()
	case tc == nil:
		return m.MonitorNoTerminal()
	case tc.s.Status().State != session.Connected:
		return m.MonitorNotConnected()
	}
	return ""
}

// readUsage reads the processes of tc's server when it is time, and asks
// for the frame of the next reading.
func (a *App) readUsage(c *ui.Context, tc *conn) {
	u := a.usage
	connects := tc.s.Status().Connects
	if u.conn != tc.id || u.connects != connects {
		u.gen++
		u.conn, u.connects, u.running, u.at = tc.id, connects, false, time.Time{}
		u.prev, u.procs, u.read, u.err = session.ProcSample{}, nil, false, nil
	}
	if u.running {
		return
	}
	every := usageEvery
	if u.prev.CPUTotal > 0 && !u.read {
		every = usageFirst
	}
	now := c.Now()
	if wait := every - now.Sub(u.at); !u.at.IsZero() && wait > 0 {
		c.After(wait)
		return
	}
	if errors.Is(u.err, session.ErrNoProcs) {
		return // not Linux: no use asking again
	}
	client := tc.s.Client()
	if client == nil {
		return
	}
	u.running, u.at = true, now
	gen := u.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		s, err := session.ReadProcs(ctx, client)
		cancel()
		a.post(func() {
			if u.gen != gen {
				return
			}
			u.running, u.err = false, err
			if err != nil {
				return
			}
			u.read = u.prev.CPUTotal > 0
			u.procs, u.prev = session.ProcDelta(u.prev, s), s
		})
	}()
}

// usageCols are the columns of the usage table, by the IDs it sorts by.
func usageCols(m locales.Messages) []ui.TableColumn {
	return []ui.TableColumn{
		{Title: m.ColProcess(), ID: "name", Sortable: true, MinWidth: 80},
		{Title: "PID", ID: "pid", Width: 64, Align: ui.End, Sortable: true},
		{Title: "CPU", ID: "cpu", Width: 72, Align: ui.End, Sortable: true},
		{Title: m.ColMemory(), ID: "mem", Width: 80, Align: ui.End, Sortable: true},
		{Title: m.ColDisk(), ID: "disk", Width: 88, Align: ui.End, Sortable: true},
	}
}

// sortProcs sorts ps by the column o names, the pid breaking ties.
func sortProcs(ps []session.Proc, o ui.SortOrder) {
	by := func(a, b session.Proc) int {
		switch o.Column {
		case "name":
			return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		case "pid":
			return cmp.Compare(a.PID, b.PID)
		case "mem":
			return cmp.Compare(a.MemKB, b.MemKB)
		case "disk":
			return cmp.Compare(a.Disk, b.Disk)
		}
		return cmp.Compare(a.CPU, b.CPU)
	}
	slices.SortStableFunc(ps, func(a, b session.Proc) int {
		r := by(a, b)
		if o.Descending {
			r = -r
		}
		return cmp.Or(r, cmp.Compare(a.PID, b.PID))
	})
}

func fmtCPU(p float64) string { return fmt.Sprintf("%.1f%%", p) }

// usagePanel is the usage panel's body.
func (a *App) usagePanel(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	if a.usage == nil {
		a.usage = &usageState{sort: ui.SortOrder{Column: "cpu", Descending: true}, list: ui.ListState{RoundRows: true}}
	}
	u := a.usage
	cn := a.focused()
	tc := a.sshConn(cn)
	if note := a.usageNote(m, cn, tc); note != "" {
		panelNote(c, tok, note)
		return
	}
	a.readUsage(c, tc)
	switch {
	case errors.Is(u.err, session.ErrNoProcs):
		panelNote(c, tok, m.UsageLinuxOnly())
		return
	case u.err != nil && u.prev.CPUTotal == 0:
		panelNote(c, tok, m.UsageFailed(u.err.Error()))
		return
	case u.prev.CPUTotal == 0:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			panelNote(c, tok, m.MonitorWaiting())
		})
		return
	}
	ps := u.procs
	if q := strings.ToLower(strings.TrimSpace(u.filter)); q != "" {
		ps = slices.DeleteFunc(slices.Clone(ps), func(p session.Proc) bool {
			return !strings.Contains(strings.ToLower(p.Name), q) && !strings.Contains(strings.ToLower(p.User), q) && strconv.Itoa(p.PID) != q
		})
	} else {
		ps = slices.Clone(ps)
	}
	sortProcs(ps, u.sort)
	ui.Row(c.Key("usagetools")).Gap(8).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Row(c.Key("usageq")).Grow(1).Shrink(1).MinWidth(0).Height(28).Padding(0, 10).Gap(6).AlignItems(ui.Center).
			Radius(theme.RadiusControl).Background(tok.Fill).Children(func() {
			ui.Icon(c, icons.Search.SVG).Size(14, 14).Shrink(0).TextColor(tok.TextMuted)
			ui.TextInputBase(c.Key("q"), &u.filter).Placeholder(m.FilterProcesses()).Label(m.FilterProcesses()).Grow(1).Shrink(1).MinWidth(40)
		})
		ui.Text(c, m.ProcessCount(len(ps))).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
	})
	u.list.Sort = &u.sort
	fam := a.termFamily()
	ui.Table(c.Key("procs"), &u.list, usageCols(m), len(ps), func(row, col int) {
		p := ps[row]
		num := func(s string, ok bool) {
			t := ui.Text(c, s).FontSize(theme.Footnote).SingleLine().Font(fam)
			if !ok {
				t.TextColor(tok.TextMuted)
			}
		}
		switch col {
		case 0:
			tip := p.Name + " · PID " + strconv.Itoa(p.PID) + " · " + p.User
			ui.Text(c, p.Name).FontSize(theme.Footnote).SingleLine().Tooltip(tip)
		case 1:
			num(strconv.Itoa(p.PID), false)
		case 2:
			if p.CPUOK {
				t := ui.Text(c, fmtCPU(p.CPU)).FontSize(theme.Footnote).SingleLine().Font(fam)
				if p.CPU >= 50 {
					t.TextColor(barColor(tok, min(p.CPU, 100)))
				}
			} else {
				num(m.MetricsDash(), false)
			}
		case 3:
			num(fmtKB(p.MemKB), true)
		case 4:
			if p.DiskOK {
				num(fmtRate(p.Disk), p.Disk > 0)
			} else {
				num(m.MetricsDash(), false)
			}
		}
	}).Grow(1).Shrink(1).MinHeight(0).FillWidth()
}
