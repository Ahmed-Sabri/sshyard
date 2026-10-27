package app

import (
	"strconv"
	"time"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// dragSession is a pane, terminal or files, dragged by its tab or its
// header.
type dragSession struct{ ID int }

// paneDrop is what a pane takes dropped: a dragSession or a dragHost.
type paneDrop interface{ paneDrop() }

func (dragSession) paneDrop() {}
func (dragHost) paneDrop()    {}

// dropTarget is where a drag over the panes would land.
type dropTarget struct {
	pane int // the node
	edge layout.Edge
}

// paneNode is the node of the pane showing a session, 0 for none.
func (a *App) paneNode(id int) int {
	if _, n := a.ws.Locate(id); n != nil {
		return n.ID
	}
	return 0
}

// edgeFor is where on a pane w×h a drop at x, y lands: the outer quarter
// of a side splits the pane there, the middle replaces it.
func edgeFor(x, y, w, h float32) layout.Edge {
	if w <= 0 || h <= 0 {
		return layout.Center
	}
	fx, fy := x/w, y/h
	type cand struct {
		d float32
		e layout.Edge
	}
	best := cand{1, layout.Center}
	for _, c := range []cand{{fx, layout.Left}, {1 - fx, layout.Right}, {fy, layout.Top}, {1 - fy, layout.Bottom}} {
		if c.d < 0.25 && c.d < best.d {
			best = c
		}
	}
	return best.e
}

// edgeRect is the part of a pane a drop at edge would take.
func edgeRect(r ui.Rect, e layout.Edge) ui.Rect {
	switch e {
	case layout.Left:
		r.W /= 2
	case layout.Right:
		r.X += r.W / 2
		r.W /= 2
	case layout.Top:
		r.H /= 2
	case layout.Bottom:
		r.Y += r.H / 2
		r.H /= 2
	}
	return r
}

func stateColor(tok theme.Tokens, st session.State) ui.Color {
	switch st {
	case session.Connected:
		return tok.Success
	case session.Connecting, session.Reconnecting, session.Idle:
		return tok.Warning
	case session.Failed:
		return tok.Danger
	}
	return tok.TextMuted
}

// tabLabel names a tab by its focused pane's host.
func (a *App) tabLabel(t *layout.Tab) string {
	cn := a.conns[t.Focused().ID]
	if cn == nil {
		return ""
	}
	if n := len(t.Panes()); n > 1 {
		return a.pv(cn.host.Label()) + " +" + strconv.Itoa(n-1)
	}
	return a.pv(cn.host.Label())
}

// kindIcon is the icon of a pane kind, the "-fill" one when on.
func kindIcon(k layout.Kind, on bool) *icons.Icon {
	switch {
	case k == layout.Files && on:
		return icons.FolderOn
	case k == layout.Files:
		return icons.Folder
	case k == layout.Editor && on:
		return icons.EditFileOn
	case k == layout.Editor:
		return icons.EditFile
	case k == layout.AI && on:
		return icons.RobotOn
	case k == layout.AI:
		return icons.Robot
	case on:
		return icons.TerminalOn
	}
	return icons.Terminal
}

// OpenFilesBeside opens a files pane on the focused pane's host, to its
// right.
func (a *App) OpenFilesBeside() {
	t := a.ws.ActiveTab()
	cn := a.focused()
	if t == nil || cn == nil {
		return
	}
	a.OpenFiles(cn.host.ID, openAt{pane: t.Focus, edge: layout.Right}, cn)
}

// SplitFocused opens a terminal on the focused pane's host beside it.
func (a *App) SplitFocused(edge layout.Edge) {
	t := a.ws.ActiveTab()
	cn := a.focused()
	if t == nil || cn == nil {
		return
	}
	a.Connect(cn.host.ID, openAt{pane: t.Focus, edge: edge})
}

// CloseFocused closes the focused pane.
func (a *App) CloseFocused() {
	if cn := a.focused(); cn != nil {
		a.CloseSession(cn.id)
	}
}

func (a *App) closeTab(t *layout.Tab) {
	for _, p := range t.Panes() {
		a.CloseSession(p.Item.ID)
	}
}

// tempBadge marks a tab or pane on a temporary host of quick connect.
func tempBadge(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	ui.Box(c.Key("temp")).Shrink(0).Padding(1, 6).Radius(theme.Capsule).Background(tok.Accent.Alpha(0.14)).Children(func() {
		ui.Text(c, m.Temporary()).FontSize(theme.Caption).FontWeight(500).TextColor(tok.Accent).SingleLine()
	})
}

func (a *App) shortcuts(c *ui.Context) {
	if c.Shortcut(appMods, ui.KeyK) {
		a.OpenQuickConnect()
	}
	if c.Shortcut(saveMods, ui.KeyS) && a.page == pageWorkspace {
		if cn := a.focused(); cn != nil && cn.kind == layout.Editor {
			a.saveEditor(cn, false)
		}
	}
	if c.Shortcut(appMods, ui.KeyF) && a.page == pageWorkspace {
		if cn := a.focused(); cn != nil && cn.kind == layout.Terminal {
			a.finding = true
		}
	}
	if c.Shortcut(appMods, ui.KeyT) {
		if cn := a.focused(); cn != nil {
			a.Connect(cn.host.ID, openAt{})
		} else if id := a.selectedHost(); id != "" {
			a.Connect(id, openAt{})
		}
	}
	if c.Shortcut(appMods, ui.KeyW) {
		a.CloseFocused()
	}
	if c.Shortcut(appMods, ui.KeyD) {
		a.SplitFocused(layout.Right)
	}
	if c.Shortcut(appMods, ui.KeyE) {
		a.SplitFocused(layout.Bottom)
	}
	if c.Shortcut(appMods, ui.KeyO) {
		a.OpenFilesBeside()
	}
	if c.Shortcut(aiMods, ui.KeyA) && a.page == pageWorkspace {
		a.OpenAIBeside()
	}
	if c.Shortcut(stopMods, ui.KeyPeriod) {
		a.stopShortcut()
	}
	if c.Shortcut(stopAllMods, ui.KeyPeriod) {
		a.StopAllAI()
	}
}

// toolbarTrail is the toolbar's right padding: a small gap before the
// window buttons where they sit on the right (Windows, Linux), and the
// toolbar's own margin where they do not (macOS, on the left).
func toolbarTrail(bar ui.TitleBar) float32 {
	if bar.Right > 0 {
		return bar.Right + 6
	}
	return 12
}

// tabHeight and tabWidth size a tab; tabs narrow alike when there are
// many, as a browser's.
const (
	tabHeight = 32
	tabWidth  = 180
)

// tabs are the browser's kind: standing on the bottom of the title bar,
// the active one in the content's color and joined to it, with feet that
// curve into the bar; the others flat, apart by short lines, and lit on
// hover. Each shows the host as the sidebar does.
func (a *App) tabs(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	active := -1
	if a.page == pageWorkspace {
		active = a.ws.Active
	}
	// Read when the tabs paint, once all have been built.
	hovered := make([]bool, len(a.ws.Tabs))
	ui.Row(c.Key("tabs")).Shrink(1).MinWidth(0).AlignSelf(ui.End).Height(tabHeight).Padding(0, theme.RadiusSmall).Children(func() {
		for i, t := range a.ws.Tabs {
			on := i == active
			cn := a.conns[t.Focused().ID]
			chip := ui.Row(c.Key("tab:"+strconv.Itoa(t.ID))).Gap(6).Padding(0, 4, 0, 10).Width(tabWidth).Shrink(1).MinWidth(72).
				AlignItems(ui.Center).Cursor(ui.CursorPointer)
			if cn != nil {
				chip.Drag(dragSession{cn.id})
			}
			hovered[i] = chip.Hovered()
			chip.Draw(func(p *ui.Painter, r ui.Rect) {
				switch {
				case on:
					tabShape(p, r, tok.Canvas, tok.Line)
				case hovered[i]:
					p.Fill(ui.Rect{X: r.X + 2, Y: r.Y + 3, W: r.W - 4, H: r.H - 6}, tok.Hover, theme.RadiusControl)
				}
				if next := i + 1; next < len(hovered) && !on && next != active && !hovered[i] && !hovered[next] {
					x := r.X + r.W - 0.5
					p.Line(x, r.Y+r.H/2-7, x, r.Y+r.H/2+7, 1, tok.Line)
				}
			})
			chip.Children(func() {
				if cn != nil {
					hostAvatar(c, tok, a.liveHost(cn.host), 16)
				}
				txt := ui.Text(c, a.tabLabel(t)).SingleLine().Grow(1).Shrink(1).MinWidth(0).FontSize(theme.Footnote)
				if on {
					txt.FontWeight(500)
				} else {
					txt.TextColor(tok.TextMuted)
				}
				if cn != nil && isTemp(cn.host.ID) {
					tempBadge(c, m, tok)
				}
				if cn != nil && cn.s != nil {
					dot(c, stateColor(tok, cn.s.Status().State))
				}
				a.tabAIMarks(c, t, tok, m)
				if (IconButton{Icon: icons.Close, Label: m.CloseTab(), Size: Small}).Clicked(c, "x") {
					a.closeTab(t)
				}
			})
			chip.ContextMenu(func(menu *ui.Menu) {
				if menu.Item(m.CloseTab()).Chosen() {
					a.closeTab(t)
				}
			})
			if chip.Clicked() {
				a.ws.Active = i
				a.page = pageWorkspace
				a.focusReq = t.Focused().ID
			}
		}
	})
}

// tabShape paints the active tab in r: rounded at the top, its feet
// curving out into the bar at the bottom, filled with fill and edged
// with line, open at the bottom where it joins the content.
func tabShape(p *ui.Painter, r ui.Rect, fill, line ui.Color) {
	rr, f := theme.RadiusControl, theme.RadiusSmall
	x0, x1, y0, y1 := r.X, r.X+r.W, r.Y, r.Y+r.H
	outline := func(path *ui.Path) *ui.Path {
		return path.MoveTo(x0-f, y1).QuadTo(x0, y1, x0, y1-f).LineTo(x0, y0+rr).QuadTo(x0, y0, x0+rr, y0).
			LineTo(x1-rr, y0).QuadTo(x1, y0, x1, y0+rr).LineTo(x1, y1-f).QuadTo(x1, y1, x1+f, y1)
	}
	var shape, edge ui.Path
	p.FillPath(outline(&shape).Close(), fill)
	p.StrokePath(outline(&edge), 1, line)
}

func (a *App) tabStrip(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	// The title bar's part over the content: the tabs on its foot, the
	// actions grouped on the right, clear of the window buttons.
	bar := c.TitleBar()
	ui.Row(c.Key("toolbar")).Grow(1).Shrink(1).MinWidth(0).FillHeight().Padding(0, toolbarTrail(bar), 0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		if len(a.ws.Tabs) > 0 {
			a.tabs(c, m, tok)
		}
		ui.Spacer(c)
		// One capsule of the actions on the focused session. The settings
		// are in the status bar, the panels in the rail.
		cn := a.focused()
		if cn == nil || a.page != pageWorkspace {
			return
		}
		ui.Row(c.Key("actions")).Gap(0).Padding(2).Radius(theme.Capsule).Background(tok.Fill).AlignItems(ui.Center).Children(func() {
			add := (IconButton{Icon: icons.Add, Label: m.NewTab()}).Show(c, "newtab")
			if add.Clicked() {
				a.Connect(cn.host.ID, openAt{})
			}
			add.ContextMenu(func(menu *ui.Menu) {
				if menu.Item(m.NewTab()).Shortcut(appMods, ui.KeyT).Chosen() {
					a.Connect(cn.host.ID, openAt{})
				}
				if menu.Item(m.OpenAI()).Disabled(!remoteOnly(cn)).Chosen() {
					a.OpenAI(cn.host.ID, openAt{}, 0)
				}
				link := 0
				if cn.kind == layout.Terminal {
					link = cn.id
				}
				if menu.Item(m.OpenAIRight()).Disabled(!remoteOnly(cn)).Chosen() {
					if t := a.ws.ActiveTab(); t != nil {
						a.OpenAI(cn.host.ID, openAt{pane: t.Focus, edge: layout.Right}, link)
					}
				}
				if menu.Item(m.OpenAIBelow()).Disabled(!remoteOnly(cn)).Chosen() {
					if t := a.ws.ActiveTab(); t != nil {
						a.OpenAI(cn.host.ID, openAt{pane: t.Focus, edge: layout.Bottom}, link)
					}
				}
				menu.Separator()
				if menu.Item(m.StopAllAI()).Shortcut(stopAllMods, ui.KeyPeriod).Chosen() {
					a.StopAllAI()
				}
			})
			if iconButton(c, "splitright", icons.SplitRight, m.SplitRight()) {
				a.SplitFocused(layout.Right)
			}
			if iconButton(c, "splitdown", icons.SplitDown, m.SplitDown()) {
				a.SplitFocused(layout.Bottom)
			}
		})
	})
}

func (a *App) terminalPage(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	t := a.ws.ActiveTab()
	if t == nil {
		a.finding = false
		ui.Column(c).Fill().Center().Gap(12).Children(func() {
			tile(c, icons.Terminal, theme.Brand, 64)
			ui.Text(c, m.WelcomeTitle()).FontWeight(600).FontSize(theme.Title1).MarginY(4)
			ui.Text(c, m.WelcomeHint()).FontSize(theme.Body).TextColor(tok.TextMuted)
			if a.store.Len() == 0 {
				// No host yet: start from the ones ssh already knows.
				ui.Row(c.Key("start")).Gap(8).MarginY(8).Children(func() {
					if pill(c, m.ImportSSHConfig(), true, false, false) {
						a.OpenImport(m, "")
					}
					if pill(c, m.QuickConnect(), false, false, false) {
						a.OpenQuickConnect()
					}
					if pill(c, m.NewHost(), false, false, false) {
						a.openEditor(nil)
					}
				})
			}
		})
		return
	}
	cn := a.focused()
	if cn == nil || cn.kind != layout.Terminal {
		a.finding = false
	} else {
		if a.finding {
			cn.tv.updateFind(a.query, true)
		}
		n := len(cn.tv.matches)
		if a.match >= n {
			a.match = max(n-1, 0)
		}
		bar := ui.FindBar(c.Key("find"), &a.finding, &a.query, n, &a.match)
		if bar.Changed() || (a.finding && cn.tv.current != a.match) {
			cn.tv.showMatch(a.match)
		}
		if !a.finding {
			cn.tv.updateFind("", false)
		}
	}
	a.dropPreview = nil
	ui.Box(c).Fill().Padding(paneGap / 2).Children(func() { a.nodeView(c, t, t.Root, tok, m) })
}

func (a *App) ratio(n *layout.Node) *float32 {
	p, ok := a.pct[n.ID]
	if !ok {
		v := n.Ratio * 100
		if v <= 0 || v >= 100 {
			v = 50
		}
		p = &v
		a.pct[n.ID] = p
	}
	return p
}

func (a *App) nodeView(c *ui.Context, t *layout.Tab, n *layout.Node, tok theme.Tokens, m locales.Messages) {
	if n == nil {
		return
	}
	if n.Leaf() {
		a.paneView(c, t, n, tok, m)
		return
	}
	p := a.ratio(n)
	first := func() { a.nodeView(c, t, n.A, tok, m) }
	second := func() { a.nodeView(c, t, n.B, tok, m) }
	key := c.Key("split:" + strconv.Itoa(n.ID))
	if n.Axis == layout.Row {
		ui.SplitPercent(key, p, first, second).Fill()
	} else {
		ui.SplitVerticalPercent(key, p, first, second).Fill()
	}
	n.Ratio = *p / 100
}

// paneGap is the gutter between two panes, and from the edges of the
// content: half of it round each pane and half round all.
const paneGap = 4

func (a *App) paneView(c *ui.Context, t *layout.Tab, n *layout.Node, tok theme.Tokens, m locales.Messages) {
	cn := a.conns[n.Item.ID]
	if cn == nil {
		return
	}
	multi := len(t.Panes()) > 1
	focused := t.Focus == n.ID
	pal := tok.Terminal
	bg := pal.Background
	if cn.kind != layout.Terminal {
		bg = tok.Inset
	}
	// Each pane is a card on the canvas: a hairline edge, a soft shadow,
	// and the same gutter all round, paneGap between two panes and from
	// the edges of the content. (The gutter is the padding of a box the
	// card fills: a margin on a card that fills would push it out on the
	// right and at the bottom.)
	var pane ui.Element
	ui.Box(c.Key("pw:" + strconv.Itoa(n.ID))).Fill().Padding(paneGap / 2).Children(func() {
		pane = ui.Column(c.Key("pane:" + strconv.Itoa(n.ID))).Grow(1).Shrink(1).MinHeight(0).Radius(theme.RadiusCard).Clip().Background(bg)
		theme.Shadow(tok.Card).On(pane)
		// The ask names a pane by number. Outline that pane in the accent
		// color, wider than the focus hairline, until the question is gone.
		if a.clipAsk != nil && a.clipAsk.open && a.clipAsk.pane == cn.id {
			pane.Border(2, tok.Accent)
		} else if multi && focused {
			pane.Border(1, tok.Accent.Alpha(0.7))
		} else {
			pane.Border(1, tok.Line)
		}
		var st session.Status
		if cn.s != nil {
			st = cn.s.Status()
		}
		pane.Children(func() {
			if cn.kind == layout.AI {
				a.aiPane(c, t, n, cn, tok, m)
				return
			}
			head := ui.Row(c.Key("head")).Height(36).Shrink(0).Padding(0, 6, 0, 10).Gap(8).AlignItems(ui.Center).Drag(dragSession{cn.id}).Label(m.PaneActions())
			head.Children(func() {
				// A terminal shows its host as the sidebar does; the others
				// what they are, in its color.
				if cn.kind == layout.Terminal {
					hostAvatar(c, tok, a.liveHost(cn.host), 22)
				} else {
					softTile(c, kindIcon(cn.kind, focused), hostTint(tok, a.liveHost(cn.host)), 22)
				}
				title := a.pv(cn.host.Label())
				switch cn.kind {
				case layout.Files:
					title = m.FilesPane(a.pv(cn.host.Label()))
				case layout.Editor:
					title = cn.ed.name
				}
				tt := ui.Text(c, title).FontWeight(600).FontSize(theme.Footnote).SingleLine()
				if !focused && multi {
					tt.TextColor(tok.TextMuted)
				}
				if cn.s != nil {
					dot(c, stateColor(tok, st.State))
				}
				if cn.kind == layout.Terminal && cn.s != nil && cn.s.Term.Modes().MouseTracking {
					// The program takes the mouse: say so, and how to select.
					ui.Row(c.Key("mouse")).Shrink(0).AlignItems(ui.Center).Label(m.MouseReporting()).Tooltip(m.MouseReporting()).Children(func() {
						ui.Icon(c, icons.Mouse.SVG).Size(14, 14).TextColor(tok.Accent)
					})
				}
				if isTemp(cn.host.ID) {
					tempBadge(c, m, tok)
				}
				if cn.kind == layout.Terminal {
					a.paneForwards(c, m, tok, cn.host.ID)
				}
				ui.Text(c, a.pv(cn.host.Target())).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(1)
				ui.Spacer(c)
				if isTemp(cn.host.ID) {
					if (IconButton{Icon: icons.Save, Label: m.SaveAsHost(), Size: Small}).Clicked(c, "saveas") {
						t.Focus = n.ID
						a.SaveTempAs(cn.id)
					}
				}
				if cn.kind == layout.Terminal {
					if (IconButton{Icon: icons.Search, Label: m.Find(), Size: Small, Active: a.finding && focused}).Clicked(c, "find") {
						t.Focus = n.ID
						a.finding = !(a.finding && focused)
					}
					if (IconButton{Icon: icons.Folder, Label: m.OpenFilesHere(), Size: Small, Disabled: !remoteOnly(cn)}).Clicked(c, "files") {
						t.Focus = n.ID
						a.OpenFiles(cn.host.ID, openAt{pane: n.ID, edge: layout.Right}, cn)
					}
				} else if cn.kind == layout.Editor {
					a.editorActions(c, t, n, cn, m)
				} else {
					a.transfersButton(c, cn, m)
					a.followButton(c, t, n, cn, m)
					if (IconButton{Icon: icons.Terminal, Label: m.OpenTerminalBeside(), Size: Small}).Clicked(c, "term") {
						t.Focus = n.ID
						a.Connect(cn.host.ID, openAt{pane: n.ID, edge: layout.Right})
					}
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
			if head.Clicked() {
				t.Focus = n.ID
				a.focusReq = cn.id
			}
			ui.Box(c).Height(1).Shrink(0).FillWidth().Background(tok.Line)
			switch cn.kind {
			case layout.Files:
				a.filesBody(c, t, n, cn, pane.Bounds().W, tok, m)
			case layout.Editor:
				a.editorBody(c, t, n, cn, tok, m)
			default:
				a.termElement(c, t, n, cn, &pal, m)
				if st.State == session.Connected {
					h := cn.host
					if cur, ok := a.host(cn.host.ID); ok {
						h = cur
					}
					if a.metricsWanted(h) {
						a.metricsBar(c, cn, tok, m)
					}
				}
			}
			if cn.s != nil && st.State != session.Connected {
				a.banner(c, cn, st, tok, m)
			}
		})
	})
	a.paneDrops(n, cn, pane, tok)
}

func (a *App) paneDrops(n *layout.Node, cn *conn, pane ui.Element, tok theme.Tokens) {
	a.paneBounds[cn.id] = pane.Bounds()
	// Drops: a session from a tab or a pane, a host from the sidebar. An
	// element takes the one type it last asked for, so both go as one.
	v, over := ui.DragOver[paneDrop](pane)
	if s, ok := v.(dragSession); ok && s.ID == cn.id {
		over = false // onto itself, where it is
	}
	edge := layout.Center
	if x, y, ok := pane.PointerPosition(); ok {
		b := pane.Bounds()
		edge = edgeFor(x, y, b.W, b.H)
	}
	if over {
		a.dropPreview = &dropTarget{pane: n.ID, edge: edge}
		pane.DrawOver(func(p *ui.Painter, r ui.Rect) {
			dropPreview(p, r, edge, tok)
		})
	}
	if v, ok := ui.Drop[paneDrop](pane); ok {
		switch v := v.(type) {
		case dragSession:
			if v.ID != cn.id {
				a.ws.Move(v.ID, n.ID, edge)
				a.focusReq = v.ID
			}
		case dragHost:
			a.Connect(v.ID, openAt{pane: n.ID, edge: edge})
		}
		a.saveLayout()
	}
}

// dropPreview shows on a pane r the region a drop at edge would give
// the item: a translucent accent fill of it, inset as a pane is, with an
// accent outline.
func dropPreview(p *ui.Painter, r ui.Rect, edge layout.Edge, tok theme.Tokens) {
	er := edgeRect(r, edge)
	const inset = 4
	er = ui.Rect{X: er.X + inset, Y: er.Y + inset, W: er.W - 2*inset, H: er.H - 2*inset}
	rad := theme.RadiusCard - inset
	p.Fill(er, tok.Accent.Alpha(0.28), rad)
	p.Stroke(er, tok.Accent, rad, 2)
}

func (a *App) termElement(c *ui.Context, t *layout.Tab, n *layout.Node, cn *conn, pal *theme.Terminal, m locales.Messages) {
	tv := cn.tv
	term := ui.Box(c.Key("term")).Grow(1).FillWidth().Focusable().FocusRing(false).Clip().
		Cursor(ui.CursorText).Label(m.Terminal() + " " + cn.host.Label())
	if a.focusReq == cn.id && a.page == pageWorkspace {
		term.Focus()
		a.focusReq = 0
	}
	has := term.Focused()
	tv.focusChanged(has)
	if cn.s.Term.Modes().MouseTracking {
		term.Cursor(ui.CursorDefault) // the program's, not a text's
	}
	if has && t.Focus != n.ID {
		t.Focus = n.ID
	}
	svc := c.Services()
	term.HandleInput(func(ev ui.InputEvent) bool {
		// A left click gives the terminal the keyboard back. A right click
		// must not: focusing here inserts the find bar and the tab dot
		// before the next frame, so the context menu's target id misses.
		if ev.Kind == ui.InputPointerDown && ev.Button == 0 {
			t.Focus = n.ID
			if !has {
				a.focusReq = cn.id
			}
		}
		return tv.input(ev, svc)
	})
	term.TextCaret(tv.caret())
	term.Draw(func(p *ui.Painter, r ui.Rect) { tv.paint(p, r, pal, has) })
	term.ContextMenu(func(menu *ui.Menu) {
		if menu.Item(m.Copy()).Disabled(tv.SelectedText() == "").Chosen() {
			svc.WriteClipboard(tv.SelectedText())
		}
		if menu.Item(m.Paste()).Chosen() {
			tv.Paste(svc.ReadClipboard())
		}
		if menu.Item(m.Find()).Shortcut(appMods, ui.KeyF).Chosen() {
			t.Focus = n.ID
			a.finding = true
		}
		if menu.Item(m.ClearScreen()).Chosen() {
			tv.send([]byte{0x0c})
		}
		menu.Separator()
		if menu.Item(m.SplitRight()).Shortcut(appMods, ui.KeyD).Chosen() {
			t.Focus = n.ID
			a.SplitFocused(layout.Right)
		}
		if menu.Item(m.SplitDown()).Shortcut(appMods, ui.KeyE).Chosen() {
			t.Focus = n.ID
			a.SplitFocused(layout.Bottom)
		}
		if menu.Item(m.OpenFilesBeside()).Shortcut(appMods, ui.KeyO).Disabled(!remoteOnly(cn)).Chosen() {
			t.Focus = n.ID
			a.OpenFilesBeside()
		}
		if menu.Item(m.OpenAIRight()).Shortcut(aiMods, ui.KeyA).Disabled(!remoteOnly(cn)).Chosen() {
			t.Focus = n.ID
			a.OpenAI(cn.host.ID, openAt{pane: n.ID, edge: layout.Right}, cn.id)
		}
		if menu.Item(m.OpenAIBelow()).Disabled(!remoteOnly(cn)).Chosen() {
			t.Focus = n.ID
			a.OpenAI(cn.host.ID, openAt{pane: n.ID, edge: layout.Bottom}, cn.id)
		}
		sel := tv.SelectedText()
		if menu.Item(m.AskAI()).Disabled(sel == "" || !remoteOnly(cn)).Chosen() {
			a.AskAI(cn, sel)
		}
		menu.Separator()
		if menu.Item(m.ClosePane()).Shortcut(appMods, ui.KeyW).Chosen() {
			a.closeSoon(cn.id)
		}
	})
}

func (a *App) banner(c *ui.Context, cn *conn, st session.Status, tok theme.Tokens, m locales.Messages) {
	col := tok.Warning
	if st.State == session.Failed {
		col = tok.Danger
	}
	// A rounded notice floating inside the bottom of the pane card.
	ui.Row(c.Key("banner")).Margin(0, 8, 8, 8).Padding(6, 8, 6, 12).MinHeight(36).Gap(8).AlignItems(ui.Center).
		Radius(theme.RadiusControl).Background(col.Alpha(0.12)).Border(1, col.Alpha(0.22)).Children(func() {
		if st.State == session.Connecting || st.State == session.Reconnecting || st.State == session.Idle {
			ui.Spinner(c)
		} else {
			ui.Icon(c, icons.Warning.SVG).Size(14, 14).Shrink(0).TextColor(col)
		}
		ui.Text(c, a.pv(a.statusText(m, cn.host, st, c.Now()))).FontSize(theme.Footnote).Grow(1).Shrink(1)
		switch st.State {
		case session.Failed, session.Disconnected, session.Exited, session.Reconnecting:
			if session.Classify(st.Err) == session.KindHostChanged {
				break
			}
			if (IconButton{Icon: icons.Reconnect, Label: m.Reconnect(), Size: Small}).Clicked(c, "reconnect") {
				cn.handled = nil
				cn.s.Reconnect()
			}
		}
	})
	if st.State == session.Reconnecting {
		d := st.Retry.Sub(c.Now()) % time.Second
		if d <= 0 {
			d = time.Second
		}
		c.After(d + 50*time.Millisecond)
	}
}
