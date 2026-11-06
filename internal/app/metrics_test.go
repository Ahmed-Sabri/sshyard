package app

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

func linuxBody(second bool) string {
	cpu := "cpu 100 0 0 900 0 0 0 0\ncore 50 0 0 450 0 0 0 0\ncore 50 0 0 450 0 0 0 0\n"
	nic := "nic eth0 1000 2000\nnic eth1 0 0\n"
	if second {
		cpu = "cpu 200 0 0 1000 0 0 0 0\ncore 100 0 0 500 0 0 0 0\ncore 150 0 0 450 0 0 0 0\n"
		nic = "nic eth0 3000 2000\nnic eth1 0 4000\n"
	}
	return "os linux\n" + cpu +
		"mem 8388608 4194304 2097152 1048576\n" +
		"load 0.15 0.10 0.05\n" + nic +
		"vol 1000000 400000 /\n" +
		"vol 2000000 500000 /data\n"
}

func wrapFrame(body string) string { return "<<<SSHYARD\n" + body + ">>>\n" }

func linuxFrames(gpu bool) []string {
	a, b := linuxBody(false), linuxBody(true)
	if gpu {
		a += "gpu 40 1024 8192 55\ngpu 80 2048 8192 70\n"
		b += "gpu 40 1024 8192 55\ngpu 80 2048 8192 70\n"
	}
	return []string{wrapFrame(a), wrapFrame(b)}
}

func darwinFrames() []string {
	one := wrapFrame("os darwin\ncpu 100 0 0 900 0 0 0 0\nmem 8388608 4194304 2097152 1048576\nload 0.15 0.10 0.05\nvol 1000000 400000 /\n")
	two := wrapFrame("os darwin\ncpu 200 0 0 1000 0 0 0 0\nmem 8388608 4194304 2097152 1048576\nload 0.15 0.10 0.05\nvol 1000000 400000 /\n")
	return []string{one, two}
}

// frameExec writes frames, then a heartbeat until the channel closes.
type frameExec struct {
	frames  []string
	starts  atomic.Int32
	running atomic.Int32
}

func (f *frameExec) run(ch ssh.Channel, _ string) int {
	f.starts.Add(1)
	f.running.Add(1)
	defer f.running.Add(-1)
	for _, frame := range f.frames {
		if _, err := io.WriteString(ch, frame); err != nil {
			return 0
		}
		time.Sleep(20 * time.Millisecond)
	}
	for {
		if _, err := io.WriteString(ch, ".\n"); err != nil {
			return 0
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func startMetricSrv(t *testing.T, exec func(ssh.Channel, string) int) *sshtest.Server {
	t.Helper()
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	srv.Exec = exec
	return srv
}

func clickContaining(t *testing.T, tt *ui.Tester, sub string) {
	t.Helper()
	for _, s := range tt.Texts() {
		if strings.Contains(s, sub) {
			if err := tt.Click(s); err != nil {
				t.Fatalf("click %q: %v; texts %q", s, err, tt.Texts())
			}
			return
		}
	}
	t.Fatalf("no text contains %q in %q", sub, tt.Texts())
}

func textCount(tt *ui.Tester, s string) int {
	n := 0
	for _, text := range tt.Texts() {
		if strings.Contains(text, s) {
			n++
		}
	}
	return n
}

func textsContain(tt *ui.Tester, s string) bool {
	for _, text := range tt.Texts() {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}

func hasLatency(tt *ui.Tester) bool {
	for _, text := range tt.Texts() {
		if strings.Contains(text, "Latency ") && !strings.Contains(text, "—") {
			return true
		}
	}
	return false
}

func waitSample(t *testing.T, tt *ui.Tester) {
	t.Helper()
	waitForIn(t, tt, 2*time.Second, "metrics sample", func() bool {
		return tt.HasText("CPU 50%") &&
			tt.HasText("Memory 4.0G/8.0G, swap 1.0G/2.0G") &&
			tt.HasText("Disk 40%") &&
			tt.HasText("Load 0.15 0.10 0.05") &&
			tt.HasText("Network up") &&
			hasLatency(tt)
	})
}

func terminalsUp(a *App) int {
	n := 0
	for _, cn := range a.conns {
		if cn.kind == layout.Terminal && cn.fw == nil && cn.s != nil && cn.s.Status().State == session.Connected {
			n++
		}
	}
	return n
}

func TestMeterColorThresholds(t *testing.T) {
	tok := theme.LightTokens()
	for pct, want := range map[float64]ui.Color{0: tok.Success, 59.9: tok.Success, 60: tok.Warning, 79.9: tok.Warning, 80: tok.Danger, 100: tok.Danger} {
		if got := meterColor(tok, pct, true); got != want {
			t.Errorf("%v%%: %v, want %v", pct, got, want)
		}
	}
	if meterColor(tok, 99, false) != tok.TextMuted {
		t.Fatal("unknown percent took a meter color")
	}
	for d, want := range map[time.Duration]ui.Color{99 * time.Millisecond: tok.Success, 100 * time.Millisecond: tok.Warning, 300 * time.Millisecond: tok.Danger} {
		if got := latencyColor(tok, d, true); got != want {
			t.Errorf("latency %v: %v, want %v", d, got, want)
		}
	}
	if p, ok := loadPct(session.Reading{LoadOK: true, Load1: 1, Cores: make([]session.Core, 4)}); !ok || p != 25 {
		t.Errorf("load of 1 on 4 cores: %v %v", p, ok)
	}
	if fmtKB(4194304) != "4.0G" || fmtKB(8388608) != "8.0G" || fmtKB(1048576) != "1.0G" || fmtKB(2097152) != "2.0G" {
		t.Fatalf("sizes %s %s %s %s", fmtKB(4194304), fmtKB(8388608), fmtKB(1048576), fmtKB(2097152))
	}
	if fmtPct(50) != "50%" || fmtPct(40) != "40%" || fmtRate(0) != "0B/s" || fmtRate(1024) != "1.0K/s" {
		t.Fatalf("pct %s %s rate %s %s", fmtPct(50), fmtPct(40), fmtRate(0), fmtRate(1024))
	}
}

func TestMetricsBarAndCollector(t *testing.T) {
	ex := &frameExec{frames: linuxFrames(false)}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	c := connected(t, a, tt, srv)
	waitSample(t, tt)
	if textsContain(tt, "GPU") {
		t.Fatalf("gpu item without nvidia: %q", tt.Texts())
	}
	bars := textCount(tt, "CPU 50%")
	if bars < 1 {
		t.Fatal("no cpu text")
	}
	mustClick(t, tt, "CPU 50%")
	tt.Frame()
	if !tt.HasText("Core 0 50%") || !tt.HasText("Core 1 100%") {
		t.Fatalf("cores: %q", tt.Texts())
	}
	mustClick(t, tt, "CPU 50%")
	tt.Frame()
	mustClick(t, tt, "Disk 40%")
	tt.Frame()
	if !tt.HasText("/ 390.6M/976.6M (40%)") || !tt.HasText("/data 488.3M/1.9G (25%)") {
		t.Fatalf("disk popover: %q", tt.Texts())
	}
	mustClick(t, tt, "Disk 40%")
	tt.Frame()
	clickContaining(t, tt, "Network up")
	tt.Frame()
	if !hasExact(tt, "Network") || !tt.HasText("eth0") {
		t.Fatalf("network popover: %q", tt.Texts())
	}
	clickContaining(t, tt, "Network up")
	tt.Frame()
	// The load is one share of the CPUs; its averages in the popover.
	if hasExact(tt, "0.15 0.10 0.05") || !hasExact(tt, "8%") {
		t.Fatalf("load not as a percent: %q", tt.Texts())
	}
	mustClick(t, tt, "Load 0.15 0.10 0.05")
	tt.Frame()
	for _, s := range []string{"Load average", "1 minute  0.15", "5 minutes  0.10", "15 minutes  0.05", "Shared by 2 CPUs"} {
		if !tt.HasText(s) {
			t.Fatalf("load popover misses %q: %q", s, tt.Texts())
		}
	}
	mustClick(t, tt, "Load 0.15 0.10 0.05")
	tt.Frame()

	a.SplitFocused(layout.Right)
	tt.Frame()
	a.SplitFocused(layout.Right)
	waitFor(t, tt, "three terminals", func() bool { return terminalsUp(a) == 3 })
	waitFor(t, tt, "three bars", func() bool { return textCount(tt, "CPU 50%") == bars*3 })
	key := c.s.Config().Key
	if ex.starts.Load() != 1 || ex.running.Load() != 1 {
		t.Fatalf("starts %d running %d", ex.starts.Load(), ex.running.Load())
	}
	if srv.Logins() != 1 {
		t.Fatalf("logins %d", srv.Logins())
	}
	if got := a.pool.Shared(key); got != 3 {
		t.Fatalf("shared %d", got)
	}

	mustClick(t, tt, "Open Files for This Host")
	waitFor(t, tt, "files share", func() bool { return a.pool.Shared(key) == 4 })
	if ex.starts.Load() != 1 || textCount(tt, "CPU 50%") != bars*3 {
		t.Fatalf("files changed collection: starts %d bars %d", ex.starts.Load(), textCount(tt, "CPU 50%"))
	}

	var one int
	for id, cn := range a.conns {
		if cn.kind == layout.Terminal && cn.fw == nil {
			one = id
			break
		}
	}
	a.CloseSession(one)
	tt.Frame()
	if ex.starts.Load() != 1 || ex.running.Load() != 1 || a.pool.Shared(key) != 3 {
		t.Fatalf("after one close: starts %d running %d shared %d", ex.starts.Load(), ex.running.Load(), a.pool.Shared(key))
	}

	var ids []int
	for id, cn := range a.conns {
		if cn.kind == layout.Terminal && cn.fw == nil {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		a.CloseSession(id)
	}
	waitForIn(t, tt, 5*time.Second, "collector exit", func() bool { return ex.running.Load() == 0 })
	if ex.starts.Load() != 1 {
		t.Fatalf("starts %d after the last pane", ex.starts.Load())
	}
}

func TestMetricsBarMenu(t *testing.T) {
	ex := &frameExec{frames: linuxFrames(false)}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "box", "secret")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	a.Connect(h.ID, openAt{})
	waitFor(t, tt, "connected", func() bool { return a.focused().s.Status().State == session.Connected })
	waitSample(t, tt)

	if err := tt.RightClick("CPU 50%"); err != nil {
		t.Fatal(err)
	}
	menu := menuOf(a)
	if !textsHave(menu, "Hide metrics bar for this host") || !textsHave(menu, "Open Settings") {
		t.Fatalf("menu %q", menu)
	}
	if err := chooseMenu(a, tt, "Open Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.page != pageSettings || !tt.HasText("Refresh interval") {
		t.Fatalf("settings page %v %q", a.page, tt.Texts())
	}
	a.page = pageWorkspace
	tt.Frame()
	waitSample(t, tt)
	// The sample on screen predates the settings page: wait for the
	// collector, which settings stops, to come back.
	waitFor(t, tt, "bar resumes", func() bool { return ex.running.Load() == 1 })
	if err := tt.RightClick("CPU 50%"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(a, tt, "Hide metrics bar for this host"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("CPU 50%") || tt.HasText("Metrics unavailable") {
		t.Fatalf("bar still shown: %q", tt.Texts())
	}
	got, ok := a.Store().Get(h.ID)
	if !ok || got.Metrics != assets.MetricsOff {
		t.Fatalf("host metrics %q", got.Metrics)
	}
	waitForIn(t, tt, 5*time.Second, "hide stops collector", func() bool { return ex.running.Load() == 0 })
}

func TestMetricsHideSaveError(t *testing.T) {
	ex := &frameExec{frames: linuxFrames(false)}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "box", "secret")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	a.Connect(h.ID, openAt{})
	waitFor(t, tt, "connected", func() bool { return a.focused().s.Status().State == session.Connected })
	waitSample(t, tt)

	path := filepath.Join(a.p.DataDir, "hosts.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := tt.RightClick("CPU 50%"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(a, tt, "Hide metrics bar for this host"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("CPU 50%") {
		t.Fatalf("bar hidden after a failed save: %q", tt.Texts())
	}
	got, ok := a.Store().Get(h.ID)
	if !ok || got.Metrics == assets.MetricsOff {
		t.Fatalf("stored host metrics %q", got.Metrics)
	}
	if a.focused() != nil && a.focused().host.Metrics == assets.MetricsOff {
		t.Fatal("connection host flipped")
	}
	if !tt.HasText("Couldn’t hide the metrics bar") {
		t.Fatalf("no error toast: %q", tt.Texts())
	}
}

func textsHave(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func TestMetricsBarHidden(t *testing.T) {
	ex := &frameExec{frames: linuxFrames(false)}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	connected(t, a, tt, srv)
	waitSample(t, tt)

	a.SetMinimized(true)
	waitForIn(t, tt, 5*time.Second, "minimize stops", func() bool { return ex.running.Load() == 0 })
	tt.Frame()
	if ex.running.Load() != 0 {
		t.Fatal("collector restarted while minimised")
	}
	a.SetMinimized(false)
	tt.Frame()
	waitFor(t, tt, "restore", func() bool { return ex.running.Load() == 1 && ex.starts.Load() == 2 })

	a.page = pageSettings
	tt.Frame()
	waitForIn(t, tt, 5*time.Second, "settings hides", func() bool { return ex.running.Load() == 0 })
	a.page = pageWorkspace
	tt.Frame()
	waitFor(t, tt, "show again", func() bool { return ex.running.Load() == 1 && ex.starts.Load() == 3 })
}

func TestMetricsUnavailable(t *testing.T) {
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.Exec = nil
	a, tt := newTestApp(t)
	c := connected(t, a, tt, srv)
	waitFor(t, tt, "unavailable", func() bool {
		return tt.HasText("Metrics unavailable") && tt.HasText("The server refused the metrics command.") && hasLatency(tt)
	})
	for _, s := range tt.Texts() {
		if strings.Contains(s, "sh -c") || strings.Contains(s, "sh -s") || strings.Contains(s, "<<<SSHYARD") {
			t.Fatalf("remote command leaked: %s", s)
		}
	}
	tt.Type("echo hi\r")
	waitFor(t, tt, "echo", func() bool { return strings.Contains(c.s.Term.ScreenText(), "hi") })
	tt.SetLocale("zh-Hans")
	tt.Frame()
	if !tt.HasText("指标不可用") || !tt.HasText("服务器拒绝了指标命令。") {
		t.Fatalf("zh-Hans: %q", tt.Texts())
	}
}

func TestMetricsToggles(t *testing.T) {
	ex := &frameExec{frames: linuxFrames(false)}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())

	off := addHost(t, a, srv, "off", "secret")
	off.Metrics = assets.MetricsOff
	if _, err := a.Store().Update(off); err != nil {
		t.Fatal(err)
	}
	a.Connect(off.ID, openAt{})
	waitFor(t, tt, "off connected", func() bool { return a.focused().s.Status().State == session.Connected })
	tt.Frame()
	tt.Frame()
	if ex.starts.Load() != 0 || tt.HasText("CPU") || tt.HasText("Metrics unavailable") {
		t.Fatalf("host off: starts %d %q", ex.starts.Load(), tt.Texts())
	}
	a.CloseSession(a.focused().id)
	tt.Frame()

	s := a.Settings()
	s.MetricsEnabled = false
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	follow := addHost(t, a, srv, "follow", "secret")
	a.Connect(follow.ID, openAt{})
	waitFor(t, tt, "follow connected", func() bool { return a.focused().s.Status().State == session.Connected })
	tt.Frame()
	tt.Frame()
	if ex.starts.Load() != 0 || tt.HasText("Metrics bar") || tt.HasText("CPU 50%") {
		t.Fatalf("global off: starts %d %q", ex.starts.Load(), tt.Texts())
	}
	a.CloseSession(a.focused().id)
	tt.Frame()

	force := addHost(t, a, srv, "force", "secret")
	force.Metrics = assets.MetricsOn
	if _, err := a.Store().Update(force); err != nil {
		t.Fatal(err)
	}
	a.Connect(force.ID, openAt{})
	waitFor(t, tt, "force connected", func() bool { return a.focused().s.Status().State == session.Connected })
	tt.Frame()
	tt.Frame()
	if ex.starts.Load() != 0 || tt.HasText("CPU 50%") || tt.HasText("Metrics unavailable") {
		t.Fatalf("global off, host on: starts %d %q", ex.starts.Load(), tt.Texts())
	}
	s.MetricsEnabled = true
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	waitSample(t, tt)
	if ex.starts.Load() != 1 {
		t.Fatalf("host on with global on: starts %d", ex.starts.Load())
	}
}

func TestMetricsGPU(t *testing.T) {
	ex := &frameExec{frames: linuxFrames(true)}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	connected(t, a, tt, srv)
	waitForIn(t, tt, 2*time.Second, "gpu", func() bool {
		return tt.HasText("GPU 80%") && tt.HasText("CPU 50%") && !tt.HasText("Metrics unavailable")
	})
	mustClick(t, tt, "GPU 80%")
	tt.Frame()
	if !tt.HasText("40%, 1024/8192 MiB, 55°C") || !tt.HasText("80%, 2048/8192 MiB, 70°C") {
		t.Fatalf("gpu popover: %q", tt.Texts())
	}
}

func TestMetricsDarwinDash(t *testing.T) {
	ex := &frameExec{frames: darwinFrames()}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	connected(t, a, tt, srv)
	waitForIn(t, tt, 2*time.Second, "darwin metrics", func() bool {
		return tt.HasText("CPU 50%") &&
			tt.HasText("Memory 4.0G/8.0G, swap 1.0G/2.0G") &&
			tt.HasText("Disk 40%") &&
			tt.HasText("Load 0.15 0.10 0.05") &&
			tt.HasText("Network up —, down —")
	})
	if textsContain(tt, "GPU") {
		t.Fatalf("darwin showed gpu: %q", tt.Texts())
	}
	mustClick(t, tt, "CPU 50%")
	tt.Frame()
	if !tt.HasText("Per-core figures are not available.") {
		t.Fatalf("cores: %q", tt.Texts())
	}
}

func TestMetricsMountCommits(t *testing.T) {
	a, tt := newTestApp(t)
	tt.SetSize(1200, 1400)
	a.openSettings(prefMonitoring)
	tt.Frame()
	r, ok := tt.Find("Extra mount point")
	if !ok {
		t.Fatal("no mount field")
	}
	tt.ClickAt(r.X+r.W+90, r.Y+r.H)
	tt.Type("/mnt/data")
	tt.Frame()
	if got := a.Settings().MetricsMount; got != "" {
		t.Fatalf("saved on keystroke: %q", got)
	}
	tt.Key(0, ui.KeyEnter)
	if got := a.Settings().MetricsMount; got != "/mnt/data" {
		t.Fatalf("enter saved %q", got)
	}
	tt.Type("x")
	tt.Frame()
	if got := a.Settings().MetricsMount; got != "/mnt/data" {
		t.Fatalf("saved while still typing: %q", got)
	}
	if err := tt.Click("1 s"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.Settings().MetricsMount; got != "/mnt/datax" {
		t.Fatalf("blur saved %q", got)
	}
}

func TestMetricsSettingsControls(t *testing.T) {
	a, tt := newTestApp(t)
	tt.SetSize(1200, 1400)
	a.openSettings(prefMonitoring)
	tt.Frame()
	for _, s := range []string{"Metrics bar", "Refresh interval", "Extra mount point", "Shown with the root disk. Empty shows the root only.", "1 s", "10 s"} {
		if !tt.HasText(s) {
			t.Errorf("missing %q in %q", s, tt.Texts())
		}
	}
	mustClick(t, tt, "10 s")
	tt.Frame()
	if a.Settings().MetricsInterval != 10 {
		t.Fatalf("interval %d", a.Settings().MetricsInterval)
	}

	h, err := a.Store().Add(assets.Host{Name: "web", Address: "10.0.0.8", Port: 22, User: "root", Auth: assets.AuthAgent})
	if err != nil {
		t.Fatal(err)
	}
	a.openEditor(&h)
	tt.Frame()
	if !tt.HasText("Follow global") || !hasExact(tt, "On") || !hasExact(tt, "Off") {
		t.Fatalf("tri-state: %q", tt.Texts())
	}
	mustClick(t, tt, "Off")
	tt.Frame()
	if a.form == nil || a.form.metricsIdx != 2 {
		t.Fatalf("index %v", a.form)
	}
	mustClick(t, tt, "Save")
	tt.Frame()
	got, ok := a.Store().Get(h.ID)
	if !ok || got.Metrics != assets.MetricsOff {
		t.Fatalf("saved %q", got.Metrics)
	}
}

func TestLatencyText(t *testing.T) {
	rows := []struct {
		name string
		d    time.Duration
		en   string
		zh   string
	}{
		{name: "zero", en: "Latency <1 ms", zh: "延迟 <1 毫秒"},
		{name: "submillisecond", d: 400 * time.Microsecond, en: "Latency <1 ms", zh: "延迟 <1 毫秒"},
		{name: "one", d: time.Millisecond, en: "Latency 1 ms", zh: "延迟 1 毫秒"},
		{name: "twelve", d: 12 * time.Millisecond, en: "Latency 12 ms", zh: "延迟 12 毫秒"},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			if got := latencyText(locales.In("en"), tc.d, true); got != tc.en {
				t.Fatalf("en %q", got)
			}
			if got := latencyText(locales.In("zh-Hans"), tc.d, true); got != tc.zh {
				t.Fatalf("zh %q", got)
			}
		})
	}
}

func TestHostMetricsDisabledWhenGlobalOff(t *testing.T) {
	a, tt := newTestApp(t)
	h, err := a.Store().Add(assets.Host{Name: "web", Address: "10.0.0.8", Port: 22, User: "root", Auth: assets.AuthAgent})
	if err != nil {
		t.Fatal(err)
	}
	a.openEditor(&h)
	tt.Frame()
	if tt.HasText("Disabled globally") {
		t.Fatal("hint while the bar is on")
	}
	// Scroll the metrics control into view at the default window height.
	r := mustFind(t, tt, a.form.auth)
	tt.Scroll(r.X+r.W/2, r.Y+r.H/2, 0, 300)
	r = mustFind(t, tt, "Off")
	if r.W <= 0 || r.H <= 0 {
		t.Fatalf("metrics option is clipped: %v", r)
	}
	mustClick(t, tt, "Off")
	tt.Frame()
	if a.form == nil || a.form.metricsIdx != 2 {
		t.Fatalf("index %v", a.form)
	}
	a.form.metricsIdx = 0
	s := a.Settings()
	s.MetricsEnabled = false
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Disabled globally") {
		t.Fatalf("no hint: %q", tt.Texts())
	}
	mustClick(t, tt, "Off")
	if a.form.metricsIdx != 0 {
		t.Fatalf("index %d", a.form.metricsIdx)
	}
	tt.SetLocale("zh-Hans")
	tt.Frame()
	if !tt.HasText("全局已关闭") {
		t.Fatalf("zh: %q", tt.Texts())
	}
}
