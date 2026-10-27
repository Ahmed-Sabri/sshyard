package app

import (
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// WindowControls are the window's buttons, for a window whose view draws
// them itself (Linux, Windows, where the system's are hidden): main hands
// them over once the window is made. On macOS the traffic lights stay and
// there are none.
type WindowControls struct {
	Minimize, Maximize, Unmaximize, Close func()
}

// SetWindow gives the title bar the window's buttons.
func (a *App) SetWindow(w WindowControls) { a.post(func() { a.win = &w }) }

// SetMaximized says whether the window is maximized, for the button that
// maximizes or restores it.
func (a *App) SetMaximized(on bool) { a.post(func() { a.maximized = on }) }

// titleBar is the window's one band across the top, which drags it: over
// the sidebar the app's name after the traffic lights (macOS), or the way
// back from the settings; over the content the tabs and the actions on the
// focused session; at the end the window's buttons where the app draws
// them.
func (a *App) titleBar(c *ui.Context, m locales.Messages, tok theme.Tokens, clear bool) {
	bar := c.TitleBar()
	row := ui.Row(c.Key("titlebar")).Height(bandHeight(c)).Shrink(0).FillWidth().AlignItems(ui.Center).DragWindow()
	if !clear {
		row.Background(tok.Sidebar)
	}
	// Its foot, which the active tab covers, joining the content.
	row.Draw(func(p *ui.Painter, r ui.Rect) {
		y := r.Y + r.H - 0.5
		p.Line(r.X, y, r.X+r.W, y, 1, tok.Line)
	})
	row.Children(func() {
		// Level with the sidebar, and its separator, below.
		ui.Row(c.Key("tblead")).Width(a.sbWidth+1).Shrink(0).FillHeight().Padding(0, 10, 0, max(bar.Left+4, 14)).Gap(8).
			AlignItems(ui.Center).Children(func() {
			if a.page == pageSettings {
				if (IconButton{Icon: icons.Back, Label: m.BackToSessions()}).Clicked(c, "back") {
					a.closeSettings()
				}
				ui.Text(c, m.Settings()).FontSize(theme.Title3).FontWeight(600).SingleLine().Shrink(1)
				return
			}
			tile(c, icons.Terminal, theme.Brand, 22)
			ui.Text(c, m.AppName()).FontSize(theme.Body).FontWeight(600).SingleLine().Shrink(1)
		})
		a.tabStrip(c, m, tok)
		a.windowButtons(c, m, tok)
	})
}

// windowButtons minimize, maximize or restore, and close the window, where
// the app draws them (WindowControls).
func (a *App) windowButtons(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	w := a.win
	if w == nil {
		return
	}
	ui.Row(c.Key("winbtns")).Shrink(0).Gap(theme.Space(1)).Padding(0, 10, 0, 0).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Width(1).Height(20).Shrink(0).Margin(0, 6, 0, 0).Background(tok.Line)
		if iconButton(c, "winmin", icons.WinMinimize, m.WindowMinimize()) {
			w.Minimize()
		}
		if a.maximized {
			if iconButton(c, "winmax", icons.WinRestore, m.WindowRestore()) {
				w.Unmaximize()
			}
		} else if iconButton(c, "winmax", icons.WinMaximize, m.WindowMaximize()) {
			w.Maximize()
		}
		if (IconButton{Icon: icons.Close, Label: m.WindowClose(), Danger: true}).Clicked(c, "winclose") {
			w.Close()
		}
	})
}
