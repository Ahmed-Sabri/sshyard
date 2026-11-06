package app

import (
	"errors"
	"slices"
	"strings"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

type snippetForm struct {
	meta         assets.Snippet
	content, err string
	// adding shows the field naming a new group in place of the choice.
	adding   bool
	newGroup string
}

func (a *App) openSnippet(m locales.Messages, n assets.Snippet, duplicate bool) {
	if n.ID != "" && !a.unlocked() {
		a.withSecrets(func() { a.openSnippet(m, n, duplicate) })
		return
	}

	f := &snippetForm{meta: n}
	if n.ID != "" {
		body, err := a.snippetContent(n.ID)
		if err != nil && !errors.Is(err, keychain.ErrNotFound) {
			a.snippets.err = m.SnippetReadFailed(snippetErrorText(m, err))
			return
		}
		f.content = body
	}
	if duplicate {
		f.meta.ID = ""
		f.meta.SendEnter = false
	}
	a.snippets.form = f
	a.snippets.showing = true
}
func (a *App) saveSnippetForm(m locales.Messages) bool {
	if a.snippets.form != nil && !a.unlocked() {
		draft := a.snippets.form
		a.withSecrets(func() {
			if a.snippets.form == draft {
				a.saveSnippetForm(m)
			}
		})
		return false
	}

	f := a.snippets.form
	if f == nil {
		return false
	}
	n, err := a.saveSnippet(f.meta, f.content)
	if err != nil {
		f.err = m.SnippetSaveFailed(snippetErrorText(m, err))
		return false
	}
	if a.snippets.grouped {
		a.snippets.group = n.Group
	}
	a.snippets.selected = n.ID
	a.snippets.form = nil
	a.snippets.showing = false
	a.snippets.err = ""
	return true
}
func (a *App) confirmSnippetDelete(m locales.Messages) {
	if err := a.deleteSnippet(a.snippets.deleteID); err != nil {
		if errors.Is(err, keychain.ErrLocked) && !a.unlocked() {
			id := a.snippets.deleteID
			a.withSecrets(func() {
				if a.snippets.deleteID == id {
					a.confirmSnippetDelete(m)
				}
			})
			return
		}
		a.snippets.err = m.SnippetDeleteFailed(snippetErrorText(m, err))
		return
	}
	if a.snippets.selected == a.snippets.deleteID {
		a.snippets.selected = ""
	}
	if a.snippets.grouped && a.snippets.group != "" {
		found := false
		for _, g := range a.snippetGroups() {
			if g == a.snippets.group {
				found = true
			}
		}
		if !found {
			a.snippets.grouped, a.snippets.group = false, ""
		}
	}
	a.snippets.err = ""
}

// snippetGroupField chooses the group, the default one for a new entry, or
// names a new group in place.
func (a *App) snippetGroupField(c *ui.Context, m locales.Messages, f *snippetForm) {
	ui.Row(c).FillWidth().Gap(theme.Space(2)).AlignItems(ui.Center).Children(func() {
		if f.adding {
			in := formTextInput(c.Key("snippet:newGroup"), &f.newGroup).Placeholder(m.SnippetGroupName()).Label(m.SnippetGroupName()).AutoFocus().Grow(1).Shrink(1)
			add := (IconButton{Icon: icons.Check, Label: m.SnippetAddGroup()}).Clicked(c, "snippet:group:add")
			if in.Submitted() || add {
				a.addSnippetGroup(m, f)
			}
			if (IconButton{Icon: icons.Close, Label: m.SnippetCancelGroup()}).Clicked(c, "snippet:group:cancel") {
				f.adding, f.newGroup, f.err = false, "", ""
			}
			return
		}
		g := snippetGroupTitle(m, f.meta.Group)
		options := []string{m.SnippetDefaultGroup()}
		for _, name := range append(a.snippetGroups(), f.meta.Group) {
			if name != "" && !slices.Contains(options, name) {
				options = append(options, name)
			}
		}
		if formCombobox(c.Key("snippet:group"), &g, options).Label(m.FieldGroup()).Grow(1).Shrink(1).Changed() {
			f.meta.Group = snippetGroupName(g)
		}
		if (IconButton{Icon: icons.NewFolder, Label: m.NewGroup()}).Clicked(c, "snippet:group:new") {
			f.adding, f.newGroup = true, ""
		}
	})
}

// addSnippetGroup chooses the group named in the form, an existing one when
// the name differs only in case. The group exists once an entry is saved in it.
func (a *App) addSnippetGroup(m locales.Messages, f *snippetForm) bool {
	g := snippetGroupName(f.newGroup)
	if strings.TrimSpace(f.newGroup) == "" {
		f.err = m.RequiredField(m.FieldGroup())
		return false
	}
	if err := assets.ValidateSnippetGroup(g); err != nil {
		f.err = snippetErrorText(m, err)
		return false
	}
	for _, name := range a.snippetGroups() {
		if strings.EqualFold(name, g) {
			g = name
		}
	}
	f.meta.Group = g
	f.adding, f.newGroup, f.err = false, "", ""
	return true
}

func (a *App) snippetSheets(c *ui.Context, m locales.Messages) {
	if f := a.snippets.form; f != nil {
		tok := currentTokens(c)
		sheet(ui.Modal(c, &a.snippets.showing, func() {
			ui.Column(c).Width(440).Gap(theme.Space(3)).Children(func() {
				title := m.SnippetNew()
				if f.meta.ID != "" {
					title = m.SnippetEdit()
				}
				sheetTitle(c, title)
				ui.Form(c, func() {
					ui.Field(c, m.FieldName(), func() {
						in := formTextInput(c.Key("snippet:name"), &f.meta.Name).Label(m.FieldName()).AutoFocus()
						if !f.adding {
							a.snippets.hold.apply(c, f, in)
						}
					})
					ui.Field(c, m.FieldGroup(), func() {
						a.snippetGroupField(c, m, f)
					})
					ui.Field(c, m.SnippetContent(), func() {
						formTextArea(c.Key("snippet:content"), &f.content).Label(m.SnippetContent()).Font(a.termFamily()).FillWidth().Height(theme.Space(40))
					})
					ui.Row(c).FillWidth().Gap(theme.Space(2)).AlignItems(ui.Center).Children(func() {
						formSwitch(c.Key("snippet:enter"), &f.meta.SendEnter).Label(m.SnippetImmediate())
						ui.Text(c, m.SnippetImmediate()).FontSize(theme.Body).Grow(1).Shrink(1)
					})
				})
				if f.err != "" {
					ui.Text(c, f.err).FontSize(theme.Footnote).TextColor(tok.Danger)
				}
				ui.Row(c).Gap(theme.Space(2)).Children(func() {
					ui.Spacer(c)
					if pill(c, m.Cancel(), false, false, false) {
						a.snippets.showing = false
					}
					if pill(c, m.Save(), true, false, false) {
						a.saveSnippetForm(m)
					}
				})
			})
		}), tok)
		if !a.snippets.showing {
			a.snippets.form = nil
		}
	}
	if a.snippets.deleting {
		n, _ := a.store.Snippet(a.snippets.deleteID)
		if ui.AlertDialog(c, &a.snippets.deleting, m.SnippetDelete(), m.SnippetDeleteConfirm(n.Name), m.Cancel(), m.Delete()) == 1 {
			a.confirmSnippetDelete(m)
		}
	}
}

// snippetErrorText never exposes backend text, paths, commands or bodies.
func snippetErrorText(m locales.Messages, err error) string {
	var store *snippetStoreError
	if errors.As(err, &store) {
		stage := m.SnippetStageWrite()
		switch store.stage {
		case "read":
			stage = m.SnippetStageRead()
		case "metadata":
			stage = m.SnippetStageMetadata()
		case "rollback":
			stage = m.SnippetStageRollback()
		}
		return m.SnippetFailureDetail(stage, snippetErrorText(m, store.err))
	}
	var field *assets.FieldError
	if errors.As(err, &field) {
		name := m.SnippetContent()
		if field.Field == "snippetName" {
			name = m.FieldName()
		}
		if field.Field == "snippetGroup" {
			name = m.FieldGroup()
		}
		switch field.Reason {
		case "required":
			return m.RequiredField(name)
		case "tooLong":
			return m.SnippetTooLong(name)
		default:
			return m.SnippetInvalid(name)
		}
	}
	switch {
	case errors.Is(err, keychain.ErrNotFound), errors.Is(err, assets.ErrNotFound):
		return m.SnippetMissing()
	case errors.Is(err, errSnippetNoTerminal):
		return m.SnippetNoTarget()
	case errors.Is(err, errSnippetDisconnected):
		return m.SnippetDisconnected()
	case errors.Is(err, errSnippetUnsafePaste):
		return m.SnippetUnsafe()
	case errors.Is(err, errSnippetSend):
		return m.SnippetSendFailed()
	case errors.Is(err, errSnippetBusy):
		return m.SnippetBusy()
	}
	var op *keychain.OpError
	var system *keychain.SystemError
	var linux *keychain.LinuxServiceError
	if errors.As(err, &op) || errors.As(err, &system) || errors.As(err, &linux) {
		return unlockErrorText(m, err)
	}
	for _, known := range []error{keychain.ErrLocked, keychain.ErrBusy, keychain.ErrMasterMissing, keychain.ErrMasterInvalid, keychain.ErrCorrupt, keychain.ErrVersion, keychain.ErrUnavailable} {
		if errors.Is(err, known) {
			return unlockErrorText(m, err)
		}
	}
	return m.SnippetStorageFailed()
}
