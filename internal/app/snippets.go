package app

import (
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

type snippetState struct {
	query, group, selected, err string
	form                        *snippetForm
	showing, deleting           bool
	deleteID                    string
	hold                        focusHold
}

func (a *App) snippetTarget() *conn {
	cn := a.focused()
	if cn == nil || cn.kind != layout.Terminal || cn.tv == nil || cn.s == nil || cn.s.Status().State != session.Connected {
		return nil
	}
	return cn
}

func (a *App) snippetsPanel(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	ui.Column(c.Key("snippets")).Width(360).Shrink(0).FillHeight().Background(a.contentFill(tok.Canvas, canvasAlpha)).Children(func() {
		ui.Row(c).Height(theme.Space(10)).Shrink(0).Padding(0, theme.Space(2), 0, theme.Space(4)).Gap(theme.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.Snippets()).FontSize(theme.Body).FontWeight(600).Grow(1).Shrink(1).SingleLine()
			if (IconButton{Icon: icons.Add, Label: m.SnippetNew(), Size: Small}).Clicked(c, "snippet:new") {
				a.openSnippet(m, assets.Snippet{}, false)
			}
			if (IconButton{Icon: icons.Close, Label: m.ClosePanel(), Size: Small}).Clicked(c, "snippet:close") {
				a.inspector = ""
			}
		})
		ui.Column(c).FillWidth().Shrink(0).Padding(0, theme.Space(3), theme.Space(2), theme.Space(3)).Gap(theme.Space(2)).Children(func() {
			target := m.SnippetNoTarget()
			if cn := a.snippetTarget(); cn != nil {
				target = m.SnippetTarget(a.pv(cn.host.Label()), strconv.Itoa(cn.termNo))
			}
			panelNote(c, tok, target)
			ui.TextInput(c.Key("snippet:search"), &a.snippets.query).Placeholder(m.SnippetSearch()).Label(m.SnippetSearch()).FillWidth()
			a.menuChip(c, "snippet:groups", chip{Icon: icons.Folder, Label: a.snippetGroupLabel(m), Tip: m.SnippetFilterGroup()}, func() []menuEntry { return a.snippetGroupMenu(m) })
			if a.snippets.err != "" {
				ui.Text(c, a.snippets.err).FontSize(theme.Footnote).TextColor(tok.Danger)
			}
		})
		ui.Scroll(c.Key("snippet:list")).Grow(1).Shrink(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Padding(0, theme.Space(3)).Gap(theme.Space(1)).Children(func() {
				count := 0
				for _, n := range a.store.Snippets() {
					body, err := a.snippetContent(n.ID)
					if !a.snippetMatches(n, body) {
						continue
					}
					count++
					a.snippetRow(c, m, tok, n, body, err)
				}
				if count == 0 {
					panelNote(c, tok, m.SnippetEmpty())
				}
			})
		})
		a.snippetPreview(c, m, tok)
	})
}

func (a *App) snippetGroupLabel(m locales.Messages) string {
	if a.snippets.group == "" {
		return m.SnippetAllGroups()
	}
	return a.snippets.group
}
func (a *App) snippetGroupMenu(m locales.Messages) []menuEntry {
	out := []menuEntry{{Label: m.SnippetAllGroups(), Checked: a.snippets.group == "", Do: func() { a.snippets.group = "" }}}
	for _, g := range a.snippetGroups() {
		out = append(out, menuEntry{Label: g, Checked: a.snippets.group == g, Do: func() { a.snippets.group = g }})
	}
	return out
}
func (a *App) snippetGroups() []string {
	out := []string{}
	seen := map[string]bool{}
	for _, n := range a.store.Snippets() {
		if n.Group != "" && !seen[n.Group] {
			out = append(out, n.Group)
			seen[n.Group] = true
		}
	}
	return out
}
func (a *App) snippetMatches(n assets.Snippet, body string) bool {
	if a.snippets.group != "" && n.Group != a.snippets.group {
		return false
	}
	return strings.Contains(strings.ToLower(n.Name+"\n"+n.Group+"\n"+body), strings.ToLower(strings.TrimSpace(a.snippets.query)))
}
func (a *App) snippetRow(c *ui.Context, m locales.Messages, tok theme.Tokens, n assets.Snippet, body string, err error) {
	ui.Row(c.Key("snippet:row:" + n.ID)).FillWidth().Gap(theme.Space(1)).AlignItems(ui.Center).Children(func() {
		row := ui.ButtonBase(c.Key("snippet:select:" + n.ID)).Grow(1).Shrink(1).MinWidth(0).Padding(theme.Space(2)).Radius(theme.RadiusSmall).Label(m.SnippetPreview(n.Name))
		if a.snippets.selected == n.ID {
			row.Background(tok.Accent.Alpha(.12))
		}
		row.Children(func() {
			ui.Column(c).FillWidth().Gap(theme.Space(1)).Children(func() {
				ui.Text(c, n.Name).FontSize(theme.Body).FontWeight(600).SingleLine()
				preview := strings.SplitN(body, "\n", 2)[0]
				if err != nil {
					preview = snippetErrorText(m, err)
				}
				ui.Text(c, preview).Font(a.termFamily()).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
				mode := m.SnippetInsert()
				if n.SendEnter {
					mode = m.SnippetSendEnter()
				}
				ui.Text(c, mode).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
			})
		})
		if row.Clicked() {
			a.snippets.selected = n.ID
		}
		label, ic := m.SnippetInsertAction(n.Name), icons.Paste
		if n.SendEnter {
			label, ic = m.SnippetSendAction(n.Name), icons.Send
		}
		target := a.snippetTarget()
		disabled := err != nil || target == nil || target.tv.snippetSending
		if (IconButton{Icon: ic, Label: label, Disabled: disabled, Size: Small}).Clicked(c, "snippet:send:"+n.ID) {
			a.startSnippet(m, n.ID, n.SendEnter)
		}
		a.menuButton(c, "snippet:more:"+n.ID, IconButton{Icon: icons.More, Label: m.SnippetMore(n.Name), Size: Small}, func() []menuEntry { return a.snippetMenu(m, n, disabled) })
	})
}
func (a *App) snippetMenu(m locales.Messages, n assets.Snippet, disabled bool) []menuEntry {
	out := []menuEntry{{Icon: icons.Edit, Label: m.Edit(), Do: func() { a.openSnippet(m, n, false) }}, {Icon: icons.Copy, Label: m.SnippetDuplicate(), Do: func() { a.openSnippet(m, n, true) }}, {Icon: icons.Delete, Label: m.Delete(), Danger: true, Do: func() { a.snippets.deleteID = n.ID; a.snippets.deleting = true }}}
	if n.SendEnter {
		out = append(out, menuSep, menuEntry{Icon: icons.Paste, Label: m.SnippetInsert(), Disabled: disabled, Do: func() { a.startSnippet(m, n.ID, false) }})
	}
	return out
}
func (a *App) snippetPreview(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	n, ok := a.store.Snippet(a.snippets.selected)
	if !ok {
		return
	}
	ui.Scroll(c.Key("snippet:preview")).FillWidth().MaxHeight(theme.Space(40)).Shrink(1).Children(func() {
		ui.Column(c).FillWidth().Padding(theme.Space(3)).Gap(theme.Space(2)).Children(func() {
			ui.Text(c, n.Name).FontSize(theme.Body).FontWeight(600)
			body, err := a.snippetContent(n.ID)
			if err != nil {
				body = snippetErrorText(m, err)
			}
			ui.Text(c, body).Font(a.termFamily()).FontSize(theme.Footnote)
		})
	})
}
func (a *App) startSnippet(m locales.Messages, id string, enter bool) {
	a.snippets.err = ""
	if err := a.sendSnippet(id, enter); err != nil {
		a.snippets.err = snippetErrorText(m, err)
	}
}
func (a *App) snippetSendResult(err error) {
	a.toast(func(m locales.Messages) ui.Toast {
		if err != nil {
			return ui.Toast{Title: snippetErrorText(m, err), Type: "error"}
		}
		return ui.Toast{Title: m.SnippetSent()}
	})
}
