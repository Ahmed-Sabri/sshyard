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

// addMenu is the sidebar's "+": a new host, quick connect, import.
func (a *App) addMenu(menu *ui.Menu, m locales.Messages) {
	if menu.Item(m.NewHost()).Chosen() {
		a.openEditor(nil)
	}
	if menu.Item(m.QuickConnectMenu()).Shortcut(appMods, ui.KeyK).Chosen() {
		a.OpenQuickConnect()
	}
	if menu.Item(m.NewForwardMenu()).Disabled(a.store.Len() == 0).Chosen() {
		a.openFwdForm(m, nil, a.selectedHost())
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
	bar := c.TitleBar()
	ui.Column(c).Fill().Children(func() {
		// The top band, level with the toolbar: the app's identity after
		// the window controls (macOS), and adding a host.
		ui.Row(c.Key("sbhead")).Height(bandHeight(c)).Shrink(0).Padding(0, 10, 0, max(bar.Left+4, 14)).Gap(8).
			AlignItems(ui.Center).DragWindow().Children(func() {
			tile(c, icons.Terminal, theme.Brand, 22)
			ui.Column(c).Gap(0).Shrink(1).Children(func() {
				ui.Text(c, m.AppName()).FontSize(theme.Body).FontWeight(600).SingleLine()
				ui.Text(c, m.HostCount(a.store.Len())).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
			})
			ui.Spacer(c)
			// Adding: a new host, a host typed on the spot, or the
			// hosts of ~/.ssh/config.
			(IconButton{Icon: icons.Add, Label: m.AddMenu()}).Show(c, "newhost").Menu(func(menu *ui.Menu) {
				a.addMenu(menu, m)
			})
		})
		ui.Box(c).Padding(0, 12, 6, 12).Children(func() { a.searchBox(c, m, tok) })
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
					ui.SidebarSection(c.Key("s:g:"+g), g, a.section("g:"+g), func() {
						for _, h := range byGroup[g] {
							a.hostItem(c, m, h, "")
						}
					})
				}
				if hs := byGroup[""]; len(hs) > 0 {
					title := m.Ungrouped()
					if len(groups) == 0 {
						title = m.Hosts()
					}
					ui.SidebarSection(c.Key("s:none"), title, a.section("none"), func() {
						for _, h := range hs {
							a.hostItem(c, m, h, "")
						}
					})
				}
				a.forwardsSection(c, m, tok)
			}).Grow(1).FillWidth()
			_ = sb
		}
		id := a.selectedHost()
		ui.Column(c).Padding(10).Gap(8).Children(func() {
			if !a.keychainOK {
				ui.Row(c).Padding(8, 10).Gap(8).Radius(theme.RadiusControl).Background(tok.Warning.Alpha(0.12)).Children(func() {
					ui.Icon(c, icons.Warning.SVG).Size(14, 14).Shrink(0).TextColor(tok.Warning)
					ui.Text(c, m.KeychainUnavailable()).FontSize(theme.Caption).TextColor(tok.Text).Shrink(1)
				})
			}
			if id != "" {
				h, _ := a.store.Get(id)
				// The chosen host's card: who it is, and what to do with it.
				el := card(c, tok).Padding(10).Gap(10).FillWidth().Label(m.HostActions())
				el.Children(func() {
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						tile(c, icons.Host, tok.Tint(h.ID), 30)
						ui.Column(c).Shrink(1).Gap(1).Children(func() {
							ui.Text(c, h.Label()).FontWeight(600).SingleLine()
							ui.Text(c, h.Target()).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
						})
					})
					ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
						if pill(c, m.Connect(), true, false, false) {
							a.Connect(id, openAt{})
						}
						ui.Spacer(c)
						if iconButton(c, "files", icons.Folder, m.OpenFiles()) {
							a.OpenFiles(id, openAt{}, nil)
						}
						fav := m.AddFavorite()
						if h.Favorite {
							fav = m.RemoveFavorite()
						}
						if (IconButton{Icon: icons.Star, On: icons.StarOn, Label: fav, Active: h.Favorite, Toggle: true}).Clicked(c, "fav") {
							a.store.SetFavorite(id, !h.Favorite)
						}
						if iconButton(c, "edit", icons.Edit, m.EditHost()) {
							hh := h
							a.openEditor(&hh)
						}
						if (IconButton{Icon: icons.Delete, Label: m.DeleteHost(), Danger: true}).Clicked(c, "delete") {
							a.deleteID, a.deleting = id, true
						}
					})
				})
			}
		})
	})
}

// searchBox is the hosts' search field: a magnifier, the text, and while
// it holds any, an IconButton clearing it (as Escape does) that gives the
// field the keyboard back. MyGo's SearchField draws its clear button's
// cross in the theme's background, which the sidebar's clear material
// makes invisible: hence a field of the app's own.
func (a *App) searchBox(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	row := ui.Row(c.Key("hostsearch")).FillWidth().Height(30).Padding(0, 4, 0, 10).Gap(6).AlignItems(ui.Center).
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

func (a *App) hostItem(c *ui.Context, m locales.Messages, h assets.Host, prefix string) {
	icon := icons.Host.SVG
	if h.Favorite && prefix == "" {
		icon = icons.StarOn.SVG
	}
	it := ui.SidebarItem(c.Key(prefix+h.ID), prefix+h.ID, icon, h.Label()).Tooltip(a.hostTip(m, h)).Drag(dragHost{h.ID})
	if it.DoubleClicked() {
		a.Connect(h.ID, openAt{})
	}
	it.ContextMenu(func(menu *ui.Menu) {
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
	})
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
