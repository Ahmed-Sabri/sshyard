package app

import (
	"context"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/updater"
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
// the sidebar room for the traffic lights (macOS); over the content the
// tabs, joining it, and at the end the app's own buttons, then the
// window's buttons where the app draws them. Every control in it centers
// on the band's middle, the traffic lights' line (barLine).
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
		// Level with the sidebar, and its separator, below, so that the
		// tabs start where the content does.
		ui.Box(c.Key("tblead")).Width(max(a.sbWidth+1, bar.Left)).Shrink(0).FillHeight()
		a.tabStrip(c, m, tok)
		a.windowButtons(c, m, tok)
	})
}

// appButtons are the app's own IconButtons in the title bar: updates, its
// source on GitHub and the settings. Updates show in the accent while one
// is to install; a click checks again, unless one is under way or found,
// and shows where the settings tell of it.
func (a *App) appButtons(c *ui.Context, m locales.Messages) {
	st := a.upd.Status()
	found := st.State == updater.Available || st.State == updater.Ready
	label := m.CheckForUpdates()
	if found || st.State == updater.Checking || st.State == updater.Downloading {
		label = updateText(m, st)
	}
	if (IconButton{Icon: icons.Update, Label: label, Active: found}).Clicked(c, "update") {
		switch st.State {
		case updater.Disabled, updater.Checking, updater.Downloading, updater.Available, updater.Ready:
		default:
			go a.upd.Check(context.Background())
		}
		a.openSettings(prefAbout)
	}
	if iconButton(c, "github", icons.GitHub, m.GitHubRepo()) {
		c.OpenURL(repoURL)
	}
	on := a.page == pageSettings
	if (IconButton{Icon: icons.Settings, On: icons.SettingsOn, Label: m.Settings(), Active: on, Toggle: true}).Clicked(c, "settings") {
		if on {
			a.closeSettings()
		} else {
			a.openSettings(a.prefNow())
		}
	}
}

// barLine is a row of the title bar's controls, centered on the band's
// middle, where the traffic lights are and a tab's title is.
func barLine(c *ui.Context, key string) ui.Element {
	return ui.Row(c.Key(key)).FillHeight().Shrink(0).AlignItems(ui.Center)
}

// windowButtons minimize, maximize or restore, and close the window, where
// the app draws them (WindowControls).
func (a *App) windowButtons(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	w := a.win
	if w == nil {
		return
	}
	barLine(c, "winbtns").Gap(theme.Space(1)).Padding(0, 10, 0, 0).Children(func() {
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
