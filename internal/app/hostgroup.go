package app

import (
	"slices"
	"strings"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// hostGroupField chooses an existing group or names one in the host draft.
func (a *App) hostGroupField(c *ui.Context, m locales.Messages, f *hostForm) {
	ui.Row(c).FillWidth().Gap(theme.Space(2)).AlignItems(ui.Center).Children(func() {
		if f.addingGroup {
			in := ui.TextInput(c.Key("f:newGroup"), &f.newGroup).Label(m.NewGroup()).Placeholder(m.GroupPlaceholder()).AutoFocus().Grow(1).Shrink(1)
			a.editorHold.apply(c, &f.newGroup, in)
			add := (IconButton{Icon: icons.Check, Label: m.SnippetAddGroup()}).Clicked(c, "f:group:add")
			if in.Submitted() || add {
				a.addHostGroup(m, f)
			}
			if (IconButton{Icon: icons.Close, Label: m.SnippetCancelGroup()}).Clicked(c, "f:group:cancel") {
				f.addingGroup, f.newGroup, f.err = false, "", ""
			}
			return
		}
		group := f.group
		if group == "" {
			group = m.NoGroup()
		}
		options := append([]string{m.NoGroup()}, a.store.Groups()...)
		if f.group != "" && !slices.Contains(options, f.group) {
			options = append(options, f.group)
		}
		if ui.Combobox(c.Key("f:group"), &group, options).Label(m.FieldGroup()).Grow(1).Shrink(1).Changed() {
			f.group = group
			if group == m.NoGroup() {
				f.group = ""
			}
		}
		if (IconButton{Icon: icons.NewFolder, Label: m.NewGroup()}).Clicked(c, "f:group:new") {
			f.addingGroup, f.newGroup = true, ""
		}
	})
}

// addHostGroup only updates the draft; saving the host creates its group.
func (a *App) addHostGroup(m locales.Messages, f *hostForm) bool {
	group := strings.TrimSpace(f.newGroup)
	if group == "" {
		f.err = m.RequiredField(m.FieldGroup())
		return false
	}
	for _, name := range a.store.Groups() {
		if strings.EqualFold(name, group) {
			group = name
			break
		}
	}
	f.group = group
	f.addingGroup, f.newGroup, f.err = false, "", ""
	return true
}
