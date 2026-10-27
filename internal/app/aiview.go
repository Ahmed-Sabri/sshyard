package app

import (
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/ai/policy"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func (a *App) aiPane(c *ui.Context, t *layout.Tab, n *layout.Node, cn *conn, tok theme.Tokens, m locales.Messages) {
	a.refreshMode(cn)
	if t.Focus == n.ID {
		a.aiRecent[cn.host.ID] = cn.id
	}
	head := ui.Column(c.Key("aihead")).Shrink(0).FillWidth()
	head.Children(func() {
		row := ui.Row(c.Key("head")).Height(36).Shrink(0).Padding(0, 6, 0, 10).Gap(8).AlignItems(ui.Center).Drag(dragSession{cn.id}).Label(m.PaneActions())
		row.Children(func() {
			softTile(c, kindIcon(layout.AI, t.Focus == n.ID), hostTint(tok, a.liveHost(cn.host)), 22)
			title := ui.Text(c, m.AiPane(a.pv(cn.host.Label()))).FontWeight(600).FontSize(theme.Footnote).SingleLine().Shrink(1)
			if t.Focus != n.ID && len(t.Panes()) > 1 {
				title.TextColor(tok.TextMuted)
			}
			if isTemp(cn.host.ID) {
				tempBadge(c, m, tok)
			}
			ui.Spacer(c)
			if (IconButton{Icon: icons.Stop, Label: m.StopAI(), Size: Small, Danger: true}).Clicked(c, "stop") {
				a.StopAI(cn.id)
			}
			if (IconButton{Icon: icons.SplitRight, Label: m.SplitRight(), Size: Small}).Clicked(c, "splitright") {
				t.Focus = n.ID
				a.SplitFocused(layout.Right)
			}
			if (IconButton{Icon: icons.SplitDown, Label: m.SplitDown(), Size: Small}).Clicked(c, "splitdown") {
				t.Focus = n.ID
				a.SplitFocused(layout.Bottom)
			}
			if (IconButton{Icon: icons.Close, Label: m.ClosePane(), Size: Small, Danger: true}).Clicked(c, "close") {
				a.closeSoon(cn.id)
			}
		})
		if row.Clicked() {
			t.Focus = n.ID
			a.focusReq = cn.id
			a.aiRecent[cn.host.ID] = cn.id
		}
		ui.Box(c).Height(1).Shrink(0).FillWidth().Background(tok.Line)
		ui.Row(c.Key("aipick")).Padding(6, 10).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			a.linkedSelect(c, cn, m)
			a.agentSelect(c, cn, m)
			a.providerSelect(c, cn, m)
		})
		ui.Row(c.Key("aimodes")).Padding(0, 10, 6, 10).Gap(8).AlignItems(ui.Center).Children(func() {
			shown := cn.ai.mode
			if shown == "" {
				shown = string(a.modeOf(cn))
			}
			cn.ai.modeIdx = modeIndex(shown)
			if ui.Segmented(c.Key("aimode"), &cn.ai.modeIdx, m.AiModeAsk(), m.AiModeReview(), m.AiModeFull()).Label(m.AiDefaultMode()).Changed() {
				a.setPaneMode(cn, []string{"ask", "review", "full"}[cn.ai.modeIdx])
			}
		})
		if a.modeOf(cn) == policy.ModeFull {
			ui.Row(c.Key("fullbar")).Height(22).Shrink(0).Padding(0, 10).AlignItems(ui.Center).FillWidth().Background(tok.Danger).Children(func() {
				ui.Text(c, m.AiFullBar()).FontSize(theme.Caption).FontWeight(600).TextColor(tok.AccentText).SingleLine()
			})
		}
	})

	body := ui.Scroll(c.Key("aitime")).Grow(1).FillWidth()
	body.Children(func() {
		ui.Column(c).Padding(10).Gap(8).FillWidth().Children(func() {
			if len(cn.ai.items) == 0 && !cn.ai.running {
				ui.Text(c, m.AiEmpty()).FontSize(theme.Body).TextColor(tok.TextMuted)
			}
			for i := range cn.ai.items {
				a.aiCard(c, cn, i, tok, m)
			}
			if cn.ai.paused && !cn.ai.running {
				ui.Text(c, m.AiPaused()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
				if pill(c, m.AiContinue(), true, false, false) {
					a.ContinueAI(cn.id)
				}
			}
		})
	})

	ui.Row(c.Key("aicompose")).Shrink(0).Padding(8, 10).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
		in := ui.TextInput(c.Key("aiin:"+strconv.Itoa(cn.id)+":"+strconv.Itoa(cn.ai.gen)), &cn.ai.input).Placeholder(m.AiPlaceholder()).Grow(1).Shrink(1)
		if a.focusReq == cn.id && a.page == pageWorkspace {
			in.Focus()
			a.focusReq = 0
		}
		if pill(c, m.AiSend(), true, false, cn.ai.running || strings.TrimSpace(cn.ai.input) == "") {
			a.SendAI(cn.id, cn.ai.input)
		}
	})
}

func (a *App) linkedSelect(c *ui.Context, cn *conn, m locales.Messages) {
	opts := []string{m.AiNoTerminal()}
	ids := []int{0}
	cur := m.AiNoTerminal()
	var terms []*conn
	for _, o := range a.conns {
		if o.kind == layout.Terminal && o.host.ID == cn.host.ID {
			terms = append(terms, o)
		}
	}
	for i := 1; i < len(terms); i++ {
		for j := i; j > 0 && terms[j].id < terms[j-1].id; j-- {
			terms[j], terms[j-1] = terms[j-1], terms[j]
		}
	}
	for i, o := range terms {
		label := m.Terminal() + " " + strconv.Itoa(i+1)
		opts = append(opts, label)
		ids = append(ids, o.id)
		if o.id == cn.ai.link {
			cur = label
		}
	}
	if ui.Select(c.Key("ailink"), &cur, opts).Label(m.AiLinkedTerminal()).Width(150).Changed() {
		for i, label := range opts {
			if label == cur {
				cn.ai.link = ids[i]
				a.saveLayout()
			}
		}
	}
}

func (a *App) agentSelect(c *ui.Context, cn *conn, m locales.Messages) {
	opts := []string{m.AiBuiltin()}
	cur := m.AiBuiltin()
	if cn.ai.agent != "" && cn.ai.agent != "builtin" {
		opts = append(opts, cn.ai.agent)
		cur = cn.ai.agent
	}
	if ui.Select(c.Key("aiagent"), &cur, opts).Label(m.AiAgent()).Width(130).Changed() {
		if cur == m.AiBuiltin() {
			cn.ai.agent = "builtin"
		} else {
			cn.ai.agent = cur
		}
		a.saveLayout()
	}
}

func (a *App) providerSelect(c *ui.Context, cn *conn, m locales.Messages) {
	opts := []string{m.AiNoProvider()}
	ids := []string{""}
	cur := m.AiNoProvider()
	for _, p := range a.settings.AIProviders {
		label := p.Name
		if label == "" {
			label = p.Model
		}
		if label == "" {
			label = p.ID
		}
		opts = append(opts, label)
		ids = append(ids, p.ID)
		if p.ID == cn.ai.provider {
			cur = label
		}
	}
	if ui.Select(c.Key("aiprov"), &cur, opts).Label(m.AiProvider()).Width(150).Changed() {
		for i, label := range opts {
			if label == cur {
				cn.ai.provider = ids[i]
				a.saveLayout()
			}
		}
	}
	if p := a.providerByID(cn.ai.provider); p != nil && p.Model != "" {
		ui.Text(c, p.Model).FontSize(theme.Caption).TextColor(currentTokens(c).TextMuted).SingleLine()
	}
}

func (a *App) aiCard(c *ui.Context, cn *conn, i int, tok theme.Tokens, m locales.Messages) {
	it := &cn.ai.items[i]
	switch it.kind {
	case "user":
		ui.Text(c, it.text).FontSize(theme.Body).FillWidth()
	case "text":
		ui.Text(c, it.text).FontSize(theme.Body).TextColor(tok.Text).FillWidth()
	case "think":
		ui.Collapsible(c.Key("think:"+strconv.Itoa(i)), m.AiThinking(), &it.open, func() {
			ui.Text(c, it.text).FontSize(theme.Footnote).TextColor(tok.TextMuted).FillWidth()
		})
	case "err":
		ui.Text(c, aiErrLine(m, it.text)).FontSize(theme.Footnote).TextColor(tok.Danger).FillWidth()
	case "tool":
		a.toolCard(c, cn, it, tok, m)
	}
}

func (a *App) toolCard(c *ui.Context, cn *conn, it *aiItem, tok theme.Tokens, m locales.Messages) {
	card := it.tool
	muted := tok.Text
	if !card.Run {
		muted = tok.TextMuted
	}
	title := card.Category
	if title == "" {
		title = card.Name
	}
	where := m.AiWhereRemote(cn.host.Label())
	if card.Where == string(policy.Local) {
		where = m.AiWhereLocal()
	}
	ui.Column(c.Key("tool:"+card.ID)).Padding(8, 10).Gap(4).FillWidth().Radius(theme.RadiusControl).Background(tok.Fill).Border(1, tok.Line).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontWeight(600).FontSize(theme.Footnote).TextColor(muted).SingleLine()
			ui.Text(c, riskText(m, card.Risk)).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
			ui.Text(c, where).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(1)
			ui.Spacer(c)
			ui.Text(c, verdictText(m, card.Verdict)).FontSize(theme.Caption).TextColor(muted).SingleLine()
		})
		what := card.Command
		if what == "" {
			what = card.Path
		}
		if what != "" {
			ui.Text(c, what).Font("monospace").FontSize(theme.Footnote).TextColor(muted).FillWidth()
		}
		if !card.Run && card.Refusal != "" {
			ui.Text(c, card.Refusal).FontSize(theme.Caption).TextColor(tok.TextMuted).FillWidth()
		}
		if card.Output != "" && card.Phase == "done" {
			ui.Text(c, toolOutput(m, card.Output)).Font("monospace").FontSize(theme.Caption).TextColor(muted).FillWidth()
		}
		if card.Phase == "done" && card.Run {
			ui.Row(c).Gap(8).Children(func() {
				if card.Exit >= 0 {
					ui.Text(c, m.AiExit(strconv.Itoa(card.Exit))).FontSize(theme.Caption).TextColor(tok.TextMuted)
				}
				ui.Text(c, m.AiDuration(strconv.FormatInt(card.DurMS, 10))).FontSize(theme.Caption).TextColor(tok.TextMuted)
				if card.Command != "" {
					ui.Spacer(c)
					if pill(c, m.AiInsert(), false, false, a.conns[cn.ai.link] == nil || a.conns[cn.ai.link].tv == nil) {
						a.InsertAI(cn.id, card.Command)
					}
				}
			})
		}
	})
}

func (a *App) tabAIMarks(c *ui.Context, t *layout.Tab, tok theme.Tokens, m locales.Messages) {
	full, running := false, false
	for _, p := range t.Panes() {
		cn := a.conns[p.Item.ID]
		if cn == nil || cn.ai == nil {
			continue
		}
		a.refreshMode(cn)
		if a.modeOf(cn) == policy.ModeFull {
			full = true
		}
		if cn.ai.running {
			running = true
		}
	}
	if running {
		ui.Icon(c, icons.Robot.SVG).Size(14, 14).Shrink(0).TextColor(tok.Accent).Label(m.AiRunning()).Tooltip(m.AiRunning())
	}
	if full {
		ui.Icon(c, icons.Warning.SVG).Size(14, 14).Shrink(0).TextColor(tok.Danger).Label(m.AiTabWarning()).Tooltip(m.AiTabWarning())
	}
}

func modeIndex(mode string) int {
	switch mode {
	case "review":
		return 1
	case "full":
		return 2
	default:
		return 0
	}
}

func riskText(m locales.Messages, s string) string {
	switch s {
	case "readonly":
		return m.AiRiskReadonly()
	case "low":
		return m.AiRiskLow()
	case "medium":
		return m.AiRiskMedium()
	case "high":
		return m.AiRiskHigh()
	case "critical":
		return m.AiRiskCritical()
	default:
		return m.AiRiskUnknown()
	}
}

func riskOf(m locales.Messages, r policy.Risk) string {
	switch r {
	case policy.RiskReadOnly:
		return m.AiRiskReadonly()
	case policy.RiskLow:
		return m.AiRiskLow()
	case policy.RiskMedium:
		return m.AiRiskMedium()
	case policy.RiskHigh:
		return m.AiRiskHigh()
	case policy.RiskCritical:
		return m.AiRiskCritical()
	default:
		return m.AiRiskUnknown()
	}
}

func verdictText(m locales.Messages, v string) string {
	switch v {
	case "full":
		return m.AiAutoFull()
	case "auto":
		return m.AiVerdictAuto()
	case "reviewer":
		return m.AiVerdictReviewer()
	case "user":
		return m.AiVerdictUser()
	case "timeout":
		return m.AiVerdictTimeout()
	case "cancel":
		return m.AiVerdictCancel()
	default:
		return m.AiVerdictReject()
	}
}

func toolOutput(m locales.Messages, s string) string {
	if s == "remote tools are not connected yet" {
		return m.AiToolsPending()
	}
	first, rest, multiline := strings.Cut(s, "\n")
	if first == "ai.command_output_limit" {
		text := m.AiCommandOutputLimit()
		if multiline {
			text += "\n" + rest
		}
		return text
	}
	return securityText(m, s)
}

func aiErrLine(m locales.Messages, s string) string {
	switch s {
	case "key":
		return m.AiNoKey()
	case "provider":
		return m.AiNoModel()
	default:
		return m.AiRequestFailed(toolOutput(m, s))
	}
}

// aiSheetStop is the stop shortcut from inside a sheet. The window
// shortcut is not delivered while a dialog is up, and Stop has to close
// that dialog.
func (a *App) aiSheetStop(c *ui.Context) {
	if c.Shortcut(stopMods, ui.KeyPeriod) {
		a.stopShortcut()
	}
	if c.Shortcut(stopAllMods, ui.KeyPeriod) {
		a.StopAllAI()
	}
}

func (a *App) aiSheets(c *ui.Context, m locales.Messages) {
	tok := currentTokens(c)
	a.consentSheet(c, m, tok)
	a.fullSheet(c, m, tok)
	a.askSheet(c, m, tok)
	a.providerSheet(c, m, tok)
	a.providerDelete(c, m)
}

func (a *App) consentSheet(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	if a.consent == nil {
		return
	}
	ask := a.consent
	sheet(ui.Modal(c.Key("aiconsent"), &ask.open, func() {
		a.aiSheetStop(c)
		ui.Column(c).Gap(14).Width(420).Children(func() {
			sheetTitle(c, m.AiConsentTitle())
			ui.Text(c, m.AiConsentBody()).FontSize(theme.Body).FillWidth()
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					ask.open = false
				}
				if pill(c, m.AiConsentConfirm(), true, false, false) {
					a.grantConsent(ask.host)
					id, text := ask.conn, ask.text
					ask.open = false
					a.consent = nil
					a.SendAI(id, text)
				}
			})
		})
	}), tok)
	if a.consent == ask && !ask.open {
		a.consent = nil
	}
}

func (a *App) fullSheet(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	f := a.full
	if f == nil {
		return
	}
	sheet(ui.Modal(c.Key("aifull"), &f.open, func() {
		a.aiSheetStop(c)
		ui.Column(c).Gap(14).Width(440).Children(func() {
			sheetTitle(c, m.AiFullTitle())
			ui.Text(c, m.AiFullBody()).FontSize(theme.Body).FillWidth()
			box := ui.Checkbox(c.Key("aifullcheck"), &f.tick, m.AiFullCheck())
			a.fullHold.apply(c, f, box)
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					f.open = false
				}
				if pill(c, m.AiFullConfirm(), false, true, !f.tick) {
					a.applyFull()
					f.open = false
				}
			})
		})
	}), tok)
	if a.full == f && !f.open {
		a.full = nil
	}
}

func (a *App) askSheet(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	cn := a.pendingAI()
	if cn == nil {
		a.askOpen, a.askFor = false, 0
		return
	}
	if a.askFor != cn.id {
		a.askFor = cn.id
		a.askOpen = true
	}
	if !a.askOpen {
		a.answerAI(cn, policy.Reject)
		a.askFor = 0
		return
	}
	pr := cn.ai.pending.prompt
	sheet(ui.Modal(c.Key("aiask"), &a.askOpen, func() {
		a.aiSheetStop(c)
		ui.Column(c).Gap(12).Width(520).Children(func() {
			where := m.AiWhereRemote(cn.host.Label())
			if pr.Action.Where == policy.Local {
				where = m.AiWhereLocal()
			}
			cat := string(pr.Decision.Category)
			if cat == "" {
				cat = pr.Action.Tool
			}
			title := ui.Text(c, where+" · "+cat+" · "+riskOf(m, pr.Decision.Risk)).FontSize(theme.Title3).FontWeight(600)
			if pr.Decision.Red {
				title.TextColor(tok.Danger)
			}
			if pr.Action.Command != "" {
				ui.Text(c, pr.Action.Command).Font("monospace").FontSize(theme.Footnote).FillWidth()
			}
			if pr.Action.Dir != "" {
				ui.Text(c, m.AiDir()+": "+pr.Action.Dir).FontSize(theme.Caption).TextColor(tok.TextMuted)
			}
			if pr.Diff != "" {
				ui.Checkbox(c.Key("onlydiff"), &cn.ai.onlyDiff, m.AiOnlyChanges())
				diff := pr.Diff
				if cn.ai.onlyDiff {
					diff = agent.OnlyChanges(diff)
				}
				ui.Scroll(c.Key("diff")).Height(220).FillWidth().Children(func() {
					ui.Text(c, diff).Font("monospace").FontSize(theme.Caption).FillWidth()
				})
			} else if pr.Action.Path != "" {
				ui.Text(c, pr.Action.Path).Font("monospace").FontSize(theme.Footnote).FillWidth()
			}
			if pr.Action.Reason != "" {
				ui.Text(c, m.AiReason()+": "+pr.Action.Reason).FontSize(theme.Footnote).FillWidth()
			}
			if pr.ReviewNote != "" {
				ui.Text(c, m.AiReviewNote()+": "+pr.ReviewNote).FontSize(theme.Footnote).TextColor(tok.TextMuted).FillWidth()
			}
			ui.Row(c).Gap(8).Children(func() {
				if pill(c, m.AiAllowOnce(), false, pr.Decision.Red, false) {
					a.answerAI(cn, policy.AllowOnce)
				}
				if pr.Decision.Similar {
					if pill(c, m.AiAllowSimilar(), false, pr.Decision.Red, false) {
						a.answerAI(cn, policy.AllowSimilar)
					}
				}
				ui.Spacer(c)
				el, ok := pillEl(c, m.AiReject(), false, true, false)
				a.aiHold.apply(c, cn.ai.pending, el)
				if ok {
					a.answerAI(cn, policy.Reject)
				}
			})
		})
	}), tok)
	if !a.askOpen && cn.ai != nil && cn.ai.pending != nil {
		a.answerAI(cn, policy.Reject)
		a.askFor = 0
	}
}
