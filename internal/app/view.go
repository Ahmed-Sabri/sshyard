package app

import (
	"time"

	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// View builds the window.
func (a *App) View(c *ui.Context) {
	a.mu.Lock()
	a.svc, a.live = c.Services(), true
	a.mu.Unlock()
	// The same clock as the toasts, so a snapshot and its Undo end together.
	a.clip.now = func() time.Time { return c.Now() }
	a.drain()
	a.commitMetricsMount()
	a.syncMetrics()
	for _, t := range a.toasts {
		c.AddToast(t(locales.In(c.Locale())))
	}
	a.toasts = nil
	a.wakeUndo(c)
	a.followCwd(c.Now())

	base := *c.Theme()
	base.Font = a.fonts.ui
	tok := theme.For(a.settings.Theme, base.Dark)
	c.SetTheme(tok.Apply(base))
	m := locales.In(c.Locale())
	a.fontToasts(c, m)

	a.shortcuts(c)
	// The window: an edge-to-edge sidebar on the window's material (or
	// its solid fill) holding the hosts, or the settings' pages while they
	// show, beside the canvas of the content. The content's top band is
	// the unified toolbar and its bottom one the status bar; in the
	// workspace a rail of IconButtons runs down its right edge, with the
	// panel it opens beside the panes.
	clear := c.Vibrancy()
	if clear {
		c.Root().Background(ui.Transparent)
	} else {
		c.Root().Background(tok.Sidebar)
	}
	ui.Row(c).Fill().TextColor(tok.Text).Children(func() {
		sb := ui.Box(c.Key("sidebar")).Width(a.sbWidth).FillHeight()
		if !clear {
			sb.Background(tok.Sidebar)
		}
		sb.Children(func() {
			c.SetTheme(tok.SidebarTheme(base, clear))
			if a.page == pageSettings {
				a.settingsNav(c, m, tok)
			} else {
				a.sidebarView(c, m, tok)
			}
			c.SetTheme(tok.Apply(base))
			if clear {
				c.Root().Background(ui.Transparent)
			}
		})
		ui.Box(c).Width(1).FillHeight().Background(tok.Line)
		ui.Column(c.Key("main")).Grow(1).Shrink(1).FillHeight().Background(tok.Canvas).Children(func() {
			a.tabStrip(c, m, tok)
			ui.Row(c.Key("body")).Grow(1).Shrink(1).FillWidth().MinHeight(0).Children(func() {
				ui.Column(c.Key("content")).Grow(1).Shrink(1).MinWidth(0).FillHeight().Children(func() {
					switch a.page {
					case pageSettings:
						a.settingsPage(c, m, tok)
					default:
						a.terminalPage(c, m, tok)
					}
				})
				if a.page != pageWorkspace {
					return
				}
				if a.inspector != "" {
					ui.Box(c).Width(1).FillHeight().Shrink(0).Background(tok.Line)
					a.inspectorView(c, m, tok)
				}
				ui.Box(c).Width(1).FillHeight().Shrink(0).Background(tok.GlassEdge)
				a.railView(c, m, tok)
			})
			a.statusBar(c, m, tok)
		})
	})
	a.editorView(c, m)
	a.credView(c, m)
	a.proxyView(c, m)
	a.fwdView(c, m)
	a.fwdDeleteView(c, m)
	a.installView(c, m)
	a.quickView(c, m)
	a.importView(c, m)
	a.promptsView(c, m)
	a.oscAskView(c, m)
	a.aiSheets(c, m)
}
