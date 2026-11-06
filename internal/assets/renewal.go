package assets

import (
	"strings"
	"time"
)

// DateLayout is how a Renewal writes its day: a calendar day, the same
// in every time zone, so that a backup opened elsewhere keeps it.
const DateLayout = time.DateOnly

// Renewal is a host's subscription, as a rented server bills: it began
// on Start and renews every Months months, on the day of the month it
// began, or the month's last day where that comes first. Remind is how
// many days before each renewal to remind of it, 0 for never.
type Renewal struct {
	Start  string `json:"start"`
	Months int    `json:"months"`
	Remind int    `json:"remind"`
}

// RenewalMonths are the periods the editor offers, in months.
var RenewalMonths = []int{1, 3, 6, 12, 24, 36}

// RenewalReminds are the days before a renewal the editor offers to
// remind of it, never first.
var RenewalReminds = []int{0, 1, 3, 7, 15, 30}

// RenewalRemindDefault is a new subscription's reminder, in days.
const RenewalRemindDefault = 3

// On says the host keeps a subscription.
func (r Renewal) On() bool { return r.Start != "" }

func (r *Renewal) normalize() {
	r.Start = strings.TrimSpace(r.Start)
	if _, err := time.Parse(DateLayout, r.Start); err != nil || r.Months < 1 || r.Months > 120 {
		*r = Renewal{}
		return
	}
	r.Remind = min(max(r.Remind, 0), 365)
}

// civil is the calendar day of t, in t's location, at midnight UTC, so
// that days are counted without daylight saving in the way.
func civil(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// addMonths is day d moved n months, kept in its month: a subscription of
// the 31st renews on the 30th of a 30-day month, and back on the 31st.
func addMonths(d time.Time, n int) time.Time {
	y, m := d.Year(), d.Month()+time.Month(n)
	last := time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(y, m, min(d.Day(), last), 0, 0, 0, 0, time.UTC)
}

// Next is the first renewal on or after the day of now (in now's
// location), as a calendar day at midnight UTC: the one of today is
// still to come until the day is over. The day it began is not one.
func (r Renewal) Next(now time.Time) (time.Time, bool) {
	start, err := time.Parse(DateLayout, r.Start)
	if err != nil || r.Months < 1 {
		return time.Time{}, false
	}
	today := civil(now)
	months := (today.Year()-start.Year())*12 + int(today.Month()-start.Month())
	k := max(months/r.Months-1, 1)
	d := addMonths(start, k*r.Months)
	for d.Before(today) {
		k++
		d = addMonths(start, k*r.Months)
	}
	return d, true
}

// DaysLeft is how many days from now's day the next renewal is, 0 for
// today.
func (r Renewal) DaysLeft(now time.Time) (int, bool) {
	d, ok := r.Next(now)
	if !ok {
		return 0, false
	}
	return int(d.Sub(civil(now)).Hours() / 24), true
}

// Reminding says the next renewal is within the days of its reminder,
// and how many days away it is.
func (r Renewal) Reminding(now time.Time) (int, bool) {
	if r.Remind == 0 {
		return 0, false
	}
	days, ok := r.DaysLeft(now)
	return days, ok && days <= r.Remind
}
