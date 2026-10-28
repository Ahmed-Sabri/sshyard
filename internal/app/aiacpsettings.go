package app

import (
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

type acpForm struct {
	command, args, err string
	hold               focusHold
}

func (a *App) openACP() {
	a.acpForm = &acpForm{command: a.settings.AIACP.Command, args: strings.Join(a.settings.AIACP.Args, "\n")}
	a.acpOpen = true
}

func (a *App) saveACP(m locales.Messages) bool {
	f := a.acpForm
	command := strings.TrimSpace(f.command)
	if command == "" || strings.ContainsRune(command, 0) || strings.ContainsRune(f.args, 0) {
		f.err = m.AiACPCommandRequired()
		return false
	}
	var args []string
	if f.args != "" {
		args = strings.Split(strings.ReplaceAll(f.args, "\r\n", "\n"), "\n")
	}
	s := a.settings
	s.AIACP = settings.ACPConfig{Command: command, Args: args}
	if err := a.ApplySettings(s); err != nil {
		f.err = m.AiSettingsSaveFailed()
		return false
	}
	a.acpOpen = false
	return true
}

func (a *App) acpSheet(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	if a.acpForm == nil {
		return
	}
	f := a.acpForm
	sheet(ui.Modal(c.Key("acpconfig"), &a.acpOpen, func() {
		ui.Column(c).Gap(theme.Space(3)).Width(440).Children(func() {
			sheetTitle(c, m.AiACPConfigure())
			ui.Text(c, m.AiACPConfigNote()).FontSize(theme.Footnote).FillWidth().TextColor(tok.TextMuted)
			ui.Field(c, m.AiACPCommand(), func() {
				in := ui.TextInput(c.Key("acpcommand"), &f.command).Label(m.AiACPCommand()).AutoFocus()
				f.hold.apply(c, f, in)
			})
			ui.Field(c, m.AiACPArgs(), func() { ui.TextArea(c.Key("acpargs"), &f.args).Label(m.AiACPArgs()).FillWidth().Height(100) })
			ui.Text(c, m.AiACPArgsNote()).FontSize(theme.Caption).FillWidth().TextColor(tok.TextMuted)
			if f.err != "" {
				ui.Text(c, f.err).FontSize(theme.Footnote).TextColor(tok.Danger)
			}
			ui.Row(c).Gap(theme.Space(2)).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					a.acpOpen = false
				}
				if pill(c, m.Save(), true, false, false) {
					a.saveACP(m)
				}
			})
		})
	}), tok)
	if !a.acpOpen {
		a.acpForm = nil
	}
}

func (a *App) agentChip(c *ui.Context, cn *conn, tok theme.Tokens, m locales.Messages) {
	label := m.AiBackendUnavailable()
	tint := tok.Warning
	if cn.ai.agent == "builtin" {
		label = m.AiBuiltin()
		tint = ui.Color{}
	}
	if cn.ai.agent == "acp" {
		label = m.AiACP()
		tint = ui.Color{}
	}
	a.menuChip(c, "aiagent:"+strconv.Itoa(cn.id), chip{Icon: icons.Robot, Label: label, Tip: m.AiBackend(), Tint: tint}, func() []menuEntry {
		return []menuEntry{
			{Icon: icons.Robot, Label: m.AiBuiltin(), Checked: cn.ai.agent == "builtin", Do: func() { a.setAIAgent(cn, "builtin") }},
			{Icon: icons.Robot, Label: m.AiACP(), Checked: cn.ai.agent == "acp", Do: func() { a.setAIAgent(cn, "acp") }},
			menuSep,
			{Icon: icons.Settings, Label: m.AiACPConfigure(), Do: a.openACP},
		}
	})
}
