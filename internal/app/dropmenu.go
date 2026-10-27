package app

import (
	"runtime"
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/egoist/mygo/ui"
)

// menuEntry is an entry of a menu, the same in the app's drop-down menus
// (menuButton), drawn with its icon, and in a context menu of the system
// (nativeMenu).
type menuEntry struct {
	Icon *icons.Icon
	// Lead, when set, builds what shows in place of the icon, 16 DIPs
	// square, as a host's tile.
	Lead  func()
	Label string
	// Mods and Key are the shortcut shown beside it; Key 0 shows none.
	Mods ui.Modifiers
	Key  ui.Key
	// Checked marks the entry with a check, Danger tints it red.
	Disabled, Checked, Danger bool
	// Sep makes the entry a separator.
	Sep bool
	// Sub are the entries of its submenu, nil for none.
	Sub []menuEntry
	// Do runs as the entry is chosen.
	Do func()
}

// menuSep is a separator between entries.
var menuSep = menuEntry{Sep: true}

// nativeMenu fills menu, one of the system's, with entries.
func nativeMenu(menu *ui.Menu, entries []menuEntry) {
	for _, e := range entries {
		switch {
		case e.Sep:
			menu.Separator()
		case e.Sub != nil:
			menu.Submenu(e.Label, func(sub *ui.Menu) { nativeMenu(sub, e.Sub) }).Disabled(e.Disabled)
		default:
			it := menu.Item(e.Label).Disabled(e.Disabled).Checked(e.Checked)
			if e.Key != 0 {
				it.Shortcut(e.Mods, e.Key)
			}
			if it.Chosen() && e.Do != nil {
				e.Do()
			}
		}
	}
}

// menuWidth is the narrowest a drop-down menu is; menuHeight the most it
// grows to before it scrolls.
const (
	menuWidth  = 200
	menuHeight = 480
)

// dropMenu is the drop-down menu open, at most one: the key of its
// button, the entry whose submenu shows (-1 for none), and the level, 0
// the menu and 1 its submenu, whose first entry takes the focus in the
// next frame as it opens, for the arrows (-1 for none). The focus shows
// on it only when it came from the keyboard.
type dropMenu struct {
	key   string
	sub   int
	focus int
}

// menuButton is b opening a drop-down menu of entries below it, under
// key, which is unique in the window: the app's own, drawn as its
// popovers are, an icon on each entry. entries runs only while it shows.
func (a *App) menuButton(c *ui.Context, key string, b IconButton, entries func() []menuEntry) ui.Element {
	open := a.menu.key == key
	b.Active = b.Active || open
	el := b.Show(c, key).Role(ui.RoleMenuButton).Expanded(open)
	if el.Clicked() && !b.Disabled {
		if open {
			a.menu = dropMenu{}
		} else {
			a.menu = dropMenu{key: key, sub: -1, focus: 0}
		}
		open = !open
	}
	if !open {
		return el
	}
	tok := currentTokens(c)
	ui.PopoverBase(c.Key("menu:"+key), el, &open, func(panel ui.Element) {
		panel.Margin(4, 0, 0, 0)
		a.menuPanel(c, tok, panel, entries(), 0)
	})
	if !open {
		a.menu = dropMenu{}
	}
	return el
}

// menuPanel lays out the panel of a drop-down menu at level, 0 the menu
// and 1 a submenu of it, with its entries.
func (a *App) menuPanel(c *ui.Context, tok theme.Tokens, panel ui.Element, entries []menuEntry, level int) {
	panel.Role(ui.RoleMenu).MinWidth(menuWidth).Padding(5).Radius(theme.RadiusCard).Background(tok.Inset).
		Border(1, tok.GlassEdge).FocusGroup(ui.Vertical)
	tok.Lift.On(panel)
	focus := a.menu.focus == level
	if focus {
		a.menu.focus = -1
	}
	panel.Children(func() {
		ui.Scroll(c.Key("scroll")).MaxHeight(menuHeight).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				for i, e := range entries {
					if e.Sep {
						ui.Box(c.Key("sep:"+strconv.Itoa(i))).Height(1).Margin(4, 8).Background(tok.Line)
						continue
					}
					row := a.menuRow(c, tok, e, i, level)
					if focus && !e.Disabled {
						row.Focus()
						focus = false
					}
				}
			})
		})
	})
}

// menuRow is the i-th entry e of a drop-down menu at level: lit in the
// accent's tint under the pointer or the keyboard's focus, as IconButtons
// are; its submenu after it, which the pointer or the right arrow opens.
func (a *App) menuRow(c *ui.Context, tok theme.Tokens, e menuEntry, i, level int) ui.Element {
	row := ui.ButtonBase(c.Key("mi:"+strconv.Itoa(i))).Role(ui.RoleMenuItem).FillWidth().Justify(ui.Start).
		Padding(6, 8).Gap(10).Radius(theme.RadiusSmall).FocusRing(false).Disabled(e.Disabled)
	if e.Checked {
		row.Role(ui.RoleMenuItemCheckBox).Checked(true)
	}
	hue := tok.Accent
	if e.Danger {
		hue = tok.Danger
	}
	strong := float32(0.12)
	if tok.Dark {
		strong = 0.22
	}
	subOpen := level == 0 && e.Sub != nil && a.menu.sub == i
	if level == 0 && row.Hovered() && a.menu.sub != i && !e.Disabled {
		// The pointer moves the submenu shown onto its entry's, or
		// closes it over another.
		a.menu.sub = -1
		if e.Sub != nil {
			a.menu.sub = i
		}
		c.Invalidate()
	}
	lit := !e.Disabled && (row.Hovered() || row.FocusVisible() || subOpen)
	fg, muted := tok.Text, tok.TextMuted
	switch {
	case e.Disabled:
		row.Opacity(0.4)
	case lit && row.Pressed():
		row.Background(hue.Alpha(strong * 1.8)).Cursor(ui.CursorPointer)
		fg, muted = hue, hue
	case lit:
		row.Background(hue.Alpha(strong)).Cursor(ui.CursorPointer)
		fg, muted = hue, hue
	case e.Danger:
		fg, muted = hue, hue
	}
	row.Children(func() {
		ic := ui.Box(c).Size(16, 16).Shrink(0)
		switch {
		case e.Lead != nil:
			ic.Children(e.Lead)
		case e.Icon != nil:
			ic.Children(func() { ui.Icon(c, e.Icon.SVG).Size(16, 16).TextColor(muted) })
		}
		ui.Text(c, e.Label).FontSize(theme.Body).TextColor(fg).SingleLine().Grow(1).Shrink(1).MinWidth(0)
		if e.Key != 0 {
			ui.Text(c, shortcutText(runtime.GOOS, e.Mods, e.Key)).FontSize(theme.Footnote).TextColor(muted).SingleLine().
				Shrink(0).MarginStart(theme.Space(4))
		}
		switch {
		case e.Sub != nil:
			ui.Icon(c, icons.Submenu.SVG).Size(14, 14).Shrink(0).TextColor(muted)
		case e.Checked:
			ui.Icon(c, icons.Check.SVG).Size(14, 14).Shrink(0).TextColor(tok.Accent)
		}
	})
	if e.Disabled {
		return row
	}
	if e.Sub != nil {
		if row.Clicked() || row.Shortcut(0, ui.KeyRight) {
			a.menu.sub, a.menu.focus = i, 1
			subOpen = true
		}
		if subOpen {
			open := true
			ui.PopoverBase(c.Key("sub:"+strconv.Itoa(i)), row, &open, func(panel ui.Element) {
				panel.AttachTo(row, ui.AnchorTopEnd, ui.AnchorTopStart).Margin(0, 0, 0, 6).Top(-6)
				a.menuPanel(c, tok, panel, e.Sub, 1)
				if panel.OverlayShortcut(0, ui.KeyLeft) {
					open = false
					row.Focus()
				}
			})
			if !open && a.menu.sub == i {
				a.menu.sub = -1
			}
		}
		return row
	}
	if row.Clicked() {
		a.menu = dropMenu{}
		if e.Do != nil {
			e.Do()
		}
	}
	return row
}

// shortcutText is a shortcut as a menu shows it on goos: "⌘K" on macOS,
// "Ctrl+Shift+K" elsewhere.
func shortcutText(goos string, mods ui.Modifiers, key ui.Key) string {
	sep := "+"
	if goos == "darwin" {
		sep = ""
	}
	return strings.Join(keyCaps(goos, mods, key), sep)
}
