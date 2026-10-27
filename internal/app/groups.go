package app

import (
	"errors"
	"strings"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// groupForm is the group sheet: a new group, or a group renamed.
type groupForm struct {
	// old is the group renamed, "" for a new one
	old  string
	name string
	// host is moved into the new group once it is made, when the sheet
	// was opened from its menu
	host string
	err  string
}

func (a *App) openGroupForm(old, host string) {
	a.group, a.grouping = &groupForm{old: old, name: old, host: host}, true
}

// SaveGroup makes or renames the group of the sheet.
func (a *App) SaveGroup(m locales.Messages) bool {
	f := a.group
	if f == nil {
		return false
	}
	var err error
	if f.old == "" {
		err = a.store.AddGroup(f.name)
	} else {
		err = a.store.RenameGroup(f.old, f.name)
	}
	if err != nil {
		var fe *assets.FieldError
		switch {
		case errors.As(err, &fe) && fe.Reason == "taken":
			f.err = m.GroupTaken()
		case errors.As(err, &fe):
			f.err = m.RequiredField(m.FieldGroup())
		default:
			f.err = m.ErrGeneric(err.Error())
		}
		return false
	}
	name := strings.TrimSpace(f.name) // as the store saved it
	if f.host != "" {
		a.store.SetGroup(f.host, name)
	}
	if f.old != "" {
		// The section stays as open or closed as it was.
		if open, ok := a.sections["g:"+f.old]; ok {
			a.sections["g:"+name] = open
		}
	}
	a.group, a.grouping = nil, false
	return true
}

func (a *App) groupView(c *ui.Context, m locales.Messages) {
	if f := a.group; f != nil {
		tok := currentTokens(c)
		sheet(ui.Modal(c, &a.grouping, func() {
			ui.Column(c).Gap(18).Width(380).Children(func() {
				title := m.NewGroup()
				if f.old != "" {
					title = m.RenameGroup()
				}
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					tile(c, icons.NewFolder, theme.Brand, 36)
					sheetTitle(c, title)
				})
				ui.Form(c, func() {
					ui.Field(c, m.FieldGroup(), func() {
						in := ui.TextInput(c.Key("g:name"), &f.name).Placeholder(m.GroupPlaceholder()).AutoFocus()
						a.groupHold.apply(c, f, in)
						if in.Submitted() {
							a.SaveGroup(m)
						}
					})
				})
				if f.err != "" {
					ui.Text(c, f.err).FontSize(theme.Footnote).TextColor(tok.Danger)
				}
				ui.Row(c).Gap(8).Children(func() {
					ui.Spacer(c)
					if pill(c, m.Cancel(), false, false, false) {
						a.grouping = false
					}
					if pill(c, m.Save(), true, false, false) {
						a.SaveGroup(m)
					}
				})
			})
		}), tok)
		if !a.grouping {
			a.group = nil
		}
	}
	if a.groupDeleting {
		switch ui.AlertDialog(c, &a.groupDeleting, m.DeleteGroup(), m.DeleteGroupConfirm(a.groupDelete), m.Cancel(), m.Delete()) {
		case 1:
			if err := a.store.DeleteGroup(a.groupDelete); err != nil {
				c.AddToast(ui.Toast{Title: m.ErrGeneric(err.Error()), Type: "error"})
			}
		}
	}
}
