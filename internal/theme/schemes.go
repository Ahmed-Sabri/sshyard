package theme

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// Scheme is a terminal color scheme, its colors as #RRGGBB: one of the
// built-in Schemes, or one the user imported (kept in the settings).
// Search matches keep the app's colors.
type Scheme struct {
	// ID names the scheme in the settings: a built-in's own, or
	// CustomPrefix and the name of an imported one.
	ID         string `json:"id"`
	Name       string `json:"name"`
	Foreground string `json:"foreground"`
	Background string `json:"background"`
	// Cursor, CursorText and Selection may be empty: the foreground, the
	// background and the foreground faded stand in.
	Cursor     string `json:"cursor,omitempty"`
	CursorText string `json:"cursorText,omitempty"`
	Selection  string `json:"selection,omitempty"`
	// ANSI are the 16 colors of SGR 30–37 and 90–97.
	ANSI [16]string `json:"ansi"`
}

// CustomPrefix starts the ID of an imported scheme.
const CustomPrefix = "custom:"

// Custom reports a scheme the user imported.
func (s Scheme) Custom() bool { return strings.HasPrefix(s.ID, CustomPrefix) }

// Valid reports a scheme with a name and colors that all parse.
func (s Scheme) Valid() bool {
	if strings.TrimSpace(s.Name) == "" || s.ID == "" {
		return false
	}
	for _, h := range s.ANSI {
		if _, ok := parseHex(h); !ok {
			return false
		}
	}
	for _, h := range []string{s.Foreground, s.Background} {
		if _, ok := parseHex(h); !ok {
			return false
		}
	}
	for _, h := range []string{s.Cursor, s.CursorText, s.Selection} {
		if _, ok := parseHex(h); h != "" && !ok {
			return false
		}
	}
	return true
}

// Dark reports a scheme whose background is dark.
func (s Scheme) Dark() bool {
	c, _ := parseHex(s.Background)
	return 0.2126*float32(c.R)+0.7152*float32(c.G)+0.0722*float32(c.B) < 128
}

// Terminal is the scheme as the terminal's palette; base gives what the
// scheme does not set (the search matches).
func (s Scheme) Terminal(base Terminal) Terminal {
	color := func(h string, or ui.Color) ui.Color {
		if c, ok := parseHex(h); ok {
			return c
		}
		return or
	}
	t := base
	t.Foreground = color(s.Foreground, base.Foreground)
	t.Background = color(s.Background, base.Background)
	t.Cursor = color(s.Cursor, t.Foreground)
	t.CursorText = color(s.CursorText, t.Background)
	t.Selection = color(s.Selection, t.Foreground.Alpha(0.25))
	for i, h := range s.ANSI {
		t.ANSI[i] = color(h, base.ANSI[i])
	}
	return t
}

// FindScheme is the scheme with id, built-in or among custom.
func FindScheme(id string, custom []Scheme) (Scheme, bool) {
	for _, list := range [][]Scheme{Schemes, custom} {
		for _, s := range list {
			if s.ID == id {
				return s, true
			}
		}
	}
	return Scheme{}, false
}

// parseHex reads #RRGGBB.
func parseHex(h string) (ui.Color, bool) {
	if len(h) != 7 || h[0] != '#' {
		return ui.Color{}, false
	}
	v, err := strconv.ParseUint(h[1:], 16, 32)
	if err != nil {
		return ui.Color{}, false
	}
	return ui.RGB(uint8(v>>16), uint8(v>>8), uint8(v)), true
}

// Schemes are the built-in color schemes, as their authors publish them.
var Schemes = []Scheme{
	{ID: "dracula", Name: "Dracula", Foreground: "#F8F8F2", Background: "#282A36", Cursor: "#F8F8F2", Selection: "#44475A",
		ANSI: [16]string{"#21222C", "#FF5555", "#50FA7B", "#F1FA8C", "#BD93F9", "#FF79C6", "#8BE9FD", "#F8F8F2",
			"#6272A4", "#FF6E6E", "#69FF94", "#FFFFA5", "#D6ACFF", "#FF92DF", "#A4FFFF", "#FFFFFF"}},
	{ID: "solarized-dark", Name: "Solarized Dark", Foreground: "#839496", Background: "#002B36", Cursor: "#93A1A1", Selection: "#073642",
		ANSI: [16]string{"#073642", "#DC322F", "#859900", "#B58900", "#268BD2", "#D33682", "#2AA198", "#EEE8D5",
			"#002B36", "#CB4B16", "#586E75", "#657B83", "#839496", "#6C71C4", "#93A1A1", "#FDF6E3"}},
	{ID: "solarized-light", Name: "Solarized Light", Foreground: "#657B83", Background: "#FDF6E3", Cursor: "#586E75", Selection: "#EEE8D5",
		ANSI: [16]string{"#073642", "#DC322F", "#859900", "#B58900", "#268BD2", "#D33682", "#2AA198", "#EEE8D5",
			"#002B36", "#CB4B16", "#586E75", "#657B83", "#839496", "#6C71C4", "#93A1A1", "#FDF6E3"}},
	{ID: "nord", Name: "Nord", Foreground: "#D8DEE9", Background: "#2E3440", Cursor: "#D8DEE9", Selection: "#434C5E",
		ANSI: [16]string{"#3B4252", "#BF616A", "#A3BE8C", "#EBCB8B", "#81A1C1", "#B48EAD", "#88C0D0", "#E5E9F0",
			"#4C566A", "#BF616A", "#A3BE8C", "#EBCB8B", "#81A1C1", "#B48EAD", "#8FBCBB", "#ECEFF4"}},
	{ID: "one-dark", Name: "One Dark", Foreground: "#ABB2BF", Background: "#282C34", Cursor: "#528BFF", Selection: "#3E4451",
		ANSI: [16]string{"#282C34", "#E06C75", "#98C379", "#E5C07B", "#61AFEF", "#C678DD", "#56B6C2", "#ABB2BF",
			"#5C6370", "#E06C75", "#98C379", "#E5C07B", "#61AFEF", "#C678DD", "#56B6C2", "#FFFFFF"}},
	{ID: "one-light", Name: "One Light", Foreground: "#383A42", Background: "#FAFAFA", Cursor: "#526FFF", Selection: "#E5E5E6",
		ANSI: [16]string{"#383A42", "#E45649", "#50A14F", "#C18401", "#4078F2", "#A626A4", "#0184BC", "#A0A1A7",
			"#696C77", "#E45649", "#50A14F", "#C18401", "#4078F2", "#A626A4", "#0184BC", "#FAFAFA"}},
	{ID: "gruvbox-dark", Name: "Gruvbox Dark", Foreground: "#EBDBB2", Background: "#282828", Cursor: "#EBDBB2", Selection: "#504945",
		ANSI: [16]string{"#282828", "#CC241D", "#98971A", "#D79921", "#458588", "#B16286", "#689D6A", "#A89984",
			"#928374", "#FB4934", "#B8BB26", "#FABD2F", "#83A598", "#D3869B", "#8EC07C", "#EBDBB2"}},
	{ID: "tokyo-night", Name: "Tokyo Night", Foreground: "#C0CAF5", Background: "#1A1B26", Cursor: "#C0CAF5", Selection: "#33467C",
		ANSI: [16]string{"#15161E", "#F7768E", "#9ECE6A", "#E0AF68", "#7AA2F7", "#BB9AF7", "#7DCFFF", "#A9B1D6",
			"#414868", "#F7768E", "#9ECE6A", "#E0AF68", "#7AA2F7", "#BB9AF7", "#7DCFFF", "#C0CAF5"}},
	{ID: "monokai", Name: "Monokai", Foreground: "#F8F8F2", Background: "#272822", Cursor: "#F8F8F0", Selection: "#49483E",
		ANSI: [16]string{"#272822", "#F92672", "#A6E22E", "#F4BF75", "#66D9EF", "#AE81FF", "#A1EFE4", "#F8F8F2",
			"#75715E", "#F92672", "#A6E22E", "#F4BF75", "#66D9EF", "#AE81FF", "#A1EFE4", "#F9F8F5"}},
	{ID: "catppuccin-mocha", Name: "Catppuccin Mocha", Foreground: "#CDD6F4", Background: "#1E1E2E", Cursor: "#F5E0DC", CursorText: "#1E1E2E", Selection: "#585B70",
		ANSI: [16]string{"#45475A", "#F38BA8", "#A6E3A1", "#F9E2AF", "#89B4FA", "#F5C2E7", "#94E2D5", "#BAC2DE",
			"#585B70", "#F38BA8", "#A6E3A1", "#F9E2AF", "#89B4FA", "#F5C2E7", "#94E2D5", "#A6ADC8"}},
}
