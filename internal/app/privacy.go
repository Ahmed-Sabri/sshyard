package app

import "github.com/Licoy/sshyard/internal/mask"

// pv is s as the app shows it: with its IP addresses masked while privacy
// mode is on. Every view passes what it shows of a host (its label, which
// is its address when it has no name, where it logs in, a status or an
// error naming it) through here; the fields of a form being edited show
// what they hold.
func (a *App) pv(s string) string {
	if a.settings.Privacy {
		return mask.Text(s)
	}
	return s
}

// TogglePrivacy turns privacy mode on or off, and keeps it so.
func (a *App) TogglePrivacy() error {
	s := a.settings
	s.Privacy = !s.Privacy
	return a.ApplySettings(s)
}
