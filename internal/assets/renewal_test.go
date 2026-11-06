package assets

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func day(s string) time.Time {
	d, err := time.ParseInLocation(DateLayout, s, time.Local)
	if err != nil {
		panic(err)
	}
	return d.Add(15 * time.Hour)
}

func TestRenewalNext(t *testing.T) {
	for _, tc := range []struct {
		start  string
		months int
		now    string
		next   string
		days   int
	}{
		// A monthly subscription begun on October 1st renews on November
		// 1st; its day is still to come until it is over.
		{"2026-10-01", 1, "2026-10-01", "2026-11-01", 31},
		{"2026-10-01", 1, "2026-10-29", "2026-11-01", 3},
		{"2026-10-01", 1, "2026-11-01", "2026-11-01", 0},
		{"2026-10-01", 1, "2026-11-02", "2026-12-01", 29},
		// The 31st renews on a shorter month's last day, then on the 31st.
		{"2026-01-31", 1, "2026-02-10", "2026-02-28", 18},
		{"2026-01-31", 1, "2026-03-01", "2026-03-31", 30},
		{"2024-02-29", 12, "2025-01-01", "2025-02-28", 58},
		{"2024-02-29", 12, "2028-01-01", "2028-02-29", 59},
		// Longer periods, years after it began.
		{"2020-05-15", 3, "2026-11-06", "2026-11-15", 9},
		{"2023-06-30", 36, "2026-06-30", "2026-06-30", 0},
		// One beginning later renews a period after that.
		{"2027-01-10", 1, "2026-11-06", "2027-02-10", 96},
	} {
		r := Renewal{Start: tc.start, Months: tc.months, Remind: 3}
		next, ok := r.Next(day(tc.now))
		days, _ := r.DaysLeft(day(tc.now))
		if !ok || next.Format(DateLayout) != tc.next || days != tc.days {
			t.Errorf("%s every %d at %s: next %s in %d (%v), want %s in %d", tc.start, tc.months, tc.now, next.Format(DateLayout), days, ok, tc.next, tc.days)
		}
	}
	if _, ok := (Renewal{}).Next(time.Now()); ok {
		t.Error("no subscription renews")
	}
}

func TestRenewalReminding(t *testing.T) {
	r := Renewal{Start: "2026-10-01", Months: 1, Remind: 3}
	for now, want := range map[string]bool{"2026-10-28": false, "2026-10-29": true, "2026-11-01": true, "2026-11-02": false} {
		if _, soon := r.Reminding(day(now)); soon != want {
			t.Errorf("%s: reminding %v, want %v", now, soon, want)
		}
	}
	r.Remind = 0
	if _, soon := r.Reminding(day("2026-11-01")); soon {
		t.Error("a subscription without a reminder reminds")
	}
}

func TestRenewalNormalizeAndPersist(t *testing.T) {
	h := Host{Address: "1.2.3.4", User: "root", Provider: "  Vultr ", Note: "\n blog\nproxy  ", Renewal: Renewal{Start: " 2026-10-01 ", Months: 1, Remind: -2}}
	h.Normalize()
	if h.Provider != "Vultr" || h.Note != "blog\nproxy" || h.Renewal != (Renewal{Start: "2026-10-01", Months: 1}) {
		t.Fatalf("normalized %q %q %+v", h.Provider, h.Note, h.Renewal)
	}
	for _, bad := range []Renewal{{Start: "10/01/2026", Months: 1}, {Start: "2026-10-01"}, {Start: "2026-10-01", Months: 121}} {
		h.Renewal = bad
		h.Normalize()
		if h.Renewal.On() {
			t.Errorf("kept %+v", bad)
		}
	}
	b, _ := json.Marshal(Host{ID: "x"})
	if strings.Contains(string(b), "renewal") || strings.Contains(string(b), "provider") || strings.Contains(string(b), `"note"`) {
		t.Fatalf("empty fields written: %s", b)
	}

	path := filepath.Join(t.TempDir(), "hosts.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	web, err := s.Add(Host{Name: "web", Address: "10.0.0.5", User: "root", Provider: "Vultr", Note: "blog and proxy", Renewal: Renewal{Start: "2026-10-01", Months: 12, Remind: 7}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(Host{Name: "db", Address: "10.0.0.6", User: "root"}); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(web.ID)
	if got.Provider != "Vultr" || got.Note != "blog and proxy" || got.Renewal != (Renewal{Start: "2026-10-01", Months: 12, Remind: 7}) {
		t.Fatalf("reopened %+v", got)
	}
	for _, q := range []string{"vultr", "PROXY blog"} {
		if r := s.Search(q); len(r) != 1 || r[0].ID != web.ID {
			t.Errorf("search %q: %+v", q, r)
		}
	}
}
