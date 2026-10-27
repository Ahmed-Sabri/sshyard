package app

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

// metricsProbe is how often a visible bar times the keepalive.
const metricsProbe = 5 * time.Second

// metricsWanted reports whether this host shows a bar. The global switch
// hides every host. Off hides one host while the global switch is on.
func (a *App) metricsWanted(h assets.Host) bool {
	if !a.settings.MetricsEnabled || h.Metrics == assets.MetricsOff {
		return false
	}
	return true
}

// commitMetricsMount saves the extra mount after the monitoring settings
// close (or another settings page shows) on a field that still had the
// caret. On the page itself the field saves on Enter and when focus
// leaves it.
func (a *App) commitMetricsMount() {
	if (a.page == pageSettings && a.prefNow() == prefMonitoring) || !a.metMountFocus {
		return
	}
	a.metMountFocus = false
	if a.metMount == a.settings.MetricsMount {
		return
	}
	s := a.settings
	s.MetricsMount = a.metMount
	if err := a.ApplySettings(s); err != nil {
		a.toast(func(m locales.Messages) ui.Toast {
			return ui.Toast{Title: m.ErrGeneric(err.Error()), Type: "error"}
		})
	}
}

func (a *App) minimizedNow() bool {
	a.metMu.Lock()
	defer a.metMu.Unlock()
	return a.minimized
}

// SetMinimized pauses every collector while the window is minimised.
// Stop only cancels, so a server that never closes the channel cannot
// stall this caller. Probes reset on the next frame, because sessions
// live on the UI thread.
func (a *App) SetMinimized(min bool) {
	a.metMu.Lock()
	a.minimized = min
	var cols []*session.Collector
	if min {
		for _, c := range a.metrics {
			cols = append(cols, c)
		}
	}
	a.metMu.Unlock()
	if min {
		for _, c := range cols {
			c.Stop()
		}
	}
	a.post(func() { a.syncMetrics() })
}

// stopCollectors ends every collector. It does not touch sessions.
// Each cancel is immediate. The wait is bounded so a server that never
// closes the channel cannot hold the window open.
func (a *App) stopCollectors() {
	a.metMu.Lock()
	cols := a.metrics
	a.metrics = map[string]*session.Collector{}
	a.metMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, c := range cols {
		c.Stop()
	}
	for _, c := range cols {
		c.Wait(ctx)
	}
}

// syncMetrics starts one collector per host that has a visible terminal
// and stops it when none remain visible. The collector does not take a
// pool reference. Call it on the UI thread.
func (a *App) syncMetrics() {
	type want struct {
		client   *ssh.Client
		interval int
		mount    string
		show     bool
	}
	byKey := map[string]*want{}
	var order []string

	active := map[int]bool{}
	if !a.minimizedNow() && a.page == pageWorkspace {
		if t := a.ws.ActiveTab(); t != nil {
			for _, p := range t.Panes() {
				active[p.Item.ID] = true
			}
		}
	}
	for _, cn := range a.conns {
		if cn == nil || cn.s == nil || cn.fw != nil || cn.kind != layout.Terminal {
			continue
		}
		h := cn.host
		if cur, ok := a.host(cn.host.ID); ok {
			h = cur
		}
		visible := a.metricsWanted(h) && active[cn.id] && cn.s.Status().State == session.Connected
		if visible {
			cn.s.SetProbe(metricsProbe)
		} else {
			cn.s.SetProbe(0)
		}
		key := cn.s.Config().Key
		w := byKey[key]
		if w == nil {
			w = &want{}
			byKey[key] = w
			order = append(order, key)
		}
		if visible {
			if cl := cn.s.Client(); cl != nil {
				w.show = true
				w.client = cl
				w.interval = a.settings.MetricsInterval
				w.mount = a.settings.MetricsMount
			}
		}
	}

	a.metMu.Lock()
	if a.metrics == nil {
		a.metrics = map[string]*session.Collector{}
	}
	var stop []*session.Collector
	type job struct {
		c *session.Collector
		w *want
	}
	var jobs []job
	for k, c := range a.metrics {
		if byKey[k] == nil {
			stop = append(stop, c)
			delete(a.metrics, k)
		}
	}
	for _, k := range order {
		w := byKey[k]
		c := a.metrics[k]
		if !w.show || w.client == nil {
			if c != nil {
				stop = append(stop, c)
			}
			continue
		}
		if c == nil {
			c = session.NewCollector()
			a.metrics[k] = c
		}
		jobs = append(jobs, job{c, w})
	}
	a.metMu.Unlock()

	for _, c := range stop {
		c.Stop()
	}
	for _, j := range jobs {
		// A minimize between the scan and here must not start a command.
		if a.minimizedNow() {
			j.c.Stop()
			continue
		}
		j.c.Start(j.w.client, j.w.interval, j.w.mount)
	}
}

func (a *App) metricsCollector(cn *conn) *session.Collector {
	if cn == nil || cn.s == nil {
		return nil
	}
	a.metMu.Lock()
	defer a.metMu.Unlock()
	return a.metrics[cn.s.Config().Key]
}

// hideMetrics turns the bar off for one host and keeps that choice.
// A failed save leaves the host unchanged and says so.
func (a *App) hideMetrics(id string) {
	h, ok := a.host(id)
	if !ok {
		return
	}
	h.Metrics = assets.MetricsOff
	if isTemp(id) {
		a.temps[id] = h
	} else if _, err := a.store.Update(h); err != nil {
		a.toast(func(m locales.Messages) ui.Toast {
			return ui.Toast{Title: m.MetricsHideFailed(err.Error()), Type: "error"}
		})
		return
	}
	for _, cn := range a.conns {
		if cn.host.ID == id {
			cn.host.Metrics = assets.MetricsOff
		}
	}
}

func metricsIndex(m assets.MetricsMode) int {
	switch m {
	case assets.MetricsOn:
		return 1
	case assets.MetricsOff:
		return 2
	default:
		return 0
	}
}

func metricsMode(i int) assets.MetricsMode {
	switch i {
	case 1:
		return assets.MetricsOn
	case 2:
		return assets.MetricsOff
	default:
		return assets.MetricsFollow
	}
}

func intervalIndex(n int) int {
	switch session.MetricsInterval(n) {
	case 1:
		return 0
	case 5:
		return 2
	case 10:
		return 3
	default:
		return 1
	}
}

// meterColor is warning above 80% and danger above 95%.
func meterColor(tok theme.Tokens, pct float64, ok bool) ui.Color {
	if !ok {
		return tok.Text
	}
	if pct > 95 {
		return tok.Danger
	}
	if pct > 80 {
		return tok.Warning
	}
	return tok.Text
}

// latencyColor is warning above 300 ms.
func latencyColor(tok theme.Tokens, d time.Duration, ok bool) ui.Color {
	if ok && d > 300*time.Millisecond {
		return tok.Warning
	}
	return tok.Text
}

func fmtKB(kb uint64) string {
	const m = 1024
	const g = 1024 * 1024
	if kb >= g {
		return fmt.Sprintf("%.1fG", float64(kb)/float64(g))
	}
	if kb >= m {
		return fmt.Sprintf("%.1fM", float64(kb)/float64(m))
	}
	return fmt.Sprintf("%dK", kb)
}

func fmtPct(p float64) string { return fmt.Sprintf("%.0f%%", p) }

func fmtLoad(v float64) string { return fmt.Sprintf("%.2f", v) }

func fmtRate(b float64) string {
	if b < 0 {
		b = 0
	}
	const k = 1024.0
	switch {
	case b >= k*k*k:
		return fmt.Sprintf("%.1fG/s", b/(k*k*k))
	case b >= k*k:
		return fmt.Sprintf("%.1fM/s", b/(k*k))
	case b >= k:
		return fmt.Sprintf("%.1fK/s", b/k)
	default:
		return fmt.Sprintf("%.0fB/s", b)
	}
}

func metricsReason(m locales.Messages, code string) string {
	switch code {
	case session.ReasonExec:
		return m.MetricsReasonExec()
	case session.ReasonShell:
		return m.MetricsReasonShell()
	case session.ReasonParse:
		return m.MetricsReasonParse()
	default:
		return ""
	}
}

func memoryText(m locales.Messages, rd session.Reading) string {
	if !rd.MemOK {
		d := m.MetricsDash()
		return m.MetricsMemory(d, d, d, d)
	}
	return m.MetricsMemory(fmtKB(rd.MemUsedKB), fmtKB(rd.MemTotalKB), fmtKB(rd.SwapUsedKB), fmtKB(rd.SwapTotalKB))
}

func memoryHot(rd session.Reading) (float64, bool) {
	if !rd.MemOK {
		return 0, false
	}
	pct := rd.MemPct
	if rd.SwapTotalKB > 0 && rd.SwapPct > pct {
		pct = rd.SwapPct
	}
	return pct, true
}

func loadText(m locales.Messages, rd session.Reading) string {
	if !rd.LoadOK {
		d := m.MetricsDash()
		return m.MetricsLoad(d, d, d)
	}
	return m.MetricsLoad(fmtLoad(rd.Load1), fmtLoad(rd.Load5), fmtLoad(rd.Load15))
}

func latencyText(m locales.Messages, d time.Duration, ok bool) string {
	return m.MetricsLatencyLabel(latencyValue(m, d, ok))
}

// latencyValue is the latency alone, as "12 ms", or a dash.
func latencyValue(m locales.Messages, d time.Duration, ok bool) string {
	if !ok {
		return m.MetricsDash()
	}
	num := "<1"
	if d >= time.Millisecond {
		num = strconv.FormatInt(d.Milliseconds(), 10)
	}
	return m.MetricsLatency(num)
}

func rootVol(vols []session.VolUse) (session.VolUse, bool) {
	for _, v := range vols {
		if v.Mount == "/" {
			return v, true
		}
	}
	if len(vols) > 0 {
		return vols[0], true
	}
	return session.VolUse{}, false
}

func maxGPU(gs []session.GPU) (float64, bool) {
	var pct float64
	ok := false
	for _, g := range gs {
		if !g.UtilOK {
			continue
		}
		u := float64(g.Util)
		if !ok || u > pct {
			pct, ok = u, true
		}
	}
	return pct, ok
}

func (a *App) metricsItemMenu(el ui.Element, cn *conn, m locales.Messages) {
	el.ContextMenu(func(menu *ui.Menu) {
		if menu.Item(m.MetricsHide()).Chosen() {
			a.hideMetrics(cn.host.ID)
		}
		if menu.Item(m.OpenSettings()).Chosen() {
			a.openSettings(prefMonitoring)
		}
	})
}

func (a *App) metricsText(c *ui.Context, cn *conn, m locales.Messages, text, label string, col ui.Color, fam string) {
	tx := ui.Text(c, text).Font(fam).FontSize(theme.Caption).TextColor(col).SingleLine().Shrink(0).Label(label)
	a.metricsItemMenu(tx, cn, m)
}

func (a *App) metricsButton(c *ui.Context, cn *conn, m locales.Messages, key, text, label string, col ui.Color, fam string, open *bool, tip string, pop func()) {
	b := ui.ButtonBase(c.Key(key)).Label(label).Tooltip(tip).Font(fam).FontSize(theme.Caption).TextColor(col).Shrink(0)
	a.metricsItemMenu(b, cn, m)
	if b.Clicked() {
		was := *open
		cn.metCPU, cn.metDisk, cn.metNet, cn.metGPU = false, false, false, false
		*open = !was
	}
	b.Children(func() {
		tx := ui.Text(c, text).Font(fam).FontSize(theme.Caption).TextColor(col).SingleLine().PassThrough()
		a.metricsItemMenu(tx, cn, m)
	})
	ui.Popover(c, b, open, func() {
		ui.Column(c).Padding(8, 10).Gap(6).Children(pop)
	})
}

func (a *App) metricsBar(c *ui.Context, cn *conn, tok theme.Tokens, m locales.Messages) {
	var rd session.Reading
	reason := ""
	if col := a.metricsCollector(cn); col != nil {
		rd, reason = col.View()
	}
	fam := a.termFamily()
	ui.Box(c).Height(1).Shrink(0).FillWidth().Background(tok.Line)
	row := ui.Row(c.Key("metrics")).MinHeight(22).Shrink(0).FillWidth().Padding(2, 8, 2, 8).Gap(8).AlignItems(ui.Center).
		Font(fam).FontSize(theme.Caption).Label(m.MetricsBar())
	a.metricsItemMenu(row, cn, m)
	row.Children(func() {
		if reason != "" {
			why := metricsReason(m, reason)
			label := m.MetricsUnavailable()
			tip := label
			if why != "" {
				label = m.MetricsUnavailableReason(m.MetricsUnavailable(), why)
				tip = why
			}
			tx := ui.Text(c, m.MetricsUnavailable()).Font(fam).FontSize(theme.Caption).TextColor(tok.Text).SingleLine().Grow(1).Shrink(1).Label(label).Tooltip(tip)
			a.metricsItemMenu(tx, cn, m)
			a.latencyItem(c, cn, tok, m, fam)
			return
		}
		cpuPct := m.MetricsDash()
		if rd.CPUOK {
			cpuPct = fmtPct(rd.CPU)
		}
		cpu := m.MetricsCPU(cpuPct)
		a.metricsButton(c, cn, m, "mcpu", cpu, cpu, meterColor(tok, rd.CPU, rd.CPUOK), fam, &cn.metCPU, cpu, func() {
			a.cpuPop(c, m, tok, fam, rd)
		})
		hot, hotOK := memoryHot(rd)
		mem := memoryText(m, rd)
		a.metricsText(c, cn, m, mem, mem, meterColor(tok, hot, hotOK), fam)
		diskPct := m.MetricsDash()
		diskOK := false
		var diskHot float64
		if v, ok := rootVol(rd.Vols); ok {
			diskPct = fmtPct(v.Pct)
			diskHot, diskOK = v.Pct, true
		}
		disk := m.MetricsDisk(diskPct)
		a.metricsButton(c, cn, m, "mdisk", disk, disk, meterColor(tok, diskHot, diskOK), fam, &cn.metDisk, disk, func() {
			a.diskPop(c, m, fam, rd)
		})
		up, down := m.MetricsDash(), m.MetricsDash()
		if rd.NetOK {
			up, down = fmtRate(rd.NetUp), fmtRate(rd.NetDown)
		}
		net := m.MetricsNet(up, down)
		netLabel := m.MetricsNetLabel(up, down)
		a.metricsButton(c, cn, m, "mnet", net, netLabel, tok.Text, fam, &cn.metNet, a.netTip(m, rd, netLabel), func() {
			a.netPop(c, m, fam, rd)
		})
		load := loadText(m, rd)
		a.metricsText(c, cn, m, load, load, tok.Text, fam)
		if len(rd.GPUs) > 0 {
			gp := m.MetricsDash()
			gok := false
			var gpct float64
			if p, ok := maxGPU(rd.GPUs); ok {
				gp = fmtPct(p)
				gpct, gok = p, true
			}
			gpu := m.MetricsGPU(gp)
			a.metricsButton(c, cn, m, "mgpu", gpu, gpu, meterColor(tok, gpct, gok), fam, &cn.metGPU, gpu, func() {
				a.gpuPop(c, m, fam, rd)
			})
		}
		ui.Spacer(c)
		a.latencyItem(c, cn, tok, m, fam)
	})
}

func (a *App) latencyItem(c *ui.Context, cn *conn, tok theme.Tokens, m locales.Messages, fam string) {
	d, ok := cn.s.Latency()
	text := latencyText(m, d, ok)
	a.metricsText(c, cn, m, text, text, latencyColor(tok, d, ok), fam)
}

func (a *App) cpuPop(c *ui.Context, m locales.Messages, tok theme.Tokens, fam string, rd session.Reading) {
	ui.Text(c, m.MetricsCores()).Font(fam).FontSize(theme.Caption).FontWeight(600)
	if len(rd.Cores) == 0 {
		ui.Text(c, m.MetricsNoCores()).Font(fam).FontSize(theme.Caption).Label(m.MetricsNoCores())
		return
	}
	for i, core := range rd.Cores {
		pct := m.MetricsDash()
		col := meterColor(tok, 0, false)
		if core.OK {
			pct = fmtPct(core.Pct)
			col = meterColor(tok, core.Pct, true)
		}
		label := m.MetricsCore(strconv.Itoa(i), pct)
		w := float32(4)
		if core.OK && core.Pct > 0 {
			p := core.Pct
			if p > 100 {
				p = 100
			}
			w = 4 + 76*float32(p)/100
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).Font(fam).FontSize(theme.Caption).TextColor(col).Label(label)
			ui.Box(c).Width(w).Height(6).Radius(3).Background(col)
		})
	}
}

func (a *App) diskPop(c *ui.Context, m locales.Messages, fam string, rd session.Reading) {
	ui.Text(c, m.MetricsDiskTitle()).Font(fam).FontSize(theme.Caption).FontWeight(600)
	if len(rd.Vols) == 0 {
		ui.Text(c, m.MetricsDash()).Font(fam).FontSize(theme.Caption)
		return
	}
	for _, v := range rd.Vols {
		line := m.MetricsVol(v.Mount, fmtKB(v.UsedKB), fmtKB(v.TotalKB), fmtPct(v.Pct))
		ui.Text(c, line).Font(fam).FontSize(theme.Caption).Label(line)
	}
}

func (a *App) netTip(m locales.Messages, rd session.Reading, label string) string {
	if len(rd.NICs) == 0 {
		return label
	}
	tip := ""
	for i, n := range rd.NICs {
		if i > 0 {
			tip += "\n"
		}
		tip += nicLine(m, n)
	}
	return tip
}

func nicLine(m locales.Messages, n session.NICRate) string {
	up, down := m.MetricsDash(), m.MetricsDash()
	if n.OK {
		up, down = fmtRate(n.Up), fmtRate(n.Down)
	}
	return m.MetricsNIC(n.Name, up, down)
}

func (a *App) netPop(c *ui.Context, m locales.Messages, fam string, rd session.Reading) {
	ui.Text(c, m.MetricsNetTitle()).Font(fam).FontSize(theme.Caption).FontWeight(600)
	if len(rd.NICs) == 0 {
		ui.Text(c, m.MetricsDash()).Font(fam).FontSize(theme.Caption)
		return
	}
	for _, n := range rd.NICs {
		line := nicLine(m, n)
		ui.Text(c, line).Font(fam).FontSize(theme.Caption).Label(line)
	}
}

func gpuBits(m locales.Messages, g session.GPU) (util, mem, temp string) {
	util, mem, temp = m.MetricsDash(), m.MetricsDash(), m.MetricsDash()
	if g.UtilOK {
		util = fmtPct(float64(g.Util))
	}
	if g.MemOK {
		mem = strconv.FormatInt(g.MemUsed, 10) + "/" + strconv.FormatInt(g.MemTotal, 10) + " MiB"
	}
	if g.TempOK {
		temp = strconv.FormatInt(g.Temp, 10) + "°C"
	}
	return util, mem, temp
}

func (a *App) gpuPop(c *ui.Context, m locales.Messages, fam string, rd session.Reading) {
	ui.Text(c, m.MetricsGPUTitle()).Font(fam).FontSize(theme.Caption).FontWeight(600)
	for _, g := range rd.GPUs {
		util, mem, temp := gpuBits(m, g)
		line := m.MetricsGPULine(util, mem, temp)
		ui.Text(c, line).Font(fam).FontSize(theme.Caption).Label(line)
	}
}
