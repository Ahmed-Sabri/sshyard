package app

import (
	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
	"strings"
)

// addEntries are the entries of the sidebar's "+": a new host or group,
// quick connect, a local terminal, import.
func (a *App) addEntries(m locales.Messages) []menuEntry {
	return []menuEntry{
		{Icon: icons.Host, Label: m.NewHost(), Do: func() { a.openEditor(nil) }},
		{Icon: icons.NewFolder, Label: m.NewGroup(), Do: func() { a.openGroupForm("", "") }},
		{Icon: icons.Quick, Label: m.QuickConnectMenu(), Mods: appMods, Key: ui.KeyK, Do: a.OpenQuickConnect},
		{Icon: icons.Forward, Label: m.NewForwardMenu(), Disabled: a.store.Len() == 0, Do: func() { a.openFwdForm(m, nil, a.selectedHost()) }},
		{Icon: icons.LocalShell, Label: m.LocalTerminal(), Do: func() { a.OpenLocalTerminal() }},
		menuSep,
		{Icon: icons.Import, Label: m.ImportSSHConfigMenu(), Do: func() { a.OpenImport(m, "") }},
	}
}

// dragHost is a host dragged from the sidebar onto a pane.
type dragHost struct{ ID string }

func (a *App) section(key string) *bool {
	if b, ok := a.sections[key]; ok {
		return b
	}
	b := true
	a.sections[key] = &b
	return &b
}

func (a *App) sidebarView(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	ui.Column(c).Fill().Children(func() {
		// The search, and beside it adding: a new host or group, a host
		// typed on the spot, or the hosts of ~/.ssh/config.
		ui.Row(c.Key("sbtools")).Padding(8, 8, 6, 12).Gap(4).AlignItems(ui.Center).Children(func() {
			a.searchBox(c, m, tok)
			a.sidebarSectionsButton(c, m)
			a.menuButton(c, "newhost", IconButton{Icon: icons.Add, Label: m.AddMenu()}, func() []menuEntry {
				return a.addEntries(m)
			})
		})
		if a.store.Len() == 0 && len(a.store.Groups()) == 0 {
			ui.Column(c).Padding(16).Gap(6).Children(func() {
				ui.Text(c, m.NoHosts()).FontWeight(600)
				ui.Text(c, m.NoHostsHint()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			})
			ui.Spacer(c)
		} else {
			favs, groups, byGroup := hostsBy(a.store, a.search)
			sb := ui.Sidebar(c.Key("hosts"), &a.selected, func() {
				if len(favs) > 0 {
					ui.SidebarSection(c.Key("s:fav"), m.Favorites(), a.section("fav"), func() {
						for _, h := range favs {
							a.hostItem(c, m, h, "fav:")
						}
					})
				}
				for _, g := range groups {
					sec := ui.SidebarSection(c.Key("s:g:"+g), g, a.section("g:"+g), func() {
						for _, h := range byGroup[g] {
							a.hostItem(c, m, h, "")
						}
						if len(byGroup[g]) == 0 {
							ui.Text(c, m.EmptyGroup()).FontSize(theme.Caption).TextColor(tok.TextMuted).Padding(2, 8, 6, 8)
						}
					})
					a.groupSection(c, m, tok, sec, g)
				}
				if hs := byGroup[""]; len(hs) > 0 {
					title := m.Ungrouped()
					if len(groups) == 0 {
						title = m.Hosts()
					}
					sec := ui.SidebarSection(c.Key("s:none"), title, a.section("none"), func() {
						for _, h := range hs {
							a.hostItem(c, m, h, "")
						}
					})
					a.hostDrops(sec, tok, "")
				}
				a.forwardsSection(c, m, tok)
			}).Grow(1).FillWidth()
			_ = sb
		}

		// The foot of the sidebar: host count and live SSH connections.
		ui.Box(c).Height(1).Shrink(0).FillWidth().Background(tok.Line)
		ui.Row(c.Key("sbfoot")).Height(statusBarHeight).Shrink(0).Padding(0, 12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.HostCount(a.store.Len())).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(1)
			ui.Spacer(c)
			n := a.connectedCount()
			color := tok.TextMuted
			if n > 0 {
				color = tok.Success
			}
			ui.Row(c.Key("sbconnections")).Shrink(0).Gap(theme.Space(1)).AlignItems(ui.Center).Children(func() {
				dot(c, color)
				ui.Text(c, m.ConnectionCount(n)).FontSize(theme.Caption).TextColor(color).SingleLine()
			})
		})
	})
}

// connectedCount counts shared SSH clients once, excluding local shells.
func (a *App) connectedCount() int {
	clients := make(map[*ssh.Client]struct{})
	for _, cn := range a.conns {
		if cn.s != nil {
			if client := cn.s.Client(); client != nil {
				clients[client] = struct{}{}
			}
		}
	}
	return len(clients)
}

// groupSection gives a group's section its menu, and takes the hosts
// dropped on it.
func (a *App) groupSection(c *ui.Context, m locales.Messages, tok theme.Tokens, sec ui.Element, g string) {
	a.contextMenu(sec, func() []menuEntry {
		return []menuEntry{
			{Icon: icons.Add, Label: m.NewHostInGroup(), Do: func() {
				a.openEditor(nil)
				a.form.group = g
			}},
			{Icon: icons.Rename, Label: m.RenameGroup(), Do: func() { a.openGroupForm(g, "") }},
			menuSep,
			{Icon: icons.Delete, Label: m.DeleteGroup(), Danger: true, Do: func() { a.groupDelete, a.groupDeleting = g, true }},
		}
	})
	a.groupDragDrop(c, m, tok, sec, g)
}

// hostDrops moves a host dropped from the sidebar on a section into the
// section's group, "" for none.
func (a *App) hostDrops(sec ui.Element, tok theme.Tokens, g string) {
	if d, ok := ui.Drop[dragHost](sec); ok {
		if h, ok := a.store.Get(d.ID); ok && h.Group != g {
			a.store.SetGroup(d.ID, g)
		}
	}
	if d, ok := ui.DragOver[dragHost](sec); ok {
		if h, ok := a.store.Get(d.ID); ok && h.Group != g {
			sec.Background(tok.Accent.Alpha(0.08))
		}
	}
}

// searchBox is the hosts' search field: a magnifier, the text, and while
// it holds any, an IconButton clearing it (as Escape does) that gives the
// field the keyboard back. MyGo's SearchField draws its clear button's
// cross in the theme's background, which the sidebar's clear material
// makes invisible: hence a field of the app's own.
func (a *App) searchBox(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	row := ui.Row(c.Key("hostsearch")).Grow(1).Shrink(1).MinWidth(0).Height(30).Padding(0, 4, 0, 10).Gap(6).AlignItems(ui.Center).
		Radius(theme.RadiusControl).Background(tok.Fill)
	row.Children(func() {
		ui.Icon(c, icons.Search.SVG).Size(14, 14).Shrink(0).TextColor(tok.TextMuted)
		in := ui.TextInputBase(c.Key("q"), &a.search).Placeholder(m.SearchHosts()).Label(m.SearchHosts()).Grow(1).Shrink(1).MinWidth(0)
		if a.searchFocus {
			in.Focus()
			a.searchFocus = false
		}
		if a.search != "" {
			if (IconButton{Icon: icons.ClearText, Label: m.ClearSearch(), Size: Small}).Clicked(c, "clear") || in.Shortcut(0, ui.KeyEscape) {
				a.ClearSearch()
			}
		} else {
			// as high as with the button, so that the field keeps its size
			ui.Box(c).Size(24, 24).Shrink(0)
		}
	})
}

// ClearSearch empties the hosts' search and focuses its field.
func (a *App) ClearSearch() {
	a.search = ""
	a.searchFocus = true
}

// selectedHost is the ID of the host chosen in the sidebar, if it still is.
func (a *App) selectedHost() string {
	id := a.selected
	if len(id) > 4 && id[:4] == "fav:" {
		id = id[4:]
	}
	if _, ok := a.store.Get(id); !ok {
		return ""
	}
	return id
}

// hostActionsWidth is the room the actions of a sidebar host take at the
// end of its row: two small IconButtons and the gap between them.
const hostActionsWidth = 2*24 + 2 + 4

// hostItem is a host of the sidebar: its tile, then its name, and while
// the pointer is over it or it is chosen, what to do with it at the end
// (files, and the rest in its menu, as on a right click; a double click
// connects). A dot on the tile's corner tells how its sessions are. The
// item lays its children out reversed, so that the tile it is given
// (after its label, as a badge) comes first.
func (a *App) hostItem(c *ui.Context, m locales.Messages, h assets.Host, prefix string) {
	tok := currentTokens(c)
	it := ui.SidebarItem(c.Key(prefix+h.ID), prefix+h.ID, nil, a.pv(h.Label())).Reverse().Tooltip(a.pv(a.hostTip(m, h))).Drag(dragHost{h.ID})
	chosen := a.selected == prefix+h.ID
	if chosen {
		// Gray even while the sidebar has the focus, as the actions on
		// it are drawn for the sidebar's ground, not the accent.
		it.Background(tok.Press).TextColor(tok.Text)
	}
	more := "more:" + prefix + h.ID
	shown := chosen || it.Hovered() || a.menu.key == more
	if shown {
		it.Padding(theme.Space(1.5), hostActionsWidth, theme.Space(1.5), theme.Space(2))
	}
	it.Children(func() {
		ui.Box(c.Key("avatar")).Shrink(0).Children(func() {
			hostAvatar(c, tok, h, 20)
			if st, ok := a.hostState(h.ID); ok {
				dot(c, stateColor(tok, st)).Absolute().Top(-2).Right(-2).Border(1.5, tok.Sidebar).Size(9, 9).Radius(4.5).
					Label(a.statusText(m, h, session.Status{State: st}, c.Now()))
			}
		})
		// Shown while its menu is open, which outlives the hover.
		acts := ui.Row(c.Key("acts")).Absolute().Top(0).Bottom(0).Right(4).Gap(2).AlignItems(ui.Center)
		if !shown {
			acts.Invisible()
		}
		acts.Children(func() {
			if (IconButton{Icon: icons.Folder, Label: m.OpenFiles(), Size: Small}).Clicked(c, "files") {
				a.OpenFiles(h.ID, openAt{}, nil)
			}
			a.menuButton(c, more, IconButton{Icon: icons.More, Label: m.MoreActions(), Size: Small}, func() []menuEntry {
				return a.hostEntries(m, h)
			})
		})
	})
	if it.DoubleClicked() {
		a.Connect(h.ID, openAt{})
	}
	a.contextMenu(it, func() []menuEntry { return a.hostEntries(m, h) })
}

// hostEntries are what can be done with a sidebar host: its context
// menu, and the menu of its "more" button.
func (a *App) hostEntries(m locales.Messages, h assets.Host) []menuEntry {
	noTab := a.ws.ActiveTab() == nil
	beside := func(open func(at openAt)) func() {
		return func() {
			if t := a.ws.ActiveTab(); t != nil {
				open(openAt{pane: t.Focus, edge: layout.Right})
			}
		}
	}
	groups := []menuEntry{
		{Icon: icons.Folder, Label: m.NoGroup(), Checked: h.Group == "", Do: func() {
			if h.Group != "" {
				a.store.SetGroup(h.ID, "")
			}
		}},
	}
	groupNames := a.store.Groups()
	if len(groupNames) > 0 {
		groups = append(groups, menuSep)
	}
	for _, g := range groupNames {
		groups = append(groups, menuEntry{Icon: icons.Folder, Label: g, Checked: h.Group == g, Do: func() {
			if h.Group != g {
				a.store.SetGroup(h.ID, g)
			}
		}})
	}
	groups = append(groups, menuSep, menuEntry{Icon: icons.NewFolder, Label: m.NewGroupEllipsis(), Do: func() { a.openGroupForm("", h.ID) }})
	return []menuEntry{
		{Icon: icons.Run, Label: m.Connect(), Do: func() { a.Connect(h.ID, openAt{}) }},
		{Icon: icons.SplitRight, Label: m.SplitRight(), Disabled: noTab, Do: beside(func(at openAt) { a.Connect(h.ID, at) })},
		{Icon: icons.Folder, Label: m.OpenFiles(), Do: func() { a.OpenFiles(h.ID, openAt{}, nil) }},
		{Icon: icons.Robot, Label: m.OpenAI(), Do: func() { a.OpenAI(h.ID, openAt{}, 0) }},
		{Icon: icons.OpenFile, Label: m.OpenFilesBeside(), Disabled: noTab, Do: beside(func(at openAt) { a.OpenFiles(h.ID, at, nil) })},
		menuSep,
		{Icon: icons.Star, Label: m.Favorite(), Checked: h.Favorite, Do: func() { a.store.SetFavorite(h.ID, !h.Favorite) }},
		{Icon: icons.MoveTo, Label: m.MoveToGroup(), Sub: groups},
		{Icon: icons.Edit, Label: m.Edit(), Do: func() {
			hh := h
			a.openEditor(&hh)
		}},
		{Icon: icons.InstallKey, Label: m.InstallPublicKey(), Do: func() { a.openInstall(h.ID) }},
		{Icon: icons.Forward, Label: m.NewForwardMenu(), Do: func() { a.openFwdForm(m, nil, h.ID) }},
		{Icon: icons.Delete, Label: m.Delete(), Danger: true, Do: func() { a.deleteID, a.deleting = h.ID, true }},
	}
}

// hostState is how the sessions open on host id are, the best of them
// (connected, then on the way, then failed), and false with none.
func (a *App) hostState(id string) (session.State, bool) {
	rank := func(st session.State) int {
		switch st {
		case session.Connected:
			return 3
		case session.Connecting, session.Reconnecting:
			return 2
		case session.Failed, session.Disconnected:
			return 1
		}
		return 0
	}
	best, ok := session.State(0), false
	for _, cn := range a.conns {
		if cn.host.ID != id || cn.s == nil || cn.fw != nil {
			continue
		}
		st := cn.s.Status().State
		if rank(st) > 0 && (!ok || rank(st) > rank(best)) {
			best, ok = st, true
		}
	}
	return best, ok
}

// hostTip is a sidebar host's tooltip: where it logs in, and the proxy
// and jump hosts it is reached through ("via A → B").
func (a *App) hostTip(m locales.Messages, h assets.Host) string {
	var chain []string
	if px, ok := a.proxyOf(h); ok {
		chain = append(chain, px.Name)
	}
	path, _ := a.store.JumpPath(h.ID)
	for _, j := range path {
		chain = append(chain, j.Label())
	}
	if len(chain) == 0 {
		return h.Target()
	}
	return m.ViaJumps(h.Target(), strings.Join(chain, " → "))
}
