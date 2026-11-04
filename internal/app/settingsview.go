package app

import (
	"context"
	"runtime"
	"slices"
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

// repoURL is where SSHYard's source and issues live.
const repoURL = "https://github.com/Licoy/sshyard"

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

// prefPage is a page of the settings, chosen in their sidebar.
type prefPage string

const (
	prefAppearance  prefPage = "appearance"
	prefTerminal    prefPage = "terminal"
	prefMonitoring  prefPage = "monitoring"
	prefFiles       prefPage = "files"
	prefAI          prefPage = "ai"
	prefAgents      prefPage = "agents"
	prefCredentials prefPage = "credentials"
	prefProxies     prefPage = "proxies"
	prefBackup      prefPage = "backup"
	prefShortcuts   prefPage = "shortcuts"
	prefAbout       prefPage = "about"
)

// prefItem is one page in the settings' sidebar: its icon, tint, title
// and the line under the title on the page.
type prefItem struct {
	page        prefPage
	icon        *icons.Icon
	tint        ui.Color
	title, desc string
}

// prefGroup is a titled group of pages in the settings' sidebar.
type prefGroup struct {
	title string
	items []prefItem
}

func prefGroups(m locales.Messages, tok theme.Tokens) []prefGroup {
	return []prefGroup{
		{m.SettingsGroupPreferences(), []prefItem{
			{prefAppearance, icons.Appearance, theme.Brand, m.Appearance(), m.AppearanceDesc()},
			{prefTerminal, icons.Terminal, tok.TintAt(0), m.TerminalSection(), m.TerminalDesc()},
			{prefMonitoring, icons.Monitor, tok.TintAt(5), m.MonitoringSection(), m.MonitoringDesc()},
			{prefFiles, icons.Folder, tok.TintAt(2), m.FilesSection(), m.FilesDesc()},
			{prefAI, icons.Robot, tok.TintAt(3), m.AiSettings(), m.AiDesc()},
			{prefAgents, icons.MCP, tok.TintAt(5), m.AgentsPage(), m.AgentsDesc()},
		}},
		{m.SettingsGroupManage(), []prefItem{
			{prefCredentials, icons.Key, tok.TintAt(1), m.Credentials(), m.CredentialsDesc()},
			{prefProxies, icons.Proxy, tok.TintAt(4), m.Proxies(), m.ProxiesDesc()},
			{prefBackup, icons.Backup, tok.TintAt(6), m.BackupSection(), m.BackupDesc()},
		}},
		{m.SettingsGroupHelp(), []prefItem{
			{prefShortcuts, icons.Shortcuts, tok.TextMuted, m.ShortcutsSection(), m.ShortcutsDesc()},
			{prefAbout, icons.Info, tok.TintAt(7), m.AboutSection(), m.AboutDesc()},
		}},
	}
}

// prefNow is the settings page showing, the first one when none was
// chosen.
func (a *App) prefNow() prefPage {
	if a.pref == "" {
		return prefAppearance
	}
	return a.pref
}

// openSettings shows the settings on page p.
func (a *App) openSettings(p prefPage) {
	a.page, a.pref = pageSettings, p
}

// closeSettings goes back to the sessions, the focused terminal taking
// the keyboard again.
func (a *App) closeSettings() {
	a.page = pageWorkspace
	if cn := a.focused(); cn != nil {
		a.focusReq = cn.id
	}
}

// settingsNav is the sidebar while the settings show: the pages in
// groups, as System Settings lists them (the way back is in the title
// bar).
func (a *App) settingsNav(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	cur := a.prefNow()
	ui.Column(c).Fill().Children(func() {
		ui.Scroll(c.Key("setnav")).Grow(1).Shrink(1).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Padding(theme.Space(1), theme.Space(2.5), theme.Space(3), theme.Space(2.5)).Gap(2).Children(func() {
				for gi, g := range prefGroups(m, tok) {
					top := theme.Space(4)
					if gi == 0 {
						top = theme.Space(1)
					}
					ui.Text(c, g.title).FontSize(theme.Caption).FontWeight(600).TextColor(tok.TextMuted).
						Padding(top, theme.Space(2), theme.Space(1), theme.Space(2)).SingleLine()
					for _, it := range g.items {
						a.prefNavItem(c, tok, it, it.page == cur)
					}
				}
			})
		})
		v := a.p.Version
		if v == "" {
			v = "dev"
		}
		ui.Row(c).Shrink(0).Padding(theme.Space(2.5), theme.Space(4)).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.AppName()).FontSize(theme.Caption).FontWeight(600).TextColor(tok.TextMuted)
			ui.Text(c, m.CurrentVersion(v)).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
		})
	})
}

// prefNavItem is one page's row in the settings' sidebar: its tile and
// title, filled with a tint of the accent while it shows.
func (a *App) prefNavItem(c *ui.Context, tok theme.Tokens, it prefItem, on bool) {
	b := ui.ButtonBase(c.Key("pref:"+string(it.page))).Label(it.title).Selected(on).FillWidth().
		Padding(5, 8).Gap(10).Radius(theme.RadiusControl).AlignItems(ui.Center).Justify(ui.Start).Cursor(ui.CursorPointer)
	strong := float32(0.12)
	if tok.Dark {
		strong = 0.22
	}
	switch {
	case on:
		b.Background(tok.Accent.Alpha(strong))
	case b.Pressed():
		b.Background(tok.Press)
	case b.Hovered():
		b.Background(tok.Hover)
	}
	b.Children(func() {
		tile(c, it.icon, it.tint, 22)
		tx := ui.Text(c, it.title).FontSize(theme.Body).SingleLine().Shrink(1)
		if on {
			tx.FontWeight(600).TextColor(tok.Accent)
		}
	})
	if b.Clicked() {
		a.pref = it.page
	}
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
func (g *settingsGroup) row(label, note string, control func(), lead ...func()) {
	c, tok := g.c, g.tok
	if g.rows > 0 {
		ui.Box(c).FillWidth().Padding(0, 0, 0, 14).Children(func() { ui.Box(c).Height(1).FillWidth().Background(tok.Line) })
	}
	g.rows++
	ui.Row(c).MinHeight(48).Padding(8, 14).Gap(16).AlignItems(ui.Center).FillWidth().Children(func() {
		if len(lead) > 0 {
			lead[0]()
		}
		ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
			ui.Text(c, label).FontSize(theme.Body).FontWeight(500)
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

// settingsSection is a card of settings under a tile and a title; with
// no title, the card alone.
func settingsSection(c *ui.Context, tok theme.Tokens, ic *icons.Icon, tint ui.Color, title string, fn func(g *settingsGroup)) {
	ui.Column(c).Gap(8).FillWidth().Children(func() {
		if title != "" {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 4).Children(func() {
				tile(c, ic, tint, 20)
				ui.Text(c, title).FontSize(theme.Body).FontWeight(600)
			})
		}
		card(c, tok).FillWidth().Children(func() {
			fn(&settingsGroup{c: c, tok: tok})
		})
	})
}

func (a *App) settingsPage(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	if !a.unlocked() {
		a.secretsBanner(c, m, tok)
	}
	s := a.settings
	changed := false
	cur := a.prefNow()
	var item prefItem
	for _, g := range prefGroups(m, tok) {
		for _, it := range g.items {
			if it.page == cur {
				item = it
			}
		}
	}
	ui.Scroll(c.Key("prefpage:" + string(cur))).Fill().Children(func() {
		ui.Column(c).Padding(theme.Space(7), theme.Space(8), theme.Space(10), theme.Space(8)).Gap(theme.Space(6)).MaxWidth(720).FillWidth().Children(func() {
			ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
				tile(c, item.icon, item.tint, 40)
				ui.Column(c).Gap(3).Shrink(1).Children(func() {
					ui.Text(c, item.title).FontWeight(700).FontSize(theme.Title1)
					ui.Text(c, item.desc).FontSize(theme.Body).TextColor(tok.TextMuted)
				})
			})
			switch cur {
			case prefAppearance:
				a.prefAppearance(c, m, tok, &s, &changed)
			case prefTerminal:
				a.prefTerminal(c, m, tok, &s, &changed)
			case prefMonitoring:
				a.prefMonitoring(c, m, tok, &s, &changed)
			case prefFiles:
				a.prefFiles(c, m, tok, &s, &changed)
			case prefAI:
				a.aiSettings(c, m, tok, &s, &changed)
			case prefAgents:
				a.prefAgents(c, m, tok, &s, &changed)
			case prefCredentials:
				a.credentialsSection(c, m, tok)
			case prefProxies:
				a.proxiesSection(c, m, tok)
			case prefBackup:
				a.backupSection(c, m, tok)
			case prefShortcuts:
				shortcutsPage(c, m, tok)
			case prefAbout:
				a.aboutPage(c, m, tok, &s, &changed)
			}
		})
	})
	if changed {
		if err := a.ApplySettings(s); err != nil {
			c.AddToast(ui.Toast{Title: m.ErrGeneric(err.Error()), Type: "error"})
		}
	}
}

// themeSwitch chooses the appearance: a capsule track of the modes, each
// with its icon, the one chosen raised on it.
func (a *App) themeSwitch(c *ui.Context, m locales.Messages, tok theme.Tokens) (theme.Mode, bool) {
	a.themeIdx = 0
	for i, md := range theme.Modes {
		if md == a.settings.Theme {
			a.themeIdx = i
		}
	}
	type seg struct {
		icon  *icons.Icon
		label string
	}
	segs := []seg{{icons.LocalDisk, m.ThemeAuto()}, {icons.Light, m.ThemeLight()}, {icons.Dark, m.ThemeDark()}}
	parts := ui.SegmentedBase(c.Key("theme"), &a.themeIdx, len(segs))
	parts.Track.Label(m.Appearance()).Padding(2).Gap(2).Radius(theme.Capsule).Background(tok.Fill).Children(func() {
		for i, sg := range segs {
			el := parts.Segment(i).Padding(4, 12).Gap(6).Radius(theme.Capsule).AlignItems(ui.Center).Cursor(ui.CursorPointer)
			on := i == a.themeIdx
			col := tok.TextMuted
			switch {
			case on:
				theme.Shadow(tok.Card).On(el.Background(tok.Inset))
				col = tok.Text
			case el.Hovered():
				el.Background(tok.Hover)
			}
			el.Children(func() {
				ui.Icon(c, sg.icon.SVG).Size(14, 14).TextColor(col)
				ui.Text(c, sg.label).FontSize(theme.Footnote).FontWeight(500).TextColor(col).SingleLine()
			})
		}
	})
	if !parts.Track.Changed() {
		return a.settings.Theme, false
	}
	return theme.Modes[a.themeIdx], true
}

func (a *App) prefAppearance(c *ui.Context, m locales.Messages, tok theme.Tokens, s *settings.Settings, changed *bool) {
	settingsSection(c, tok, nil, ui.Color{}, "", func(g *settingsGroup) {
		g.row(m.Theme(), "", func() {
			if md, ok := a.themeSwitch(c, m, tok); ok {
				s.Theme, *changed = md, true
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
				*changed = true
			}
		})
	})
	a.materialSection(c, m, tok, s, changed)
	settingsSection(c, tok, icons.Edit, tok.TintAt(4), m.FontsSection(), func(g *settingsGroup) {
		g.row(m.InterfaceFont(), "", func() {
			if f, ok := a.fontPicker(c, "uifont", m.InterfaceFont(), m.SystemDefault(), &a.fonts.uiSel, s.UIFont, a.fonts.list); ok {
				s.UIFont, *changed = f, true
			}
		})
		g.divider()
		a.uiFontPreview(c, m, tok)
	})
}

// materials are the materials a part of the window may have, as the
// segments of its choice show them.
var materials = []string{settings.MaterialTranslucent, settings.MaterialOpaque}

// materialSection chooses how the sidebar with the title bar, and the
// content, show the window's material: translucent or opaque, each on its
// own. Without a material (Linux, Windows) the choice cannot be made.
func (a *App) materialSection(c *ui.Context, m locales.Messages, tok theme.Tokens, s *settings.Settings, changed *bool) {
	off := !c.Vibrancy()
	settingsSection(c, tok, icons.Appearance, tok.TintAt(2), m.MaterialSection(), func(g *settingsGroup) {
		choose := func(key, label, note string, idx *int, cur *string) {
			*idx = max(slices.Index(materials, *cur), 0)
			if off {
				note = m.MaterialUnsupported()
			}
			g.row(label, note, func() {
				seg := ui.Segmented(c.Key(key), idx, m.MaterialTranslucent(), m.MaterialOpaque()).Label(label).Disabled(off)
				if seg.Changed() && !off {
					*cur, *changed = materials[*idx], true
				}
			})
		}
		choose("matchrome", m.MaterialChrome(), "", &a.chromeMat, &s.ChromeMaterial)
		choose("matcontent", m.MaterialContent(), m.MaterialContentNote(), &a.contentMat, &s.ContentMaterial)
	})
}

func (a *App) prefTerminal(c *ui.Context, m locales.Messages, tok theme.Tokens, s *settings.Settings, changed *bool) {
	settingsSection(c, tok, icons.Edit, tok.TintAt(4), m.FontsSection(), func(g *settingsGroup) {
		g.row(m.TerminalFont(), "", func() {
			if f, ok := a.fontPicker(c, "termfont", m.TerminalFont(), m.SystemDefault(), &a.fonts.termSel, s.TerminalFont, fonts.ForTerminal(a.fonts.list, s.MonospacedOnly)); ok {
				s.TerminalFont, *changed = f, true
			}
		})
		g.row(m.MonospacedOnly(), m.MonospacedOnlyNote(), func() {
			mono := s.MonospacedOnly
			if ui.Switch(c.Key("monoonly"), &mono).Label(m.MonospacedOnly()).Changed() {
				s.MonospacedOnly, *changed = mono, true
			}
		})
		g.row(m.FontSize(), "", func() {
			if ui.NumberInput(c.Key("font"), &a.fontChoice, 8, 32, 1).Changed() {
				s.TerminalFontSize = int(a.fontChoice)
				*changed = true
			}
		})
		a.termFontPreview(c, tok)
	})
	settingsSection(c, tok, icons.Reconnect, tok.TintAt(0), m.ConnectionSection(), func(g *settingsGroup) {
		g.row(m.KeepAlive(), "", func() {
			if ui.NumberInput(c.Key("keep"), &a.keepChoice, 0, 600, 5).Changed() {
				s.KeepAliveSeconds = int(a.keepChoice)
				*changed = true
			}
		})
		g.row(m.AutoReconnect(), "", func() {
			a.autoRecon = s.AutoReconnect
			if ui.Switch(c.Key("recon"), &a.autoRecon).Label(m.AutoReconnect()).Changed() {
				s.AutoReconnect = a.autoRecon
				*changed = true
			}
		})
	})
	settingsSection(c, tok, icons.Paste, tok.TintAt(1), m.ClipboardSection(), func(g *settingsGroup) {
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
					*changed = true
				}
			}
		})
		g.row(m.Osc52Limit(), m.Osc52LimitHint(), func() {
			if ui.NumberInput(c.Key("osc52max"), &a.osc52KiB, 64, 8192, 64).Changed() {
				s.OSC52MaxBytes = int(a.osc52KiB) << 10
				*changed = true
			}
		})
	})
}

func (a *App) prefMonitoring(c *ui.Context, m locales.Messages, tok theme.Tokens, s *settings.Settings, changed *bool) {
	settingsSection(c, tok, nil, ui.Color{}, "", func(g *settingsGroup) {
		g.row(m.MetricsBar(), m.MetricsBarNote(), func() {
			a.metOn = s.MetricsEnabled
			if ui.Switch(c.Key("meton"), &a.metOn).Label(m.MetricsBar()).Changed() {
				s.MetricsEnabled = a.metOn
				*changed = true
			}
		})
		g.row(m.MetricsInterval(), "", func() {
			a.metEvery = intervalIndex(s.MetricsInterval)
			if ui.Segmented(c.Key("metevery"), &a.metEvery, m.Seconds("1"), m.Seconds("2"), m.Seconds("5"), m.Seconds("10")).Label(m.MetricsInterval()).Changed() {
				s.MetricsInterval = []int{1, 2, 5, 10}[a.metEvery]
				*changed = true
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
					*changed = true
				}
			}
			a.metMountFocus = focused
		})
	})
}

func (a *App) prefFiles(c *ui.Context, m locales.Messages, tok theme.Tokens, s *settings.Settings, changed *bool) {
	settingsSection(c, tok, nil, ui.Color{}, "", func(g *settingsGroup) {
		g.row(m.TransferConcurrency(), m.TransferConcurrencyHint(), func() {
			if ui.NumberInput(c.Key("conc"), &a.concChoice, 1, 10, 1).Changed() {
				s.TransferConcurrency = int(a.concChoice)
				*changed = true
			}
		})
		g.row(m.OpenConfirmMB(), m.OpenConfirmHint(), func() {
			if ui.NumberInput(c.Key("openmb"), &a.openMBChoice, 1, 1024, 1).Changed() {
				s.OpenConfirmMB = int(a.openMBChoice)
				*changed = true
			}
		})
	})
}

// shortcut is one of the app's keyboard shortcuts, as the shortcuts page
// lists it.
type shortcut struct {
	label string
	mods  ui.Modifiers
	key   ui.Key
}

func appShortcuts(m locales.Messages) []shortcut {
	return []shortcut{
		{m.QuickConnect(), appMods, ui.KeyK},
		{m.NewTab(), appMods, ui.KeyT},
		{m.ClosePane(), appMods, ui.KeyW},
		{m.SplitRight(), appMods, ui.KeyD},
		{m.SplitDown(), appMods, ui.KeyE},
		{m.OpenFilesBeside(), appMods, ui.KeyO},
		{m.Find(), appMods, ui.KeyF},
		{m.OpenAIRight(), aiMods, ui.KeyA},
		{m.ShortcutStopAI(), stopMods, ui.KeyPeriod},
		{m.StopAllAI(), stopAllMods, ui.KeyPeriod},
		{m.ShortcutSaveFile(), saveMods, ui.KeyS},
	}
}

// keyCaps are the caps of a shortcut: the modifiers as macOS draws them
// there, by name elsewhere, then the key.
func keyCaps(goos string, mods ui.Modifiers, key ui.Key) []string {
	var out []string
	mac := goos == "darwin"
	for _, md := range []struct {
		mod       ui.Modifiers
		mac, name string
	}{{ui.Ctrl, "⌃", "Ctrl"}, {ui.Alt, "⌥", "Alt"}, {ui.Shift, "⇧", "Shift"}, {ui.Super, "⌘", "Super"}} {
		if mods&md.mod == 0 {
			continue
		}
		if mac {
			out = append(out, md.mac)
		} else {
			out = append(out, md.name)
		}
	}
	switch {
	case key >= ui.KeyA && key <= ui.KeyZ:
		out = append(out, string(rune('A'+key-ui.KeyA)))
	case key == ui.KeyPeriod:
		out = append(out, ".")
	case key == ui.KeySpace:
		out = append(out, "Space")
	case key == ui.KeyF2:
		out = append(out, "F2")
	case key == ui.KeyDelete && mac:
		out = append(out, "⌦")
	case key == ui.KeyDelete:
		out = append(out, "Del")
	case key == ui.KeyBackspace && mac:
		out = append(out, "⌫")
	case key == ui.KeyBackspace:
		out = append(out, "Backspace")
	}
	return out
}

func shortcutsPage(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	settingsSection(c, tok, nil, ui.Color{}, "", func(g *settingsGroup) {
		for _, sc := range appShortcuts(m) {
			g.row(sc.label, "", func() {
				for _, cap := range keyCaps(runtime.GOOS, sc.mods, sc.key) {
					keyCap(c, tok, cap)
				}
			})
		}
	})
}

// keyCap is one key of a shortcut, drawn as a key.
func keyCap(c *ui.Context, tok theme.Tokens, s string) {
	ui.Box(c).MinWidth(24).Height(22).Padding(0, 7).Radius(theme.RadiusSmall).Background(tok.Inset).Border(1, tok.Border).
		Center().Children(func() {
		ui.Text(c, s).FontSize(theme.Footnote).FontWeight(500).SingleLine()
	})
}

func (a *App) aboutPage(c *ui.Context, m locales.Messages, tok theme.Tokens, s *settings.Settings, changed *bool) {
	st := a.upd.Status()
	v := a.p.Version
	if v == "" {
		v = "dev"
	}
	busy := st.State == updater.Checking || st.State == updater.Downloading
	// The app itself: its tile, name, version and what it is.
	card(c, tok).FillWidth().Padding(theme.Space(5)).Children(func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Image(c, a.logo).Size(60, 60).Shrink(0).Radius(theme.RadiusSheet).Label(m.AppName())
			ui.Column(c).Gap(3).Grow(1).Shrink(1).Children(func() {
				ui.Text(c, m.AppName()).FontSize(theme.Title2).FontWeight(700)
				ui.Text(c, m.CurrentVersion(v)).FontSize(theme.Footnote).TextColor(tok.TextMuted)
				ui.Text(c, m.AboutTagline()).FontSize(theme.Body).TextColor(tok.TextMuted)
			})
			if pill(c, m.CheckForUpdates(), true, false, busy) {
				go a.upd.Check(context.Background())
			}
		})
	})
	settingsSection(c, tok, icons.Download, tok.TintAt(1), m.Updates(), func(g *settingsGroup) {
		g.row(m.CheckAutomatically(), "", func() {
			a.updateCheck = s.CheckUpdates
			if ui.Switch(c.Key("autoupd"), &a.updateCheck).Label(m.CheckAutomatically()).Changed() {
				s.CheckUpdates = a.updateCheck
				*changed = true
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
	settingsSection(c, tok, icons.Info, tok.TintAt(7), m.AboutInfo(), func(g *settingsGroup) {
		value := func(s string) func() {
			return func() { ui.Text(c, s).FontSize(theme.Body).TextColor(tok.TextMuted).SingleLine() }
		}
		g.row(m.AboutVersion(), "", value(v))
		g.row(m.AboutPlatform(), "", value(runtime.GOOS+" / "+runtime.GOARCH))
		dir := a.p.DataDir
		note := dir
		if dir == "" {
			note = m.AboutDataInMemory()
		}
		g.row(m.AboutDataFolder(), note, func() {
			if dir != "" && a.p.OpenPath != nil && iconButton(c, "datadir", icons.OpenFile, m.OpenDataFolder()) {
				if err := a.p.OpenPath(dir); err != nil {
					c.AddToast(ui.Toast{Title: m.ErrGeneric(err.Error()), Type: "error"})
				}
			}
		})
		g.row(m.AboutSecrets(), "", value(m.SecretsKeychain()))
	})
	settingsSection(c, tok, icons.OpenWith, tok.TintAt(2), m.AboutLinks(), func(g *settingsGroup) {
		link := func(key, url string) func() {
			return func() { ui.Link(c.Key(key), url[len("https://"):], url).FontSize(theme.Body) }
		}
		g.row(m.SourceCode(), "", link("repo", repoURL))
		g.row(m.License(), "", link("license", repoURL+"/blob/main/LICENSE"))
		g.row(m.ReportIssue(), "", link("issues", repoURL+"/issues"))
	})
}
