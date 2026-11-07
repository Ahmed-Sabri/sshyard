package app

import (
	"errors"
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

// The broadcast panel sends a command typed once to several terminals:
// those of the active tab, or of every tab, less those left out. Each
// terminal gets it as it takes a paste, as a snippet is sent.

// broadcastWidth is the broadcast panel's width, and the logs panel's.
const broadcastWidth = 320

var errBroadcastNoTarget = errors.New("broadcast: no terminal to send to")

// broadcastState is the broadcast panel's. None of it is saved: a
// command never goes anywhere on its own after a restart.
type broadcastState struct {
	// all sends to the terminals of every tab, not the active tab's only.
	all bool
	// left are the terminals left out, by pane (conn) ID.
	left map[int]bool
	// draft is the command typed; noEnter sends it without running it.
	draft   string
	noEnter bool
	// sent is how the last send went, by pane: nil once written. sending
	// counts the writes still going.
	sent    map[int]error
	sending int
	// confirm asks before sending several lines.
	confirm bool
}

// terminalPanes are the terminal panes of tabs, in order.
func (a *App) terminalPanes(tabs []*layout.Tab) []*conn {
	var out []*conn
	for _, t := range tabs {
		for _, p := range t.Panes() {
			if c := a.conns[p.Item.ID]; c != nil && c.kind == layout.Terminal && c.tv != nil && c.s != nil {
				out = append(out, c)
			}
		}
	}
	return out
}

// paneName names terminal pane c in a list of panes: its host, and its
// number when the host has more than one.
func (a *App) paneName(c *conn) string {
	name := a.pv(a.liveHost(c.host).Label())
	if c.termNo > 0 && a.hostTermPanes(c.host.ID) > 1 {
		name += " #" + strconv.Itoa(c.termNo)
	}
	return name
}

// broadcastPanes are the terminals a command may go to: the active tab's,
// or every tab's.
func (a *App) broadcastPanes() []*conn {
	if a.bc.all {
		return a.terminalPanes(a.ws.Tabs)
	}
	if t := a.ws.ActiveTab(); t != nil {
		return a.terminalPanes([]*layout.Tab{t})
	}
	return nil
}

// broadcastTargets are the terminals a command goes to: the panes not
// left out.
func (a *App) broadcastTargets() []*conn {
	var out []*conn
	for _, c := range a.broadcastPanes() {
		if !a.bc.left[c.id] {
			out = append(out, c)
		}
	}
	return out
}

// multiLine reports text of several lines, once the line ends that end it
// are gone, as snippetBytes takes them off.
func multiLine(text string) bool {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	return strings.Contains(strings.TrimRight(text, "\n"), "\n")
}

// sendBroadcast starts writing text to each target, encoded for its
// terminal, run when enter. Each target's outcome lands in bc.sent: a
// terminal disconnected or unable to take the text safely is skipped, the
// others still get it. The error is why nothing could start.
func (a *App) sendBroadcast(text string, enter bool) error {
	if a.bc.sending > 0 {
		return errSnippetBusy
	}
	if err := assets.ValidateSnippetContent(text); err != nil {
		return err
	}
	targets := a.broadcastTargets()
	if len(targets) == 0 {
		return errBroadcastNoTarget
	}
	a.bc.sent = map[int]error{}
	for _, c := range targets {
		if c.s.Status().State != session.Connected {
			a.bc.sent[c.id] = errSnippetDisconnected
			continue
		}
		payload, err := snippetBytes(text, enter, c.s.Term.Modes())
		if err != nil {
			a.bc.sent[c.id] = err
			continue
		}
		w, err := c.s.CaptureInput()
		if err != nil {
			a.bc.sent[c.id] = errSnippetDisconnected
			continue
		}
		a.bc.sending++
		id, v := c.id, c.tv
		go func() {
			err := writeSnippet(w, payload)
			a.post(func() {
				a.bc.sending--
				a.bc.sent[id] = err
				if err == nil {
					v.offset, v.scrollAcc = 0, 0
					v.selected, v.selecting = false, false
				}
			})
		}()
	}
	return nil
}

// submitBroadcast sends the draft, once confirmed when it is several
// lines.
func (a *App) submitBroadcast(confirmed bool) {
	if multiLine(a.bc.draft) && !confirmed {
		a.bc.confirm = true
		return
	}
	if err := a.sendBroadcast(a.bc.draft, !a.bc.noEnter); err != nil {
		a.toast(func(m locales.Messages) ui.Toast { return ui.Toast{Title: broadcastErrorText(m, err), Type: "error"} })
	}
}

// broadcastErrorText says why a send to a terminal, or all of them,
// failed.
func broadcastErrorText(m locales.Messages, err error) string {
	switch {
	case errors.Is(err, errBroadcastNoTarget):
		return m.BroadcastNoTarget()
	case errors.Is(err, errSnippetBusy):
		return m.BroadcastBusy()
	}
	return snippetErrorText(m, err)
}

// broadcastPanel is the broadcast panel: where the command goes, the
// terminals it goes to, each with how the last send went, and the command.
func (a *App) broadcastPanel(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	panes := a.broadcastPanes()
	targets := a.broadcastTargets()
	ui.Column(c.Key("broadcast")).Fill().Children(func() {
		a.panelHead(c, m, inspectBroadcast, m.Broadcast())
		ui.Column(c).FillWidth().Shrink(0).Padding(0, theme.Space(3), theme.Space(2), theme.Space(3)).Gap(theme.Space(2)).Children(func() {
			scope := 0
			if a.bc.all {
				scope = 1
			}
			formSegmented(c.Key("bc:scope"), &scope, false, m.BroadcastThisTab(), m.BroadcastAllTabs())
			a.bc.all = scope == 1
			if len(panes) > 0 {
				ui.Text(c, m.BroadcastSelected(strconv.Itoa(len(targets)), strconv.Itoa(len(panes)))).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
			}
		})
		ui.Box(c).Height(1).Shrink(0).FillWidth().Background(tok.Line)
		gutterScroll(c, ui.Scroll(c.Key("bc:list")).Grow(1).Shrink(1).MinHeight(0).FillWidth()).Children(func() {
			ui.Column(c).FillWidth().Padding(theme.Space(2)).Gap(theme.Space(0.5)).Children(func() {
				if len(panes) == 0 {
					ui.Column(c).FillWidth().Padding(theme.Space(8), theme.Space(4)).AlignItems(ui.Center).Children(func() {
						panelNote(c, tok, m.BroadcastNoTarget())
					})
					return
				}
				for _, cn := range panes {
					a.broadcastRow(c, m, tok, cn)
				}
			})
		})
		ui.Box(c).Height(1).Shrink(0).FillWidth().Background(tok.Line)
		a.broadcastComposer(c, m, tok, len(targets))
	})
	if a.bc.confirm && ui.AlertDialog(c, &a.bc.confirm, m.BroadcastConfirmTitle(), m.BroadcastConfirm(strconv.Itoa(len(targets))), m.Cancel(), m.BroadcastSend()) == 1 {
		a.submitBroadcast(true)
	}
}

// broadcastRow is a terminal of the broadcast panel: whether the command
// goes to it, and how the last send to it went.
func (a *App) broadcastRow(c *ui.Context, m locales.Messages, tok theme.Tokens, cn *conn) {
	st := cn.s.Status().State
	ui.Row(c.Key("bc:row:"+strconv.Itoa(cn.id))).FillWidth().MinHeight(32).Padding(theme.Space(1), theme.Space(2)).Gap(theme.Space(2)).AlignItems(ui.Center).
		Radius(theme.RadiusControl).Children(func() {
		on := !a.bc.left[cn.id]
		formCheckbox(c.Key("bc:on"), &on, a.paneName(cn)).Grow(1).Shrink(1).MinWidth(0)
		if on == a.bc.left[cn.id] {
			if a.bc.left == nil {
				a.bc.left = map[int]bool{}
			}
			if on {
				delete(a.bc.left, cn.id)
			} else {
				a.bc.left[cn.id] = true
			}
		}
		err, done := a.bc.sent[cn.id]
		switch {
		case st != session.Connected:
			ui.Text(c, m.SnippetDisconnected()).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(1)
		case done && err != nil:
			// Why, in its tooltip: the row keeps one line.
			why := broadcastErrorText(m, err)
			ui.Row(c.Key("bc:why")).Gap(theme.Space(1)).AlignItems(ui.Center).Shrink(0).Label(why).Tooltip(why).Children(func() {
				ui.Icon(c, icons.Warning.SVG).Size(12, 12).TextColor(tok.Danger)
				ui.Text(c, m.BroadcastNotSent()).FontSize(theme.Caption).TextColor(tok.Danger).SingleLine()
			})
		case done:
			ui.Row(c).Gap(theme.Space(1)).AlignItems(ui.Center).Shrink(0).Children(func() {
				ui.Icon(c, icons.Check.SVG).Size(12, 12).TextColor(tok.Success)
				ui.Text(c, m.BroadcastSent()).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
			})
		}
		dot(c, stateColor(tok, st))
	})
}

// broadcastComposer is where the command is typed: Enter sends it,
// Shift+Enter starts a new line.
func (a *App) broadcastComposer(c *ui.Context, m locales.Messages, tok theme.Tokens, targets int) {
	ui.Column(c.Key("bc:composer")).FillWidth().Shrink(0).Padding(theme.Space(3)).Gap(theme.Space(2)).Children(func() {
		ui.TextAreaBase(c.Key("bc:draft"), &a.bc.draft).Placeholder(m.BroadcastPlaceholder()).Label(m.BroadcastPlaceholder()).
			FontSize(theme.Body).Font(a.termFamily()).Padding(theme.Space(1.5), theme.Space(2)).FillWidth().Lines(2, 8).
			Radius(theme.RadiusControl).Background(tok.Fill).
			OnTextKey(func(k ui.TextKey) bool {
				if k.Key != ui.KeyEnter || k.Mods != 0 {
					return false
				}
				text := k.Text()
				a.post(func() {
					a.bc.draft = text
					a.submitBroadcast(false)
				})
				return true
			})
		ui.Row(c).FillWidth().Gap(theme.Space(2)).AlignItems(ui.Center).Children(func() {
			run := !a.bc.noEnter
			formCheckbox(c.Key("bc:enter"), &run, m.BroadcastRun()).Shrink(1)
			a.bc.noEnter = !run
			ui.Spacer(c)
			if a.bc.sending > 0 {
				ui.Spinner(c)
			}
			empty := strings.TrimSpace(a.bc.draft) == ""
			if (IconButton{Icon: icons.Send, Label: m.BroadcastSend(), Prominent: true, Disabled: empty || targets == 0 || a.bc.sending > 0}).Clicked(c, "bc:send") {
				a.submitBroadcast(false)
			}
		})
	})
}
