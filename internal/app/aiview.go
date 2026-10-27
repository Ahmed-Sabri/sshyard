package app

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/ai/policy"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// aiPane is an AI pane: its header, with the model it asks, the timeline
// of the conversation, and the composer, with the permission it runs under.
// It works with the terminal of its host in its tab (syncLink).
func (a *App) aiPane(c *ui.Context, t *layout.Tab, n *layout.Node, cn *conn, tok theme.Tokens, m locales.Messages) {
	a.refreshMode(cn)
	a.syncLink(t, cn)
	if t.Focus == n.ID {
		a.aiRecent[cn.host.ID] = cn.id
	}
	if cn.ai.md == nil {
		cn.ai.md = a.aiMarkdown(cn)
	}
	head := ui.Column(c.Key("aihead")).Shrink(0).FillWidth()
	head.Children(func() {
		row := ui.Row(c.Key("head")).Height(paneHeadHeight).Shrink(0).Padding(0, 6, 0, 10).Gap(8).AlignItems(ui.Center).Drag(dragSession{cn.id}).Label(m.PaneActions())
		row.Children(func() {
			softTile(c, kindIcon(layout.AI, t.Focus == n.ID), hostTint(tok, a.liveHost(cn.host)), paneHeadIcon)
			title := ui.Text(c, m.AiPane(a.pv(cn.host.Label()))).FontWeight(600).FontSize(theme.Footnote).SingleLine().Shrink(1).MinWidth(0)
			if t.Focus != n.ID && len(t.Panes()) > 1 {
				title.TextColor(tok.TextMuted)
			}
			if isTemp(cn.host.ID) {
				tempBadge(c, m, tok)
			}
			ui.Spacer(c).Shrink(1)
			a.providerChip(c, cn, tok, m)
			if (IconButton{Icon: icons.SplitRight, Label: m.SplitRight(), Size: Tiny}).Clicked(c, "splitright") {
				t.Focus = n.ID
				a.SplitFocused(layout.Right)
			}
			if (IconButton{Icon: icons.SplitDown, Label: m.SplitDown(), Size: Tiny}).Clicked(c, "splitdown") {
				t.Focus = n.ID
				a.SplitFocused(layout.Bottom)
			}
			if (IconButton{Icon: icons.Close, Label: m.ClosePane(), Size: Tiny, Danger: true}).Clicked(c, "close") {
				a.closeSoon(cn.id)
			}
		})
		if row.Clicked() {
			t.Focus = n.ID
			a.focusReq = cn.id
			a.aiRecent[cn.host.ID] = cn.id
		}
		ui.Box(c).Height(1).Shrink(0).FillWidth().Background(tok.Line)
	})
	a.aiTimeline(c, cn, tok, m)
	a.aiComposer(c, cn, tok, m)
}

// providerName is the short name of a provider: its name, else its model.
func providerName(p settings.AIProvider) string {
	switch {
	case p.Name != "":
		return p.Name
	case p.Model != "":
		return p.Model
	}
	return p.ID
}

// providerChip shows the pane's provider in its header, and chooses
// another from a menu. Without one it shows in the warning color.
func (a *App) providerChip(c *ui.Context, cn *conn, tok theme.Tokens, m locales.Messages) {
	ch := chip{Icon: icons.Model, Label: m.AiNoProvider(), Tip: m.AiProvider(), Tint: tok.Warning}
	cur := ""
	if p := a.paneProvider(cn); p != nil {
		ch.Label, ch.Tint, cur = providerName(*p), ui.Color{}, p.ID
	}
	a.menuChip(c, "aiprov:"+strconv.Itoa(cn.id), ch, func() []menuEntry {
		var out []menuEntry
		for _, p := range a.settings.AIProviders {
			id := p.ID
			e := menuEntry{Icon: icons.Model, Label: providerName(p), Checked: id == cur, Do: func() {
				cn.ai.provider = id
				a.saveLayout()
			}}
			if p.Model != "" && p.Model != e.Label {
				e.Note = p.Model
			}
			out = append(out, e)
		}
		if len(out) > 0 {
			out = append(out, menuSep)
		}
		return append(out, menuEntry{Icon: icons.Settings, Label: m.AiManageProviders(), Do: func() { a.openSettings(prefAI) }})
	})
}

// aiModes are the permissions a pane runs under, in the order its menu
// lists them, with their icons; modeTint colors each.
var aiModes = []struct {
	mode policy.Mode
	icon *icons.Icon
}{
	{policy.ModeAsk, icons.ModeAsk},
	{policy.ModeReview, icons.ModeReview},
	{policy.ModeFull, icons.ModeFull},
}

func modeTint(tok theme.Tokens, mode policy.Mode) ui.Color {
	switch mode {
	case policy.ModeFull:
		return tok.Danger
	case policy.ModeReview:
		return tok.Warning
	default:
		return tok.Success
	}
}

func modeName(m locales.Messages, mode policy.Mode) (name, note string) {
	switch mode {
	case policy.ModeFull:
		return m.AiModeFull(), m.AiModeFullNote()
	case policy.ModeReview:
		return m.AiModeReview(), m.AiModeReviewNote()
	default:
		return m.AiModeAsk(), m.AiModeAskNote()
	}
}

// modeChip shows the permission the pane runs under, in its color, and
// chooses another from a menu. Full access asks to be confirmed first.
func (a *App) modeChip(c *ui.Context, cn *conn, tok theme.Tokens, m locales.Messages) {
	cur := a.modeOf(cn)
	ch := chip{Icon: icons.ModeAsk, Tip: m.AiPermission(), Tint: modeTint(tok, cur)}
	for _, md := range aiModes {
		if md.mode == cur {
			ch.Icon = md.icon
		}
	}
	ch.Label, _ = modeName(m, cur)
	a.menuChip(c, "aimode:"+strconv.Itoa(cn.id), ch, func() []menuEntry {
		out := make([]menuEntry, 0, len(aiModes))
		for _, md := range aiModes {
			mode := md.mode
			name, note := modeName(m, mode)
			out = append(out, menuEntry{Icon: md.icon, Tint: modeTint(tok, mode), Label: name, Note: note,
				Checked: mode == cur, Danger: mode == policy.ModeFull, Do: func() { a.setPaneMode(cn, string(mode)) }})
		}
		return out
	})
}

// aiTimeline is the conversation, which follows its end as it grows
// unless the user scrolled up from it; before the first question, a word
// on what the pane does.
func (a *App) aiTimeline(c *ui.Context, cn *conn, tok theme.Tokens, m locales.Messages) {
	if len(cn.ai.items) == 0 && !cn.ai.running {
		ui.Column(c.Key("aiempty")).Grow(1).FillWidth().Padding(theme.Space(6)).Gap(theme.Space(2)).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			softTile(c, icons.Robot, tok.Accent, 44).Margin(0, 0, theme.Space(1), 0)
			ui.Text(c, m.AiEmptyTitle(a.pv(cn.host.Label()))).FontSize(theme.Title3).FontWeight(600).TextAlign(ui.Center).MaxWidth(320)
			ui.Text(c, m.AiEmpty()).FontSize(theme.Footnote).TextColor(tok.TextMuted).TextAlign(ui.Center).MaxWidth(300)
		})
		return
	}
	st := &cn.ai.scroll
	if st.Y >= st.MaxY-theme.Space(2) {
		st.Y = math.MaxFloat32
	}
	ui.Scroll(c.Key("aitime")).TrackScroll(st).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Padding(theme.Space(3), theme.Space(3), theme.Space(4), theme.Space(3)).Gap(theme.Space(3)).FillWidth().Children(func() {
			for i := range cn.ai.items {
				a.aiCard(c, cn, i, tok, m)
			}
			n := len(cn.ai.items)
			if cn.ai.running && (n == 0 || cn.ai.items[n-1].kind != "text") {
				ui.Row(c.Key("aiworking")).Gap(theme.Space(2)).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Label(m.AiWorking())
					ui.Text(c, m.AiWorking()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
				})
			}
			if cn.ai.paused && !cn.ai.running {
				ui.Row(c.Key("aipaused")).Gap(theme.Space(2)).AlignItems(ui.Center).FillWidth().Children(func() {
					ui.Text(c, m.AiPaused()).FontSize(theme.Footnote).TextColor(tok.TextMuted).Shrink(1)
					if pill(c, m.AiContinue(), true, false, false) {
						a.ContinueAI(cn.id)
					}
				})
			}
		})
	})
}

// aiComposer is the question being written, with the permission before it
// and the send button after it, which stops the turn while one runs. Full
// access shows a banner above it.
func (a *App) aiComposer(c *ui.Context, cn *conn, tok theme.Tokens, m locales.Messages) {
	ui.Column(c.Key("aifoot")).Shrink(0).FillWidth().Padding(0, theme.Space(3), theme.Space(3), theme.Space(3)).Gap(theme.Space(2)).Children(func() {
		if a.modeOf(cn) == policy.ModeFull {
			aiBanner(c.Key("fullbar"), tok.Danger, icons.Warning, m.AiFullBar())
		}
		box := ui.Row(c.Key("aicompose")).FillWidth().Padding(4).Gap(theme.Space(1)).AlignItems(ui.Center).
			Radius(theme.RadiusCard).Background(tok.Fill)
		edge := tok.Line
		if box.FocusWithin() {
			edge = tok.Focus
		}
		box.Border(1, edge).Children(func() {
			a.modeChip(c, cn, tok, m)
			in := ui.TextInputBase(c.Key("aiin:"+strconv.Itoa(cn.id)+":"+strconv.Itoa(cn.ai.gen)), &cn.ai.input).
				Placeholder(m.AiPlaceholder()).Label(m.AiPlaceholder()).FontSize(theme.Body).Padding(4, 6).Grow(1).Shrink(1).MinWidth(0)
			if a.focusReq == cn.id && a.page == pageWorkspace {
				in.Focus()
				a.focusReq = 0
			}
			if in.Submitted() {
				a.sendAI(cn)
			}
			if cn.ai.running {
				if (IconButton{Icon: icons.Stop, Label: m.StopAI(), Size: Small, Danger: true, Active: true}).Clicked(c, "aistop") {
					a.StopAI(cn.id)
				}
				return
			}
			empty := strings.TrimSpace(cn.ai.input) == ""
			if (IconButton{Icon: icons.Send, Label: m.AiSend(), Size: Small, Active: !empty, Disabled: empty}).Clicked(c, "aisend") {
				a.sendAI(cn)
			}
		})
	})
}

// sendAI sends the question written in the pane, and follows the reply.
func (a *App) sendAI(cn *conn) {
	if cn.ai.running || strings.TrimSpace(cn.ai.input) == "" {
		return
	}
	cn.ai.scroll.Y = math.MaxFloat32
	a.SendAI(cn.id, cn.ai.input)
}

// aiBanner is a rounded notice tinted with col, as the panes' banners are.
func aiBanner(c *ui.Context, col ui.Color, ic *icons.Icon, text string) ui.Element {
	return ui.Row(c).FillWidth().Padding(6, 10).Gap(theme.Space(2)).AlignItems(ui.Center).
		Radius(theme.RadiusControl).Background(col.Alpha(0.12)).Border(1, col.Alpha(0.22)).Children(func() {
		ui.Icon(c, ic.SVG).Size(14, 14).Shrink(0).TextColor(col)
		ui.Text(c, text).FontSize(theme.Footnote).Grow(1).Shrink(1)
	})
}

// shellLangs are the languages of code blocks that are commands to run,
// which a pane can insert into its terminal; a block that names none is
// taken for one.
var shellLangs = map[string]bool{"": true, "sh": true, "bash": true, "zsh": true, "shell": true, "console": true, "fish": true}

// aiMarkdown is how the pane shows Markdown: its code blocks in a box of
// its own, with buttons to copy them and, for a command of one line, to
// insert it into the terminal.
func (a *App) aiMarkdown(cn *conn) *markdown.Options {
	return &markdown.Options{CodeBlock: func(c *ui.Context, b *markdown.CodeBlockView) { a.aiCodeBlock(c, cn, b) }}
}

func (a *App) aiCodeBlock(c *ui.Context, cn *conn, b *markdown.CodeBlockView) {
	tok := currentTokens(c)
	m := locales.In(c.Locale())
	code := b.Code()
	ui.Column(c).Direction(ui.LeftToRight).FillWidth().Radius(theme.RadiusControl).Background(tok.Fill).Border(1, tok.Line).Clip().Children(func() {
		ui.Row(c).Padding(2, 4, 0, 10).Gap(2).AlignItems(ui.Center).Height(26).Children(func() {
			ui.Text(c, b.Language).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(1)
			ui.Spacer(c)
			a.codeActions(c, cn, code, shellLangs[b.Language], b.Open, m)
		})
		b.Body(c)
	})
}

// codeActions are the buttons on code: Copy, which shows a check for a
// moment once it has, and, for a command of one line, Insert into the
// pane's terminal, without Enter.
func (a *App) codeActions(c *ui.Context, cn *conn, code string, shell, open bool, m locales.Messages) {
	code = strings.TrimRight(code, "\n")
	ic := icons.Copy
	if cn.ai.copied == code && c.Now().Before(cn.ai.copiedTill) {
		ic = icons.Check
	}
	if (IconButton{Icon: ic, Label: m.Copy(), Size: Small, Disabled: open || code == ""}).Clicked(c, "copy") {
		c.WriteClipboard(code)
		cn.ai.copied, cn.ai.copiedTill = code, c.Now().Add(2*time.Second)
		c.After(2 * time.Second)
	}
	t := a.conns[cn.ai.link]
	if shell && !strings.Contains(code, "\n") && t != nil && t.tv != nil {
		if (IconButton{Icon: icons.Terminal, Label: m.AiInsert(), Size: Small, Disabled: open || code == ""}).Clicked(c, "insert") {
			a.InsertAI(cn.id, code)
		}
	}
}

func (a *App) aiCard(c *ui.Context, cn *conn, i int, tok theme.Tokens, m locales.Messages) {
	it := &cn.ai.items[i]
	key := c.Key("item:" + strconv.Itoa(i))
	switch it.kind {
	case "user":
		ui.Row(key).FillWidth().Justify(ui.End).Children(func() {
			strong := float32(0.12)
			if tok.Dark {
				strong = 0.22
			}
			ui.Column(c).Shrink(1).MinWidth(0).MarginStart(theme.Space(8)).Padding(7, 12).Radius(theme.RadiusCard).
				Background(tok.Accent.Alpha(strong)).Selectable().Children(func() {
				ui.Text(c, it.text).FontSize(theme.Body)
			})
		})
	case "text":
		ui.Column(key).FillWidth().Children(func() {
			if cn.ai.running && i == len(cn.ai.items)-1 {
				markdown.Stream(c, it.text, cn.ai.md).FillWidth()
			} else {
				markdown.View(c, it.text, cn.ai.md).FillWidth()
			}
		})
	case "think":
		ui.Column(key).FillWidth().Children(func() {
			ui.Collapsible(c.Key("think"), m.AiThinking(), &it.open, func() {
				ui.Row(c).Gap(theme.Space(2)).FillWidth().Children(func() {
					ui.Box(c).Width(2).Shrink(0).AlignSelf(ui.Stretch).Radius(1).Background(tok.Line)
					ui.Text(c, it.text).FontSize(theme.Footnote).TextColor(tok.TextMuted).Grow(1).Shrink(1)
				})
			})
		})
	case "err":
		aiBanner(key, tok.Danger, icons.Warning, aiErrLine(m, it.text))
	case "tool":
		a.toolCard(key, cn, it, tok, m)
	}
}

// riskTint is the color of a tool's risk: green to red.
func riskTint(tok theme.Tokens, s string) ui.Color {
	switch s {
	case "readonly", "low":
		return tok.Success
	case "medium":
		return tok.Warning
	case "high", "critical":
		return tok.Danger
	default:
		return tok.TextMuted
	}
}

// toolCard is a tool the agent called: what it is and its risk, where it
// ran and who allowed it, the command or path, and once it is done, its
// output, exit code and time.
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
	where := m.AiWhereRemote(a.pv(cn.host.Label()))
	if card.Where == string(policy.Local) {
		where = m.AiWhereLocal()
	}
	ic := icons.File
	if card.Command != "" {
		ic = icons.Terminal
	}
	risk := riskTint(tok, card.Risk)
	ui.Column(c).FillWidth().Radius(theme.RadiusControl).Background(tok.Fill).Border(1, tok.Line).Clip().Children(func() {
		ui.Row(c.Key("toolhead")).Padding(4, 4, 4, 10).Gap(theme.Space(2)).MinHeight(30).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Icon(c, ic.SVG).Size(14, 14).Shrink(0).TextColor(risk)
			ui.Text(c, title).FontWeight(600).FontSize(theme.Footnote).TextColor(muted).SingleLine().Shrink(1)
			ui.Box(c).Padding(1, 6).Radius(theme.Capsule).Background(risk.Alpha(0.12)).Shrink(0).Children(func() {
				ui.Text(c, riskText(m, card.Risk)).FontSize(theme.Caption).FontWeight(500).TextColor(risk).SingleLine()
			})
			ui.Spacer(c)
			if card.Phase != "done" {
				ui.Spinner(c).Label(m.AiWorking())
			} else {
				ui.Text(c, verdictText(m, card.Verdict)).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(0)
			}
			if card.Command != "" {
				a.codeActions(c, cn, card.Command, true, false, m)
			}
		})
		what := card.Command
		if what == "" {
			what = card.Path
		}
		if what != "" {
			ui.Box(c).FillWidth().Padding(6, 10).Background(tok.Fill).Selectable().Children(func() {
				ui.Text(c, what).Font("monospace").FontSize(theme.Footnote).TextColor(muted)
			})
		}
		if !card.Run && card.Refusal != "" {
			ui.Text(c, card.Refusal).FontSize(theme.Caption).TextColor(tok.TextMuted).Padding(6, 10, 0, 10)
		}
		if card.Output != "" && card.Phase == "done" {
			ui.Scroll(c.Key("out")).MaxHeight(220).FillWidth().Children(func() {
				ui.Box(c).FillWidth().Padding(6, 10).Selectable().Children(func() {
					ui.Text(c, toolOutput(m, card.Output)).Font("monospace").FontSize(theme.Caption).TextColor(muted)
				})
			})
		}
		ui.Row(c.Key("toolfoot")).Padding(4, 10, 6, 10).Gap(theme.Space(2)).FillWidth().Children(func() {
			ui.Text(c, where).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(1)
			if card.Phase == "done" && card.Run {
				ui.Spacer(c)
				if card.Exit >= 0 {
					exit := ui.Text(c, m.AiExit(strconv.Itoa(card.Exit))).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
					if card.Exit != 0 {
						exit.TextColor(tok.Danger)
					}
				}
				ui.Text(c, m.AiDuration(strconv.FormatInt(card.DurMS, 10))).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
			}
		})
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
