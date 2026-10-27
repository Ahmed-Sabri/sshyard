package app

import (
	"slices"
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// tabHeight and tabWidth size a tab; tabs narrow alike when there are
// many, as a browser's, down to tabMinWidth. tabInset is the track's
// padding round them, tabGap the room between two.
const (
	tabHeight   = 30
	tabWidth    = 200
	tabMinWidth = 132
	tabInset    = 3
	tabGap      = 2
)

// tabMoreWidth is the room the button listing the tabs that do not fit
// takes after the track: a 28 DIP IconButton and its gap.
const tabMoreWidth = 28 + 6

// tabsShown is the first tab and how many the title bar shows in room
// DIPs (0 before it was measured: all), keeping the active one among them
// and, as far as it can, the first it showed before.
func tabsShown(n, active, start int, room float32) (int, int) {
	fit := func(room float32) int {
		return max(int((room-2*tabInset+tabGap)/(tabMinWidth+tabGap)), 1)
	}
	if room <= 0 || fit(room) >= n {
		return 0, n
	}
	k := fit(room - tabMoreWidth)
	if active >= 0 && active < start {
		start = active
	}
	if active >= start+k {
		start = active - k + 1
	}
	return min(max(start, 0), n-k), k
}

// tabs sit in one capsule track, as a segmented control and as the
// actions beside them: the active one raised on it (TabActive, a hairline
// and the card's shadow), the others flat and apart by short lines, lit
// on hover in the same shape. Each shows the host as the sidebar does,
// its close button while it is active or under the pointer, and at once
// as the pointer comes, a tip of where its panes are. The tabs that do
// not fit go in the popover of a button after them.
func (a *App) tabs(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	area := ui.Row(c.Key("tabarea")).Grow(1).Shrink(1).MinWidth(0).FillHeight().Gap(6).AlignItems(ui.Center).ClipX()
	if len(a.ws.Tabs) == 0 {
		return
	}
	active := -1
	if a.page == pageWorkspace {
		active = a.ws.Active
	}
	room := area.Bounds().W
	start, k := tabsShown(len(a.ws.Tabs), active, a.tabStart, room)
	a.tabStart = start
	if room != a.tabRoom {
		// Measured anew (a resize): lay the tabs out again for it.
		a.tabRoom = room
		c.Invalidate()
	}
	// A copy: closing tabs from one's menu changes the list as it builds.
	shown := slices.Clone(a.ws.Tabs[start : start+k])
	// Read when the tabs paint, once all have been built.
	hovered := make([]bool, len(shown))
	area.Children(func() {
		track := ui.Row(c.Key("tabs")).Shrink(1).MinWidth(0).Height(tabHeight + 2*tabInset).Padding(tabInset).Gap(tabGap).
			AlignItems(ui.Center).Radius(theme.Capsule).Background(tok.Fill)
		track.Children(func() {
			for j, t := range shown {
				i := start + j
				a.tab(c, m, tok, t, i, i == active, j, hovered, active-start)
			}
		})
		if k < len(a.ws.Tabs) {
			a.moreTabs(c, m, tok, start, k)
		} else {
			a.tabsOpen = false
		}
	})
}

// tab is the tab t, the i-th, the j-th of those shown, whose hover it
// keeps in hovered[j]; activeAt is the place of the active one among them.
func (a *App) tab(c *ui.Context, m locales.Messages, tok theme.Tokens, t *layout.Tab, i int, on bool, j int, hovered []bool, activeAt int) {
	cn := a.conns[t.Focused().ID]
	chip := ui.Row(c.Key("tab:"+strconv.Itoa(t.ID))).Height(tabHeight).Gap(8).Padding(0, 4, 0, 6).Width(tabWidth).Shrink(1).MinWidth(tabMinWidth).
		AlignItems(ui.Center).Radius(theme.Capsule).Cursor(ui.CursorPointer)
	if cn != nil {
		chip.Drag(dragSession{cn.id})
	}
	hovered[j] = chip.Hovered()
	switch {
	case on:
		tok.Card.On(chip.Background(tok.TabActive).Border(1, tok.GlassEdge))
	case hovered[j]:
		chip.Background(tok.Hover)
	}
	chip.Draw(func(p *ui.Painter, r ui.Rect) {
		if next := j + 1; next < len(hovered) && !on && next != activeAt && !hovered[j] && !hovered[next] {
			x := r.X + r.W + tabGap/2
			p.Line(x, r.Y+r.H/2-7, x, r.Y+r.H/2+7, 1, tok.Line)
		}
	})
	chip.Children(func() {
		if cn != nil {
			hostAvatar(c, tok, a.liveHost(cn.host), 20)
		}
		txt := ui.Text(c, a.tabLabel(t)).SingleLine().Grow(1).Shrink(1).MinWidth(0).FontSize(theme.Body)
		switch {
		case on:
			txt.FontWeight(600)
		case !hovered[j]:
			txt.TextColor(tok.TextMuted)
		}
		if cn != nil && isTemp(cn.host.ID) {
			tempBadge(c, m, tok)
		}
		if cn != nil && cn.s != nil {
			dot(c, stateColor(tok, cn.s.Status().State))
		}
		a.tabAIMarks(c, t, tok, m)
		x := (IconButton{Icon: icons.Close, Label: m.CloseTab(), Size: Small}).Show(c, "x")
		if !on && !hovered[j] {
			x.Invisible()
		}
		if x.Clicked() {
			a.closeTab(t)
		}
	})
	chip.ContextMenu(func(menu *ui.Menu) { a.tabMenu(menu, m, i) })
	if chip.Clicked() {
		a.ws.Active = i
		a.page = pageWorkspace
		a.focusReq = t.Focused().ID
	}
	if hovered[j] && !chip.Pressed() && !chip.Dragging() {
		a.tabTip(c, m, tok, chip, t)
	}
}

// tabTip is a tab's tip, shown at once under it: the host of each of its
// panes, where it logs in and how its connection is.
func (a *App) tabTip(c *ui.Context, m locales.Messages, tok theme.Tokens, chip ui.Element, t *layout.Tab) {
	ui.Overlay(c, func() {
		tip := ui.Column(c.Key("tabtip")).AttachTo(chip, ui.AnchorBottomLeft, ui.AnchorTopLeft).Margin(6, 0, 0, 0).
			MaxWidth(360).Padding(8, 10).Gap(6).Radius(theme.RadiusControl).Background(tok.Inset).Border(1, tok.GlassEdge).PassThrough()
		tok.Lift.On(tip)
		tip.Children(func() {
			for _, p := range t.Panes() {
				cn := a.conns[p.Item.ID]
				if cn == nil {
					continue
				}
				h := a.liveHost(cn.host)
				ui.Row(c.Key("tip:" + strconv.Itoa(cn.id))).Gap(8).AlignItems(ui.Center).Children(func() {
					if cn.kind == layout.Terminal {
						hostAvatar(c, tok, h, 22)
					} else {
						softTile(c, kindIcon(cn.kind, false), hostTint(tok, h), 22)
					}
					ui.Column(c).Gap(1).Shrink(1).MinWidth(0).Children(func() {
						title := a.pv(h.Label())
						switch cn.kind {
						case layout.Files:
							title = m.FilesPane(title)
						case layout.Editor:
							title = cn.ed.name
						}
						ui.Text(c, title).FontSize(theme.Footnote).FontWeight(600).SingleLine()
						var parts []string
						if target := h.Target(); target != "" {
							parts = append(parts, a.pv(target))
						}
						if cn.s != nil {
							parts = append(parts, a.pv(a.statusText(m, h, cn.s.Status(), c.Now())))
						}
						line := strings.Join(parts, " · ")
						ui.Text(c, line).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
					})
				})
			}
		})
	})
}

// moreTabWidth is the width of the popover listing the tabs that do not
// fit; moreTabsHeight the most it grows to before it scrolls.
const (
	moreTabWidth   = 300
	moreTabsHeight = 420
)

// moreTabs is the button after the tabs, whose popover lists those that
// do not fit, the start .. start+k shown: each as its tip shows it, the
// active one lit; choosing one shows it.
func (a *App) moreTabs(c *ui.Context, m locales.Messages, tok theme.Tokens, start, k int) {
	hidden := len(a.ws.Tabs) - k
	b := (IconButton{Icon: icons.Collapse, Label: m.MoreTabs(hidden), Active: a.tabsOpen}).Show(c, "moretabs")
	if b.Clicked() {
		a.tabsOpen = !a.tabsOpen
	}
	ui.PopoverBase(c.Key("moretabspop"), b, &a.tabsOpen, func(panel ui.Element) {
		panel.AttachTo(b, ui.AnchorBottomEnd, ui.AnchorTopEnd).Margin(6, 0, 0, 0).Width(moreTabWidth).Padding(5).
			Radius(theme.RadiusCard).Background(tok.Inset).Border(1, tok.GlassEdge)
		tok.Lift.On(panel)
		// A copy: closing a tab from its row changes the list as it builds.
		tabs := slices.Clone(a.ws.Tabs)
		panel.Children(func() {
			ui.Scroll(c.Key("list")).MaxHeight(moreTabsHeight).FillWidth().Children(func() {
				ui.Column(c).Gap(2).FillWidth().Children(func() {
					for i, t := range tabs {
						if i < start || i >= start+k {
							a.moreTab(c, m, tok, t, i)
						}
					}
				})
			})
		})
	})
}

// moreTab is the row of the i-th tab t in the popover of moreTabs: its
// host, where it logs in and how it is connected, and its close button
// under the pointer.
func (a *App) moreTab(c *ui.Context, m locales.Messages, tok theme.Tokens, t *layout.Tab, i int) {
	cn := a.conns[t.Focused().ID]
	on := a.page == pageWorkspace && i == a.ws.Active
	label := a.tabLabel(t)
	row := ui.Row(c.Key("mt:"+strconv.Itoa(t.ID))).FillWidth().Padding(6, 6, 6, 8).Gap(10).AlignItems(ui.Center).
		Radius(theme.RadiusControl).Cursor(ui.CursorPointer).Label(label)
	hov := row.Hovered()
	switch {
	case on:
		row.Background(tok.Accent.Alpha(0.14))
	case row.Pressed():
		row.Background(tok.Press)
	case hov:
		row.Background(tok.Hover)
	}
	closed := false
	row.Children(func() {
		var sub []string
		if cn != nil {
			h := a.liveHost(cn.host)
			if cn.kind == layout.Terminal {
				hostAvatar(c, tok, h, 26)
			} else {
				softTile(c, kindIcon(cn.kind, false), hostTint(tok, h), 26)
			}
			if target := h.Target(); target != "" {
				sub = append(sub, a.pv(target))
			}
			if cn.s != nil {
				sub = append(sub, a.pv(a.statusText(m, h, cn.s.Status(), c.Now())))
			}
		}
		ui.Column(c).Gap(1).Grow(1).Shrink(1).MinWidth(0).Children(func() {
			title := ui.Text(c, label).FontSize(theme.Footnote).FontWeight(600).SingleLine()
			if on {
				title.TextColor(tok.Accent)
			}
			if len(sub) > 0 {
				ui.Text(c, strings.Join(sub, " · ")).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
			}
		})
		if cn != nil && cn.s != nil {
			dot(c, stateColor(tok, cn.s.Status().State))
		}
		x := (IconButton{Icon: icons.Close, Label: m.CloseTab(), Size: Small}).Show(c, "x")
		if !hov {
			x.Invisible()
		}
		if x.Clicked() {
			a.closeTab(t)
			closed = true
		}
	})
	if row.Clicked() && !closed {
		a.ws.Active = i
		a.page = pageWorkspace
		a.focusReq = t.Focused().ID
		a.tabsOpen = false
	}
}

// tabHost is the host of a tab: its focused pane's.
func (a *App) tabHost(t *layout.Tab) string {
	if cn := a.conns[t.Focused().ID]; cn != nil {
		return cn.host.ID
	}
	return ""
}

// tabMenu is the context menu of the i-th tab: closing it, or the tabs
// round it, others, on its host or on others.
func (a *App) tabMenu(menu *ui.Menu, m locales.Messages, i int) {
	n := len(a.ws.Tabs)
	if i >= n {
		return
	}
	host := a.tabHost(a.ws.Tabs[i])
	if menu.Item(m.CloseTab()).Chosen() {
		a.closeTabs(func(j int, _ *layout.Tab) bool { return j == i })
	}
	if menu.Item(m.CloseOtherTabs()).Disabled(n < 2).Chosen() {
		a.closeTabs(func(j int, _ *layout.Tab) bool { return j != i })
	}
	if menu.Item(m.CloseTabsLeft()).Disabled(i == 0).Chosen() {
		a.closeTabs(func(j int, _ *layout.Tab) bool { return j < i })
	}
	if menu.Item(m.CloseTabsRight()).Disabled(i == n-1).Chosen() {
		a.closeTabs(func(j int, _ *layout.Tab) bool { return j > i })
	}
	menu.Separator()
	same, others := 0, 0
	for _, t := range a.ws.Tabs {
		if a.tabHost(t) == host {
			same++
		} else {
			others++
		}
	}
	if menu.Item(m.CloseHostTabs()).Disabled(same == 0).Chosen() {
		a.closeTabs(func(_ int, t *layout.Tab) bool { return a.tabHost(t) == host })
	}
	if menu.Item(m.CloseOtherHostTabs()).Disabled(others == 0).Chosen() {
		a.closeTabs(func(_ int, t *layout.Tab) bool { return a.tabHost(t) != host })
	}
	menu.Separator()
	if menu.Item(m.CloseAllTabs()).Chosen() {
		a.closeTabs(func(int, *layout.Tab) bool { return true })
	}
}

// closeTabs closes the tabs that close says to, by their place and tab.
func (a *App) closeTabs(close func(i int, t *layout.Tab) bool) {
	var gone []*layout.Tab
	for i, t := range a.ws.Tabs {
		if close(i, t) {
			gone = append(gone, t)
		}
	}
	for _, t := range gone {
		a.closeTab(t)
	}
}
