package app

import (
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/vt"
	"github.com/egoist/mygo/ui"
)

var cubeLevels = [6]uint8{0, 95, 135, 175, 215, 255}

// paletteColor is color i of xterm's 256: the theme's 16, the 6×6×6 cube
// and the 24 grays.
func paletteColor(i uint8, pal *theme.Terminal) ui.Color {
	switch {
	case i < 16:
		return pal.ANSI[i]
	case i < 232:
		n := i - 16
		return ui.RGB(cubeLevels[n/36], cubeLevels[n/6%6], cubeLevels[n%6])
	}
	g := 8 + 10*(i-232)
	return ui.RGB(g, g, g)
}

// cellColors resolves a cell's colors against the palette: bold brightens
// the eight basic colors, inverse swaps, faint dims and hidden hides.
func cellColors(c vt.Cell, pal *theme.Terminal) (fg, bg ui.Color, bgSet bool) {
	fg, bg = pal.Foreground, pal.Background
	if i, ok := c.FG.Index(); ok {
		if c.Attr&vt.Bold != 0 && i < 8 {
			i += 8
		}
		fg = paletteColor(i, pal)
	} else if r, g, b, ok := c.FG.RGB(); ok {
		fg = ui.RGB(r, g, b)
	}
	if i, ok := c.BG.Index(); ok {
		bg, bgSet = paletteColor(i, pal), true
	} else if r, g, b, ok := c.BG.RGB(); ok {
		bg, bgSet = ui.RGB(r, g, b), true
	}
	if c.Attr&vt.Inverse != 0 {
		fg, bg, bgSet = bg, fg, true
	}
	if c.Attr&vt.Faint != 0 {
		fg = fg.Mix(bg, 0.45)
	}
	if c.Attr&vt.Hidden != 0 {
		fg = bg
	}
	return fg, bg, bgSet
}
