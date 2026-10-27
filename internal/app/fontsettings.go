package app

import (
	"github.com/Licoy/sshyard/internal/fonts"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// listFonts lists the installed families; tests replace it.
var listFonts = fonts.List

// fontState is what the font settings choose among and what is in effect.
type fontState struct {
	list            []fonts.Family
	loading, loaded bool
	// ui and term are the families in effect: the chosen ones, or ""
	// (the system's) for one not installed.
	ui, term string
	// missing are chosen families that are not installed, to tell of.
	missing []string
	// the combobox choices
	uiSel, termSel string
}

// loadFonts lists the installed families off the UI thread, once.
func (a *App) loadFonts() {
	if a.fonts.loading {
		return
	}
	a.fonts.loading = true
	list := listFonts
	go func() {
		l := list()
		a.post(func() {
			a.fonts.list, a.fonts.loaded = l, true
			a.resolveFonts(true, true)
		})
	}()
}

// resolveFonts puts the chosen families in effect, those installed: a
// family the system lacks falls back to the system's font, and when
// reportUI or reportTerm, the user hears of it.
func (a *App) resolveFonts(reportUI, reportTerm bool) {
	pick := func(name string, report bool) string {
		// Until the list is in, a family is taken as installed: text
		// falls back glyph by glyph to the system's fonts anyway.
		if name == "" || !a.fonts.loaded || fonts.Has(a.fonts.list, name) {
			return name
		}
		if report {
			a.fonts.missing = append(a.fonts.missing, name)
		}
		return ""
	}
	a.fonts.ui = pick(a.settings.UIFont, reportUI)
	a.fonts.term = pick(a.settings.TerminalFont, reportTerm)
	fam := a.termFamily()
	for _, c := range a.conns {
		if c.tv != nil {
			c.tv.family = fam
		}
	}
}

// termFamily is the terminal's font: the chosen family, then the system's
// monospaced font for what it lacks (CJK, symbols), then the system's
// fallbacks.
func (a *App) termFamily() string {
	if a.fonts.term == "" {
		return "monospace"
	}
	return a.fonts.term + ", monospace"
}

// fontToasts tells of chosen families that are not installed.
func (a *App) fontToasts(c *ui.Context, m locales.Messages) {
	for _, n := range a.fonts.missing {
		c.AddToast(ui.Toast{Title: m.ErrFontMissing(n), Type: "error"})
	}
	a.fonts.missing = nil
}

// fontOptions are a font setting's choices: the system's default first,
// then the families.
func fontOptions(def string, fs []fonts.Family) []string {
	opts := make([]string, 0, len(fs)+1)
	opts = append(opts, def)
	for _, f := range fs {
		opts = append(opts, f.Name)
	}
	return opts
}

// fontPicker is a font setting's combobox, which typing filters; it
// reports a new family, "" for the system's.
func (a *App) fontPicker(c *ui.Context, key, label, def string, sel *string, cur string, fs []fonts.Family) (string, bool) {
	*sel = cur
	if cur == "" {
		*sel = def
	}
	if !ui.Combobox(c.Key(key), sel, fontOptions(def, fs)).Label(label).Width(220).Changed() {
		return cur, false
	}
	if *sel == def {
		return "", true
	}
	return *sel, true
}

// uiFontPreview and termFontPreview show the fonts in effect: a line of the interface's, and
// a few of the terminal's, in its colors at its size, CJK included.
func (a *App) uiFontPreview(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	ui.Column(c.Key("uipreview")).FillWidth().Padding(10, 14).Gap(4).Children(func() {
		el := ui.Text(c, m.FontPreviewText()).FontSize(theme.Title3)
		if a.fonts.ui != "" {
			el.Font(a.fonts.ui)
		}
		el = ui.Text(c, "The quick brown fox jumps over the lazy dog · 敏捷的棕色狐狸 0123456789").FontSize(theme.Body).TextColor(tok.TextMuted)
		if a.fonts.ui != "" {
			el.Font(a.fonts.ui)
		}
	})
}

func (a *App) termFontPreview(c *ui.Context, tok theme.Tokens) {
	pal := tok.Terminal
	fam := a.termFamily()
	size := float32(a.settings.TerminalFontSize)
	line := func(parts ...ui.Span) {
		for i := range parts {
			parts[i].Font = fam
		}
		ui.RichText(c, parts...).FontSize(size)
	}
	// The padding is the card's inset: a margin would add to the width.
	ui.Column(c.Key("termpreview")).FillWidth().Padding(0, 14, 12, 14).Children(func() {
		ui.Column(c.Key("box")).FillWidth().Padding(12, 14).Gap(2).Radius(theme.RadiusControl).
			Background(pal.Background).TextColor(pal.Foreground).Border(1, tok.Line).Children(func() {
			line(ui.Span{Text: "demo@sshyard", Color: pal.ANSI[10]}, ui.Span{Text: ":"}, ui.Span{Text: "~/projects", Color: pal.ANSI[12]}, ui.Span{Text: "$ ls -la"})
			line(ui.Span{Text: "drwxr-xr-x  4 demo staff  128 "}, ui.Span{Text: "src/", Color: pal.ANSI[12], Weight: 700})
			line(ui.Span{Text: "-rw-r--r--  1 demo staff 2.1K 你好世界.md  ", Color: pal.Foreground}, ui.Span{Text: "0O 1lI {}[] => != ✓", Color: pal.ANSI[11]})
		})
	})
}
