package app

import (
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/egoist/mygo/ui"
)

// drawBackground paints one image in window coordinates, before all regions.
// Each region owns its tint so overlapping default fills never multiply it.
func (a *App) drawBackground(c *ui.Context, tok theme.Tokens) {
	if !a.hasBackground() {
		return
	}
	bmp := a.background.bmp
	fit := ui.Cover
	if a.effectiveBackground().Fit == "contain" {
		fit = ui.Contain
	}
	z := c.Zoom()
	c.Root().Background(tok.Canvas).Draw(func(p *ui.Painter, r ui.Rect) {
		if a.zoom.desktop > 0 {
			a.zoom.device = p.Scale() / z
		}
		p.Image(bmp, r, fit)
	})
}

func (a *App) chromeFill(col ui.Color) ui.Color {
	if a.hasBackground() {
		col.A = 255
		return col.Alpha(float32(a.effectiveBackground().ChromeOpacity) / 100)
	}
	return col
}

func (a *App) backgroundContentFill(col ui.Color) ui.Color {
	if a.hasBackground() {
		col.A = 255
		return col.Alpha(float32(a.effectiveBackground().ContentOpacity) / 100)
	}
	return col
}

func (a *App) contentRegionFill(col ui.Color) ui.Color {
	if a.hasBackground() && (a.page == pageSettings || len(a.ws.Tabs) == 0) {
		return a.backgroundContentFill(col)
	}
	return ui.Transparent
}

func (a *App) backgroundInspectorFill(col ui.Color) ui.Color {
	if a.hasBackground() {
		return a.backgroundContentFill(col)
	}
	return a.contentFill(col, canvasAlpha)
}

func (a *App) editorWindowFill(col ui.Color) ui.Color {
	if a.hasBackground() {
		return a.backgroundContentFill(col)
	}
	return ui.Transparent
}

func (a *App) editorChromeFill(col ui.Color) ui.Color {
	if a.hasBackground() {
		return a.chromeFill(col)
	}
	return ui.Transparent
}

// contentSurfaceFill leaves the owning content region as the only image tint;
// cards must not hide the image or compound that region's configured opacity.
// Controls and overlays retain their own readable fills.
func (a *App) contentSurfaceFill(col ui.Color) ui.Color {
	if a.hasBackground() || a.clearContent {
		return ui.Transparent
	}
	return col
}

func (a *App) contentCard(c *ui.Context, tok theme.Tokens) ui.Element {
	return card(c, tok).Background(a.contentSurfaceFill(tok.Inset))
}
