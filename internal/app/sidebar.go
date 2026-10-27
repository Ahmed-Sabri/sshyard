package app

import (
	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
	"strings"
)

// addMenu is the sidebar's "+": a new host or group, quick connect, a
// local terminal, import.
func (a *App) addMenu(menu *ui.Menu, m locales.Messages) {
	if menu.Item(m.NewHost()).Chosen() {
		a.openEditor(nil)
	}
	if menu.Item(m.NewGroup()).Chosen() {
		a.openGroupForm("", "")
	}
	if menu.Item(m.QuickConnectMenu()).Shortcut(appMods, ui.KeyK).Chosen() {
		a.OpenQuickConnect()
	}
	if menu.Item(m.NewForwardMenu()).Disabled(a.store.Len() == 0).Chosen() {
		a.openFwdForm(m, nil, a.selectedHost())
	}
	if menu.Item(m.LocalTerminal()).Chosen() {
		a.OpenLocalTerminal()
	}
	menu.Separator()
	if menu.Item(m.ImportSSHConfigMenu()).Chosen() {
		a.OpenImport(m, "")
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
			(IconButton{Icon: icons.Add, Label: m.AddMenu()}).Show(c, "newhost").Menu(func(menu *ui.Menu) {
				a.addMenu(menu, m)
			})
		})
		if a.store.Len() == 0 {
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
		if !a.keychainOK {
			ui.Row(c).Margin(6, 8, 8, 8).Padding(8, 10).Gap(8).Radius(theme.RadiusControl).Background(tok.Warning.Alpha(0.12)).Children(func() {
				ui.Icon(c, icons.Warning.SVG).Size(14, 14).Shrink(0).TextColor(tok.Warning)
				ui.Text(c, m.KeychainUnavailable()).FontSize(theme.Caption).TextColor(tok.Text).Shrink(1)
			})
		}
		// The foot of the sidebar: how many hosts there are.
		ui.Box(c).Height(1).Shrink(0).FillWidth().Background(tok.Line)
		ui.Row(c.Key("sbfoot")).Height(statusBarHeight).Shrink(0).Padding(0, 12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.HostCount(a.store.Len())).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
		})
	})
}

// groupSection gives a group's section its menu, and takes the hosts
// dropped on it.
func (a *App) groupSection(c *ui.Context, m locales.Messages, tok theme.Tokens, sec ui.Element, g string) {
	sec.ContextMenu(func(menu *ui.Menu) {
		if menu.Item(m.NewHostInGroup()).Chosen() {
			a.openEditor(nil)
			a.form.group = g
		}
		if menu.Item(m.RenameGroup()).Chosen() {
			a.openGroupForm(g, "")
		}
		if menu.Item(m.DeleteGroup()).Chosen() {
			a.groupDelete, a.groupDeleting = g, true
		}
	})
	a.hostDrops(sec, tok, g)
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
		in := ui.TextInputBase(c.Key("q"), &a.search).Placeholder(m.SearchHosts()).Label(m.SearchHosts()).Grow(1).Shrink(1).MinWidth(40)
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
// end of its row: three small IconButtons and the gaps between them.
const hostActionsWidth = 3*24 + 2*2 + 4

// hostItem is a host of the sidebar: its tile, then its name, and while
// the pointer is over it or it is chosen, what to do with it at the end
// (connect, files, and the rest in its menu, as on a right click). The
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
	shown := chosen || it.Hovered()
	if shown {
		it.Padding(theme.Space(1.5), hostActionsWidth, theme.Space(1.5), theme.Space(2))
	}
	it.Children(func() {
		hostAvatar(c, tok, h, 20)
		// Kept, unseen, while not shown: its menu outlives the hover.
		acts := ui.Row(c.Key("acts")).Absolute().Top(0).Bottom(0).Right(4).Gap(2).AlignItems(ui.Center)
		if !shown {
			acts.Invisible()
		}
		acts.Children(func() {
			if (IconButton{Icon: icons.Run, On: icons.RunOn, Label: m.Connect(), Active: true, Size: Small}).Clicked(c, "connect") {
				a.Connect(h.ID, openAt{})
			}
			if (IconButton{Icon: icons.Folder, Label: m.OpenFiles(), Size: Small}).Clicked(c, "files") {
				a.OpenFiles(h.ID, openAt{}, nil)
			}
			(IconButton{Icon: icons.More, Label: m.MoreActions(), Size: Small}).Show(c, "more").Menu(func(menu *ui.Menu) {
				a.hostMenu(menu, m, h)
			})
		})
	})
	if it.DoubleClicked() {
		a.Connect(h.ID, openAt{})
	}
	it.ContextMenu(func(menu *ui.Menu) { a.hostMenu(menu, m, h) })
}

// hostMenu is what can be done with a sidebar host: its context menu,
// and the menu of its "more" button.
func (a *App) hostMenu(menu *ui.Menu, m locales.Messages, h assets.Host) {
	if menu.Item(m.Connect()).Chosen() {
		a.Connect(h.ID, openAt{})
	}
	if menu.Item(m.SplitRight()).Disabled(a.ws.ActiveTab() == nil).Chosen() {
		if t := a.ws.ActiveTab(); t != nil {
			a.Connect(h.ID, openAt{pane: t.Focus, edge: layout.Right})
		}
	}
	if menu.Item(m.OpenFiles()).Chosen() {
		a.OpenFiles(h.ID, openAt{}, nil)
	}
	if menu.Item(m.OpenAI()).Chosen() {
		a.OpenAI(h.ID, openAt{}, 0)
	}
	if menu.Item(m.OpenFilesBeside()).Disabled(a.ws.ActiveTab() == nil).Chosen() {
		if t := a.ws.ActiveTab(); t != nil {
			a.OpenFiles(h.ID, openAt{pane: t.Focus, edge: layout.Right}, nil)
		}
	}
	menu.Separator()
	if menu.Item(m.Favorite()).Checked(h.Favorite).Chosen() {
		a.store.SetFavorite(h.ID, !h.Favorite)
	}
	menu.Submenu(m.MoveToGroup(), func(sub *ui.Menu) {
		if sub.Item(m.NoGroup()).Checked(h.Group == "").Chosen() && h.Group != "" {
			a.store.SetGroup(h.ID, "")
		}
		sub.Separator()
		for _, g := range a.store.Groups() {
			if sub.Item(g).Checked(h.Group == g).Chosen() && h.Group != g {
				a.store.SetGroup(h.ID, g)
			}
		}
		sub.Separator()
		if sub.Item(m.NewGroupEllipsis()).Chosen() {
			a.openGroupForm("", h.ID)
		}
	})
	if menu.Item(m.Edit()).Chosen() {
		hh := h
		a.openEditor(&hh)
	}
	if menu.Item(m.InstallPublicKey()).Chosen() {
		a.openInstall(h.ID)
	}
	if menu.Item(m.NewForwardMenu()).Chosen() {
		a.openFwdForm(m, nil, h.ID)
	}
	if menu.Item(m.Delete()).Chosen() {
		a.deleteID, a.deleting = h.ID, true
	}
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
