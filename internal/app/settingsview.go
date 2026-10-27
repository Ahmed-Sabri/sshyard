package app

import (
	"context"
	"strconv"

	"github.com/Licoy/sshyard/internal/fonts"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/updater"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// languageNames are the languages offered, by their own names.
var languageNames = map[string]string{"en": "English", "zh-Hans": "简体中文"}

// updateText says where the update client is.
func updateText(m locales.Messages, st updater.Status) string {
	switch st.State {
	case updater.Disabled:
		return m.UpdatesDisabled()
	case updater.Checking:
		return m.Checking()
	case updater.UpToDate:
		return m.UpToDate()
	case updater.Available:
		return m.UpdateAvailable(st.Version)
	case updater.Downloading:
		return m.Downloading(strconv.Itoa(st.Percent()))
	case updater.Ready:
		return m.UpdateReady()
	case updater.Failed:
		return m.UpdateFailed(errString(st.Err))
	}
	return ""
}

// settingsGroup is one group of settings, as in System Settings: a tinted
// tile and a title over a card of rows split by hairlines.
type settingsGroup struct {
	c    *ui.Context
	tok  theme.Tokens
	rows int
}

// row adds one setting: its label (and a muted note) on the left, its
// control on the right.
func (g *settingsGroup) row(label, note string, control func()) {
	c, tok := g.c, g.tok
	if g.rows > 0 {
		ui.Box(c).FillWidth().Padding(0, 0, 0, 14).Children(func() { ui.Box(c).Height(1).FillWidth().Background(tok.Line) })
	}
	g.rows++
	ui.Row(c).MinHeight(44).Padding(8, 14).Gap(16).AlignItems(ui.Center).FillWidth().Children(func() {
		ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
			ui.Text(c, label).FontSize(theme.Body)
			if note != "" {
				ui.Text(c, note).FontSize(theme.Caption).TextColor(tok.TextMuted)
			}
		})
		ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center).Children(control)
	})
}

// divider is the hairline between rows, before what is not a row.
func (g *settingsGroup) divider() {
	c, tok := g.c, g.tok
	ui.Box(c).FillWidth().Padding(0, 0, 0, 14).Children(func() { ui.Box(c).Height(1).FillWidth().Background(tok.Line) })
}

func settingsSection(c *ui.Context, tok theme.Tokens, ic *icons.Icon, tint ui.Color, title string, fn func(g *settingsGroup)) {
	ui.Column(c).Gap(8).FillWidth().Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 4).Children(func() {
			tile(c, ic, tint, 20)
			ui.Text(c, title).FontSize(theme.Body).FontWeight(600)
		})
		card(c, tok).FillWidth().Children(func() {
			fn(&settingsGroup{c: c, tok: tok})
		})
	})
}

func (a *App) settingsPage(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	s := a.settings
	changed := false
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).Padding(28, 32).Gap(24).MaxWidth(640).FillWidth().Children(func() {
			ui.Text(c, m.Settings()).FontWeight(700).FontSize(theme.Title1)
			settingsSection(c, tok, icons.Appearance, theme.Brand, m.Appearance(), func(g *settingsGroup) {
				g.row(m.Theme(), "", func() {
					a.themeIdx = 0
					for i, md := range theme.Modes {
						if md == s.Theme {
							a.themeIdx = i
						}
					}
					if ui.Segmented(c.Key("theme"), &a.themeIdx, m.ThemeAuto(), m.ThemeLight(), m.ThemeDark()).Label(m.Appearance()).Changed() {
						s.Theme = theme.Modes[a.themeIdx]
						changed = true
					}
				})
				g.row(m.Language(), "", func() {
					opts := []string{m.LanguageAuto()}
					for _, l := range settings.Languages {
						opts = append(opts, languageNames[l])
					}
					cur := opts[0]
					if n, ok := languageNames[s.Language]; ok {
						cur = n
					}
					if ui.Select(c.Key("lang"), &cur, opts).Label(m.Language()).Width(180).Changed() {
						s.Language = ""
						for _, l := range settings.Languages {
							if languageNames[l] == cur {
								s.Language = l
							}
						}
						changed = true
					}
				})
				g.row(m.InterfaceFont(), "", func() {
					if f, ok := a.fontPicker(c, "uifont", m.InterfaceFont(), m.SystemDefault(), &a.fonts.uiSel, s.UIFont, a.fonts.list); ok {
						s.UIFont, changed = f, true
					}
				})
				g.divider()
				a.uiFontPreview(c, m, tok)
			})
			settingsSection(c, tok, icons.Terminal, tok.TintAt(0), m.TerminalSection(), func(g *settingsGroup) {
				g.row(m.TerminalFont(), "", func() {
					if f, ok := a.fontPicker(c, "termfont", m.TerminalFont(), m.SystemDefault(), &a.fonts.termSel, s.TerminalFont, fonts.ForTerminal(a.fonts.list, s.MonospacedOnly)); ok {
						s.TerminalFont, changed = f, true
					}
				})
				g.row(m.MonospacedOnly(), m.MonospacedOnlyNote(), func() {
					mono := s.MonospacedOnly
					if ui.Switch(c.Key("monoonly"), &mono).Label(m.MonospacedOnly()).Changed() {
						s.MonospacedOnly, changed = mono, true
					}
				})
				g.row(m.FontSize(), "", func() {
					if ui.NumberInput(c.Key("font"), &a.fontChoice, 8, 32, 1).Width(76).Margin(0, 54, 0, 0).Changed() {
						s.TerminalFontSize = int(a.fontChoice)
						changed = true
					}
				})
				a.termFontPreview(c, tok)
				g.row(m.KeepAlive(), "", func() {
					if ui.NumberInput(c.Key("keep"), &a.keepChoice, 0, 600, 5).Width(76).Margin(0, 54, 0, 0).Changed() {
						s.KeepAliveSeconds = int(a.keepChoice)
						changed = true
					}
				})
				g.row(m.AutoReconnect(), "", func() {
					a.autoRecon = s.AutoReconnect
					if ui.Switch(c.Key("recon"), &a.autoRecon).Label(m.AutoReconnect()).Changed() {
						s.AutoReconnect = a.autoRecon
						changed = true
					}
				})
				g.row(m.Osc52Title(), m.Osc52Note(), func() {
					codes := []string{settings.OSC52Allow, settings.OSC52Ask, settings.OSC52Deny}
					a.osc52Choice = 0
					for i, code := range codes {
						if s.OSC52Write == code {
							a.osc52Choice = i
						}
					}
					if ui.Segmented(c.Key("osc52"), &a.osc52Choice, m.Osc52Allow(), m.Osc52Ask(), m.Osc52Deny()).Label(m.Osc52Title()).Changed() {
						if a.osc52Choice >= 0 && a.osc52Choice < len(codes) {
							s.OSC52Write = codes[a.osc52Choice]
							changed = true
						}
					}
				})
				g.row(m.Osc52Limit(), m.Osc52LimitHint(), func() {
					if ui.NumberInput(c.Key("osc52max"), &a.osc52KiB, 64, 8192, 64).Width(90).Margin(0, 54, 0, 0).Changed() {
						s.OSC52MaxBytes = int(a.osc52KiB) << 10
						changed = true
					}
				})
				g.row(m.MetricsBar(), m.MetricsBarNote(), func() {
					a.metOn = s.MetricsEnabled
					if ui.Switch(c.Key("meton"), &a.metOn).Label(m.MetricsBar()).Changed() {
						s.MetricsEnabled = a.metOn
						changed = true
					}
				})
				g.row(m.MetricsInterval(), "", func() {
					a.metEvery = intervalIndex(s.MetricsInterval)
					if ui.Segmented(c.Key("metevery"), &a.metEvery, m.Seconds("1"), m.Seconds("2"), m.Seconds("5"), m.Seconds("10")).Label(m.MetricsInterval()).Changed() {
						s.MetricsInterval = []int{1, 2, 5, 10}[a.metEvery]
						changed = true
					}
				})
				g.row(m.MetricsMount(), m.MetricsMountNote(), func() {
					in := ui.TextInput(c.Key("metmount"), &a.metMount).Label(m.MetricsMount())
					focused := in.Focused()
					// Enter, or the caret leaving the field. Typing restarts
					// the remote command, so it waits for one of those.
					if in.Submitted() || (a.metMountFocus && !focused) {
						if s.MetricsMount != a.metMount {
							s.MetricsMount = a.metMount
							changed = true
						}
					}
					a.metMountFocus = focused
				})
			})
			settingsSection(c, tok, icons.Folder, tok.TintAt(2), m.FilesSection(), func(g *settingsGroup) {
				g.row(m.TransferConcurrency(), m.TransferConcurrencyHint(), func() {
					if ui.NumberInput(c.Key("conc"), &a.concChoice, 1, 10, 1).Width(76).Margin(0, 54, 0, 0).Changed() {
						s.TransferConcurrency = int(a.concChoice)
						changed = true
					}
				})
				g.row(m.OpenConfirmMB(), m.OpenConfirmHint(), func() {
					if ui.NumberInput(c.Key("openmb"), &a.openMBChoice, 1, 1024, 1).Width(76).Margin(0, 54, 0, 0).Changed() {
						s.OpenConfirmMB = int(a.openMBChoice)
						changed = true
					}
				})
			})
			a.aiSettings(c, m, tok, &s, &changed)
			a.credentialsSection(c, m, tok)
			a.proxiesSection(c, m, tok)
			settingsSection(c, tok, icons.Download, tok.TintAt(1), m.Updates(), func(g *settingsGroup) {
				st := a.upd.Status()
				v := a.p.Version
				if v == "" {
					v = "dev"
				}
				g.row(m.AppName(), m.CurrentVersion(v), func() {
					busy := st.State == updater.Checking || st.State == updater.Downloading
					if pill(c, m.CheckForUpdates(), false, false, busy) {
						go a.upd.Check(context.Background())
					}
				})
				g.row(m.CheckAutomatically(), "", func() {
					a.updateCheck = s.CheckUpdates
					if ui.Switch(c.Key("autoupd"), &a.updateCheck).Label(m.CheckAutomatically()).Changed() {
						s.CheckUpdates = a.updateCheck
						changed = true
					}
				})
				if txt := updateText(m, st); txt != "" {
					g.row(txt, "", func() {
						if st.State == updater.Downloading {
							ui.Progress(c, float64(st.Percent())/100).Width(180)
						}
						if st.State == updater.Available && pill(c, m.InstallUpdate(), true, false, false) {
							go a.upd.Install(context.Background())
						}
						if st.State == updater.Ready && pill(c, m.Relaunch(), true, false, false) {
							a.Close()
							a.upd.Relaunch()
						}
					})
				}
			})
		})
	})
	if changed {
		if err := a.ApplySettings(s); err != nil {
			c.AddToast(ui.Toast{Title: m.ErrGeneric(err.Error()), Type: "error"})
		}
	}
}
