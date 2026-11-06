package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// soonRenewal is a yearly subscription renewing in about two days from
// now, within its reminder.
func soonRenewal(now time.Time) assets.Renewal {
	return assets.Renewal{Start: now.AddDate(-1, 0, 2).Format(assets.DateLayout), Months: 12, Remind: 30}
}

func TestHostCardAfterDelay(t *testing.T) {
	a, _ := newTestApp(t)
	m := locales.In("en")
	tt := ui.NewTester(func(c *ui.Context) { a.sidebarView(c, m, currentTokens(c)) }, 800, 500)
	h, err := a.Store().Add(assets.Host{Name: "web", Address: "10.0.0.5", User: "root", Provider: "Vultr", Note: "blog at 10.0.0.9", Renewal: soonRenewal(tt.Now())})
	if err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	days, _ := h.Renewal.DaysLeft(tt.Now())
	// The row marks the renewal coming.
	if !tt.HasText(m.RenewalSoon(renewWhen(m, days))) {
		t.Fatalf("no renewal mark in %q", tt.Texts())
	}
	r := mustFind(t, tt, "web")
	x, y := r.X+r.W/2, r.Y+r.H/2
	tt.Move(x, y)
	tt.Frame()
	if tt.HasText("Vultr") {
		t.Fatal("card shown at once")
	}
	tt.Advance(hostCardDelay - 50*time.Millisecond)
	tt.Frame()
	if tt.HasText("Vultr") {
		t.Fatal("card shown before its delay")
	}
	tt.Advance(100 * time.Millisecond)
	tt.Frame()
	line, _, _ := renewalLine(m, theme.LightTokens(), h.Renewal, tt.Now())
	for _, want := range []string{"root@10.0.0.5", "Vultr", "blog at 10.0.0.9", line, m.FieldProvider(), m.RenewalNext(), m.FieldNote()} {
		if !tt.HasText(want) {
			t.Errorf("card misses %q: %q", want, tt.Texts())
		}
	}
	a.settings.Privacy = true
	tt.Frame()
	if tt.HasText("blog at 10.0.0.9") || tt.HasText("root@10.0.0.5") {
		t.Error("privacy mode shows the addresses on the card")
	}
	a.settings.Privacy = false
	// It goes as the pointer leaves, and waits again as it comes back.
	tt.Move(790, 490)
	tt.Frame()
	if tt.HasText("Vultr") {
		t.Fatal("card stays after the pointer left")
	}
	tt.Move(x, y)
	tt.Frame()
	if tt.HasText("Vultr") {
		t.Fatal("card shown at once on coming back")
	}
	tt.Advance(hostCardDelay + 50*time.Millisecond)
	tt.Frame()
	if !tt.HasText("Vultr") {
		t.Fatal("card not shown on coming back")
	}
	// A press hides it until the pointer leaves.
	tt.Press(x, y)
	tt.Frame()
	tt.Release(x, y)
	tt.Advance(2 * hostCardDelay)
	tt.Frame()
	if tt.HasText("Vultr") {
		t.Fatal("card shown again after a press")
	}
	// The search finds the host by its provider and its note.
	for q, want := range map[string]bool{"vultr": true, "blog": true, "nothing-like-it": false} {
		a.search = q
		tt.Frame()
		if tt.HasText("web") != want {
			t.Errorf("search %q: shown %v, want %v", q, !want, want)
		}
	}
}

func TestHostPanelRenewal(t *testing.T) {
	a, _ := newTestApp(t)
	m := locales.In("en")
	tt := ui.NewTester(func(c *ui.Context) { a.hostPanel(c, m, currentTokens(c)) }, 320, 1200)
	plain, err := a.Store().Add(assets.Host{Name: "db", Address: "10.0.0.6", User: "root"})
	if err != nil {
		t.Fatal(err)
	}
	a.selected = plain.ID
	tt.Frame()
	if tt.HasText(m.RenewalSection()) {
		t.Fatal("subscription card shown for a host without one")
	}
	h, err := a.Store().Add(assets.Host{Name: "web", Address: "10.0.0.5", User: "root", Provider: "Vultr", Note: "blog", Renewal: soonRenewal(tt.Now())})
	if err != nil {
		t.Fatal(err)
	}
	a.selected = h.ID
	tt.Frame()
	line, _, _ := renewalLine(m, theme.LightTokens(), h.Renewal, tt.Now())
	for _, want := range []string{m.RenewalSection(), "Vultr", "blog", line, m.RemindDaysBefore(30)} {
		if !tt.HasText(want) {
			t.Errorf("panel misses %q: %q", want, tt.Texts())
		}
	}
}

func TestHostEditorRenewal(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	h, err := a.Store().Add(assets.Host{Name: "web", Address: "10.0.0.5", User: "root"})
	if err != nil {
		t.Fatal(err)
	}
	a.openEditor(&h)
	tt.Frame()
	f := a.form
	if f.renew || f.renewOpen || f.renewMonths != 1 || f.renewRemind != assets.RenewalRemindDefault {
		t.Fatalf("new subscription fields: %+v", f.renewalForm)
	}
	f.provider, f.note = "Vultr", "blog\nproxy"
	f.renew, f.renewStart, f.renewMonths, f.renewRemind = true, time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local), 12, 7
	if !a.SaveForm(m) {
		t.Fatal(f.err)
	}
	got, _ := a.Store().Get(h.ID)
	if got.Provider != "Vultr" || got.Note != "blog\nproxy" || got.Renewal != (assets.Renewal{Start: "2026-10-01", Months: 12, Remind: 7}) {
		t.Fatalf("saved %q %q %+v", got.Provider, got.Note, got.Renewal)
	}
	a.openEditor(&got)
	tt.Frame()
	if !a.form.renewOpen || !a.form.renew || a.form.renewStart.Format(assets.DateLayout) != "2026-10-01" {
		t.Fatalf("reopened fields: %+v", a.form.renewalForm)
	}
	line, _, _ := renewalLine(m, theme.LightTokens(), got.Renewal, tt.Now())
	for _, want := range []string{m.RenewalSection(), m.TrackRenewal(), m.RenewYearly(), m.RemindDaysBefore(7), m.RenewalNextOn(line), m.NoteHint()} {
		if !tt.HasText(want) {
			t.Errorf("editor misses %q", want)
		}
	}
	a.form.renew = false
	if !a.SaveForm(m) {
		t.Fatal(a.form.err)
	}
	if got, _ := a.Store().Get(h.ID); got.Renewal.On() || got.Provider != "Vultr" {
		t.Fatalf("subscription kept after turning it off: %+v", got)
	}
}

func TestRemindRenewals(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	type note struct{ id, title, body string }
	sent := make(chan note, 8)
	a.p.Notify = func(id, title, body string, clicked func()) error {
		sent <- note{id, title, body}
		clicked()
		return nil
	}
	next := func() (note, bool) {
		select {
		case n := <-sent:
			return n, true
		case <-time.After(200 * time.Millisecond):
			return note{}, false
		}
	}
	web, err := a.Store().Add(assets.Host{Name: "web", Address: "10.0.0.5", User: "root", Provider: "Vultr", Renewal: assets.Renewal{Start: "2026-10-01", Months: 1, Remind: 3}})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []assets.Host{
		{Name: "quiet", Address: "10.0.0.6", User: "root", Renewal: assets.Renewal{Start: "2026-10-01", Months: 1}},
		{Name: "later", Address: "10.0.0.7", User: "root", Renewal: assets.Renewal{Start: "2026-10-01", Months: 1, Remind: 1}},
		{Name: "none", Address: "10.0.0.8", User: "root"},
	} {
		if _, err := a.Store().Add(h); err != nil {
			t.Fatal(err)
		}
	}
	at := func(s string) time.Time {
		d, _ := time.ParseInLocation(assets.DateLayout, s, time.Local)
		return d.Add(10 * time.Hour)
	}
	a.remindRenewals(at("2026-10-28"))
	if n, ok := next(); ok {
		t.Fatalf("notified before the reminder: %+v", n)
	}
	a.remindRenewals(at("2026-10-29"))
	n, ok := next()
	if !ok || n.id != "renewal:"+web.ID || n.title != m.RenewalNotifyTitle("web", m.RenewInDays(3)) || n.body != "2026-11-01 · "+m.RenewMonthly()+" · Vultr" {
		t.Fatalf("notification %+v %v", n, ok)
	}
	if n, ok := next(); ok {
		t.Fatalf("notified twice: %+v", n)
	}
	if got := a.settings.RenewalNotified; len(got) != 1 || got[web.ID] != "2026-11-01" {
		t.Fatalf("notified %v", got)
	}
	// Clicking it shows the host.
	tt.Frame()
	if a.selected != web.ID || !a.panelOpen(inspectHost) {
		t.Fatalf("click showed %q, panels %v", a.selected, a.panels)
	}
	// Each renewal notifies once, as many days before as its host says.
	a.remindRenewals(at("2026-10-31"))
	if n, ok := next(); !ok || n.title != m.RenewalNotifyTitle("later", m.RenewInDays(1)) {
		t.Fatalf("notification of the later host %+v %v", n, ok)
	}
	if n, ok := next(); ok {
		t.Fatalf("notified again of the same renewal: %+v", n)
	}
	a.remindRenewals(at("2026-11-30"))
	titles := map[string]bool{}
	for range 2 {
		if n, ok := next(); ok && strings.HasPrefix(n.body, "2026-12-01 · ") {
			titles[n.title] = true
		}
	}
	if !titles[m.RenewalNotifyTitle("web", m.RenewInDays(1))] || !titles[m.RenewalNotifyTitle("later", m.RenewInDays(1))] {
		t.Fatalf("next renewal notifications %v", titles)
	}
	a.remindRenewals(at("2026-12-05"))
	if a.settings.RenewalNotified != nil {
		t.Fatalf("past renewals kept: %v", a.settings.RenewalNotified)
	}
	// Off, nothing is notified.
	s := a.settings
	s.RenewalNotify = false
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	a.remindRenewals(at("2026-12-30"))
	if n, ok := next(); ok {
		t.Fatalf("notified while off: %+v", n)
	}
}

func TestMCPHostRenewal(t *testing.T) {
	r := assets.Renewal{Start: "2026-10-01", Months: 3, Remind: 7}
	info := mcpHostOf(assets.Host{ID: "h1", Name: "web", Address: "10.0.0.5", Port: 22, Provider: "Vultr", Note: "blog", Renewal: r})
	now := time.Now()
	next, _ := r.Next(now)
	days, _ := r.DaysLeft(now)
	if info.Provider != "Vultr" || info.Note != "blog" || info.Renewal == nil ||
		*info.Renewal != (mcpRenewal{Start: "2026-10-01", EveryMonths: 3, RemindDays: 7, Next: next.Format(assets.DateLayout), DaysLeft: days}) {
		t.Fatalf("info %+v %+v", info, info.Renewal)
	}
	b, _ := json.Marshal(mcpHostOf(assets.Host{ID: "h2", Name: "db", Address: "10.0.0.6", Port: 22}))
	for _, absent := range []string{"renewal", "provider", "note"} {
		if strings.Contains(string(b), absent) {
			t.Errorf("empty %q listed: %s", absent, b)
		}
	}
}
