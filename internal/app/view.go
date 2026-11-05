package app

import (
	"time"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// View builds the window.
func (a *App) View(c *ui.Context) {
	a.mu.Lock()
	a.svc, a.live = c.Services(), true
	a.mu.Unlock()
	// Native callbacks run between frames and use the real clock; the Tester
	// uses its frame clock so clipboard snapshots and toast Undo expire together.
	if a.p.Post == nil {
		a.clip.now = func() time.Time { return c.Now() }
	}
	a.drain()
	a.applyZoom(c, true)
	for _, t := range a.toasts {
		c.AddToast(t(locales.In(c.Locale())))
	}
	a.toasts = nil
	if !a.ready() {
		a.unlockView(c)
		return
	}
	a.commitMetricsMount()
	a.syncMetrics()
	a.wakeUndo(c)
	a.followCwd(c)
	a.sendStartCmds()
	for _, cn := range a.conns {
		if cn.fs != nil {
			a.pendingOpens(cn)
		}
	}

	base := *c.Theme()
	base.Font = a.fonts.ui
	tok := theme.For(a.settings.Theme, base.Dark)
	c.SetTheme(tok.Apply(base))
	m := locales.In(c.Locale())
	a.localName = m.LocalTerminal()
	a.fontToasts(c, m)

	a.shortcuts(c)
	// The window: one title bar across the top, on the window's material
	// (or its solid fill) as the sidebar under it, which holds the hosts,
	// or the settings' pages while they show, beside the canvas of the
	// content. In the workspace the content's bottom band is the status
	// bar, and a rail of IconButtons runs down its right edge, with the
	// panel it opens beside the panes.
	// Each part shows the material as the appearance settings say: the
	// sidebar and the title bar (clear), and the content (clearContent),
	// through a lighter fill of its own.
	vibrant := c.Vibrancy()
	clear := vibrant && a.settings.ChromeMaterial != settings.MaterialOpaque
	a.clearContent = vibrant && a.settings.ContentMaterial == settings.MaterialTranslucent
	if vibrant {
		c.Root().Background(ui.Transparent)
	} else {
		c.Root().Background(tok.Sidebar)
	}
	ui.Column(c).Fill().TextColor(tok.Text).Children(func() {
		a.titleBar(c, m, tok, clear)
		if a.showSecretsStatus() && a.page != pageSettings {
			a.secretsBanner(c, m, tok)
		}
		ui.Row(c.Key("frame")).Grow(1).Shrink(1).MinHeight(0).FillWidth().Children(func() {
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
				if vibrant {
					c.Root().Background(ui.Transparent)
				}
			})
			ui.Box(c).Width(1).FillHeight().Background(tok.Line)
			ui.Column(c.Key("main")).Grow(1).Shrink(1).MinWidth(0).FillHeight().Background(a.contentFill(tok.Canvas, canvasAlpha)).Children(func() {
				ui.Row(c.Key("body")).Grow(1).Shrink(1).MinWidth(0).FillWidth().MinHeight(0).Children(func() {
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
					if len(a.panels) > 0 {
						ui.Box(c).Width(1).FillHeight().Shrink(0).Background(tok.Line)
						a.inspectorView(c, m, tok)
					}
					ui.Box(c).Width(1).FillHeight().Shrink(0).Background(tok.GlassEdge)
					a.railView(c, m, tok)
				})
				if a.page == pageWorkspace {
					a.statusBar(c, m, tok)
				}
			})
		})
	})
	a.editorView(c, m)
	a.groupView(c, m)
	a.tabRenameView(c, m)
	a.snippetSheets(c, m)
	a.credView(c, m)
	a.proxyView(c, m)
	a.targetView(c, m)
	a.remoteListView(c, m)
	a.backupView(c, m)
	a.defaultPasswordView(c, m)
	a.fwdView(c, m)
	a.fwdDeleteView(c, m)
	a.highlightView(c, m)
	a.installView(c, m)
	a.quickView(c, m)
	a.paletteView(c, m)
	a.importView(c, m)
	a.migrationView(c, m)
	a.promptsView(c, m)
	a.oscAskView(c, m)
	a.aiSheets(c, m)
	a.aiAttachmentPreviewView(c, m, tok)
	a.contextMenuView(c)
	a.unlockModal(c, m, tok)
}
