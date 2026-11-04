package app

import (
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestPaletteScore(t *testing.T) {
	it := paletteItem{title: "Split Right", sub: "Command", also: "Split Right"}
	for _, tc := range []struct {
		q    string
		want bool
	}{
		{"", true}, {"split", true}, {"right", true}, {"spl rig", true}, {"command", true}, {"sprt", true},
		{"left", false}, {"split left", false},
	} {
		if got := paletteScore(it, splitWords(tc.q)) > 0; got != tc.want {
			t.Errorf("%q: %v, want %v", tc.q, got, tc.want)
		}
	}
	words := splitWords("dark")
	if paletteScore(paletteItem{title: "Dark"}, words) <= paletteScore(paletteItem{title: "One Dark"}, words) ||
		paletteScore(paletteItem{title: "One Dark"}, words) <= paletteScore(paletteItem{title: "Ok", sub: "dark"}, words) {
		t.Error("a title starting with the word must come first, then a word of it")
	}
}

// openPaletteTyped opens the palette by its shortcut and types q.
func openPaletteTyped(t *testing.T, a *App, tt *ui.Tester, q string) {
	t.Helper()
	tt.Key(aiMods, ui.KeyP)
	tt.Frame()
	tt.Frame()
	if !a.palette.open || !tt.Focused(locales.In("en").CommandPalette()) {
		t.Fatalf("palette open %v; texts %q", a.palette.open, tt.Texts())
	}
	tt.Type(q)
	tt.Frame()
}

func TestCommandPalette(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	if _, err := a.store.Add(assets.Host{Name: "build box", Address: "10.0.0.9", User: "ci", Auth: assets.AuthAgent}); err != nil {
		t.Fatal(err)
	}

	// The shortcut opens it and closes it again.
	openPaletteTyped(t, a, tt, "")
	for _, s := range []string{m.SplitRight(), m.NewHost(), m.PaletteCommand()} {
		if !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
	tt.Key(aiMods, ui.KeyP)
	tt.Frame()
	if a.palette.open {
		t.Fatal("the shortcut does not close the palette")
	}

	// A setting opens its page.
	openPaletteTyped(t, a, tt, "osc 52")
	if !tt.HasText(m.PaletteInSettings(m.TerminalSection())) {
		t.Fatalf("no setting in %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.palette.open || a.page != pageSettings || a.prefNow() != prefTerminal {
		t.Fatalf("open %v, page %v %q", a.palette.open, a.page, a.prefNow())
	}
	a.closeSettings()
	tt.Frame()

	// Down chooses the next row: "Dark" the theme, then the schemes
	// with a word "Dark".
	openPaletteTyped(t, a, tt, "dark")
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.settings.TerminalScheme != "solarized-dark" || a.settings.Theme == "dark" {
		t.Fatalf("scheme %q, theme %q", a.settings.TerminalScheme, a.settings.Theme)
	}

	// Hosts are found by name; a click runs a row.
	openPaletteTyped(t, a, tt, "build")
	if !hasExact(tt, "build box") {
		t.Errorf("no host in %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.palette.open {
		t.Fatal("Esc does not close the palette")
	}
	openPaletteTyped(t, a, tt, "light")
	mustClick(t, tt, m.ThemeLight())
	tt.Frame()
	if a.palette.open || a.settings.Theme != "light" {
		t.Fatalf("open %v, theme %q", a.palette.open, a.settings.Theme)
	}
	openPaletteTyped(t, a, tt, "privacy")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !a.settings.Privacy {
		t.Fatal("privacy mode stays off")
	}

	openPaletteTyped(t, a, tt, "zzzz nothing")
	if !tt.HasText(m.PaletteNoResults()) {
		t.Errorf("no empty note in %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	// The status bar opens it too.
	mustClick(t, tt, m.CommandPalette())
	tt.Frame()
	if !a.palette.open || a.palette.query != "" {
		t.Fatalf("open %v, query %q", a.palette.open, a.palette.query)
	}
}
