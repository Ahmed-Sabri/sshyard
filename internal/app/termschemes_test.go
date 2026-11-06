package app

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/theme"
	"github.com/egoist/mygo/ui"
)

// itermcolors writes sc as iTerm2 does.
func itermcolors(sc theme.Scheme) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>`)
	color := func(key, hex string) {
		v, _ := strconv.ParseUint(hex[1:], 16, 32)
		fmt.Fprintf(&b, `<key>%s</key><dict><key>Blue Component</key><real>%g</real><key>Green Component</key><real>%g</real><key>Red Component</key><real>%g</real></dict>`,
			key, float64(v&0xFF)/255, float64(v>>8&0xFF)/255, float64(v>>16)/255)
	}
	for i, h := range sc.ANSI {
		color(fmt.Sprintf("Ansi %d Color", i), h)
	}
	color("Foreground Color", sc.Foreground)
	color("Background Color", sc.Background)
	b.WriteString(`</dict></plist>`)
	return []byte(b.String())
}

// TestTerminalSchemes: the terminal settings choose a built-in scheme,
// import an .itermcolors file and delete it again.
func TestTerminalSchemes(t *testing.T) {
	a, tt := newTestApp(t)
	a.openSettings(prefTerminal)
	tt.SetSize(1200, 2600)
	tt.Frame()
	for _, s := range []string{"Color Scheme", "Follow Appearance", "Dracula", "Solarized Light", "Catppuccin Mocha", "Import a Color Scheme"} {
		if !tt.HasText(s) {
			t.Fatalf("no %q: %q", s, tt.Texts())
		}
	}
	tok := theme.DarkTokens()
	if a.termPalette(tok) != tok.Terminal {
		t.Error("no scheme chosen, yet not the app's colors")
	}
	mustClick(t, tt, "Dracula")
	tt.Frame()
	if a.settings.TerminalScheme != "dracula" || a.termPalette(tok).Background != ui.Hex("#282A36") {
		t.Fatalf("dracula not chosen: %q", a.settings.TerminalScheme)
	}

	sumi := theme.Schemes[3]
	sumi.Background = "#1E1B18"
	a.addScheme(en, "/tmp/Sumi Ink.itermcolors", itermcolors(sumi), nil)
	tt.Frame()
	if a.settings.TerminalScheme != theme.CustomPrefix+"Sumi Ink" || a.termPalette(tok).Background != ui.Hex("#1E1B18") {
		t.Fatalf("import not chosen: %q", a.settings.TerminalScheme)
	}
	if !tt.HasText("Sumi Ink") || !tt.HasText("Imported") {
		t.Fatalf("imported scheme not listed: %q", tt.Texts())
	}
	a.addScheme(en, "/tmp/notes.itermcolors", []byte("hello"), nil)
	tt.Frame()
	if !tt.HasText("notes.itermcolors is not an .itermcolors color scheme.") || len(a.settings.TerminalSchemes) != 1 {
		t.Errorf("a bad file: %q", tt.Texts())
	}
	mustClick(t, tt, "Delete Color Scheme")
	tt.Frame()
	if len(a.settings.TerminalSchemes) != 0 || a.settings.TerminalScheme != "" || tt.HasText("Imported") {
		t.Errorf("not deleted: %+v", a.settings.TerminalSchemes)
	}
}

// TestPaneHeadOnScheme: a pane's header is drawn on the terminal's scheme,
// so a dark scheme in light mode, or a light one in dark mode, gives its
// title the other appearance's colors, which read on it.
func TestPaneHeadOnScheme(t *testing.T) {
	a, tt := newTestApp(t)
	if _, err := a.OpenLocalTerminal(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		dark   bool
		scheme string
	}{{false, "dracula"}, {true, "solarized-light"}, {false, ""}, {true, ""}} {
		tt.SetDark(tc.dark)
		a.settings.TerminalScheme = tc.scheme
		tt.Frame()
		tt.Frame()
		bg := a.termPalette(theme.For(a.settings.Theme, tc.dark)).Background
		r := a.paneBounds[a.focused().id]
		img := tt.Image()
		best := 0.0
		for y := int(r.Y) + 4; y < int(r.Y)+paneHeadHeight-4; y++ {
			for x := int(r.X) + 50; x < int(r.X+r.W)-10; x++ {
				p := img.RGBAAt(x, y)
				best = max(best, theme.Contrast(ui.Color{R: p.R, G: p.G, B: p.B, A: 255}, bg))
			}
		}
		if best < 4.5 {
			t.Errorf("dark=%v scheme=%q: header's best contrast on %v is %.2f, want ≥ 4.5", tc.dark, tc.scheme, bg, best)
		}
	}
}
